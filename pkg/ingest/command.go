// Package ingest imports document directories into the remote JAR index.
package ingest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"flag"
	"fmt"
	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/brito-rafa/jar/pkg/connection"
	"github.com/brito-rafa/jar/pkg/duck"
	"github.com/brito-rafa/jar/pkg/inference"
	"os"
	"time"
)

// Run imports documents without loading a chat model. Status loads no model.
func Run(args []string) error {
	f := flag.NewFlagSet("jar-index", flag.ContinueOnError)
	var c connection.Flags
	c.Register(f)
	docs := f.String("docs", "", "Semicolon-separated directories to import recursively (.doc/.docx/.pdf/.txt)")
	embedding := f.String("embedding-model", inference.DefaultEmbedding, "Embedding model used for documents and questions")
	dimensions := f.Int("dimensions", 768, "Values per embedding vector; must match the model output (Granite 278M: 768)")
	documentWorkers := f.Int("document-workers", 20, "Concurrent document converters (1-64)")
	embeddingWorkers := f.Int("embedding-workers", 4, "Concurrent embedding request workers (1-8)")
	embeddingBatch := f.Int("embedding-batch-size", 8, "Texts per embedding request (1-128)")
	insertBatch := f.Int("insert-batch-size", 128, "Rows per database insert (1-512)")
	force := f.Bool("force", false, "Rebuild even if the corpus is unchanged (useful for worker comparisons)")
	metadataOnly := f.Bool("metadata", false, "Build book/date metadata from stored filenames without re-embedding")
	statusOnly := f.Bool("status", false, "Show database status without reading documents or loading models")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *dimensions <= 0 || *embedding == "" {
		return fmt.Errorf("embedding-model and positive dimensions are required")
	}
	options := duck.LoadOptions{EmbeddingWorkers: *embeddingWorkers, EmbeddingBatchSize: *embeddingBatch, InsertBatchSize: *insertBatch}
	if err := options.Validate(); err != nil {
		return err
	}
	if *documentWorkers < 1 || *documentWorkers > 64 {
		return fmt.Errorf("document-workers must be between 1 and 64")
	}
	if *metadataOnly && *statusOnly {
		return fmt.Errorf("choose -metadata or -status")
	}
	if !*statusOnly && !*metadataOnly {
		if _, err := documentDirectories(*docs); err != nil {
			return err
		}
	}

	db, err := c.Open()
	if err != nil {
		return err
	}
	defer db.Close()
	if *statusOnly {
		return printStatus(db, c.Endpoint)
	}
	if *metadataOnly {
		return duck.BuildCatalog(db)
	}
	extractStart := time.Now()
	prepared, err := prepareDocuments(*docs, *documentWorkers)
	if err != nil {
		return err
	}
	defer os.Remove(prepared)
	fmt.Printf("Document extraction completed in %s\n", time.Since(extractStart))
	data, err := os.ReadFile(prepared)
	if err != nil {
		return err
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(append([]byte(fmt.Sprintf("jar-v4-dedup:%s:%d\n", *embedding, *dimensions)), data...)))
	existing, err := duck.Status(db)
	if err != nil {
		return err
	}
	if !*force && existing.Ready && existing.Fingerprint == fingerprint {
		if err := duck.EnsureCatalog(db); err != nil {
			return err
		}
		fmt.Println("Index unchanged; reusing stored embeddings.")
		return printStatus(db, c.Endpoint)
	}
	info, _, err := inference.Install(*embedding, "")
	if err != nil {
		return err
	}
	krn, err := inference.New(info, model.WithNSeqMax(*embeddingWorkers))
	if err != nil {
		return err
	}
	defer krn.Unload(context.Background())
	if err := duck.Index(db, krn, *dimensions, prepared, fingerprint, *embedding, options); err != nil {
		return err
	}
	return printStatus(db, c.Endpoint)
}

func printStatus(db *sql.DB, endpoint string) error {
	s, err := duck.Status(db)
	if err != nil {
		return err
	}
	fmt.Printf("Database: %s\nReady: %t\nChunks: %d\nEmbedding model: %s\nDimensions: %d\nIndexed at (UTC): %s\n", endpoint, s.Ready, s.Chunks, s.EmbeddingModel, s.Dimensions, s.IndexedAt)
	if !s.Ready || s.Chunks == 0 {
		fmt.Println("\nThe database seems to be empty: no ready JAR document index was found.")
		fmt.Println("Load documents with: make index DOCS=\"/path/to/documents\"")
	}
	return nil
}
