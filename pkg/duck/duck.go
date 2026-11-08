// Package duck provides basic duckdb support for the
package duck

import (
	"context"
	"crypto/sha256"
	sqlpkg "database/sql"
	"encoding/json"
	"fmt"
	"github.com/brito-rafa/jar/pkg/catalog"
	"time"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/brito-rafa/jar/pkg/inference"
	_ "github.com/duckdb/duckdb-go/v2"
)

// Index replaces the corpus transactionally on an already connected server.
func Index(db *sqlpkg.DB, krn inference.Embedder, dimensions int, chunksFile, fingerprint, embeddingModel string, options ...LoadOptions) error {
	opts := LoadOptions{EmbeddingWorkers: 1, EmbeddingBatchSize: 8, InsertBatchSize: 128}
	if len(options) > 0 {
		opts = options[0]
	}
	if err := opts.Validate(); err != nil {
		return err
	}
	totalStart := time.Now()
	success := false

	// -------------------------------------------------------------------------

	// Provision VSS on the server, including remotely managed DuckDB instances.
	if err := remoteExec(db, "INSTALL vss"); err != nil {
		return fmt.Errorf("install server VSS (server needs extension download access): %w", err)
	}
	if err := remoteExec(db, "LOAD vss"); err != nil {
		return fmt.Errorf("load server VSS: %w", err)
	}
	var err error
	var sql string
	err = remoteExec(db, "SET hnsw_enable_experimental_persistence = true;")
	if err != nil {
		return fmt.Errorf("error setting HNSW persistence: %w", err)
	}

	// -------------------------------------------------------------------------

	if err := remoteExec(db, "CREATE TABLE IF NOT EXISTS jar_index_metadata (fingerprint VARCHAR, embedding_model VARCHAR, dimensions INTEGER, indexed_at TIMESTAMP)"); err != nil {
		return err
	}
	if err := remoteExec(db, "BEGIN TRANSACTION"); err != nil {
		return err
	}
	defer func() {
		if !success {
			remoteExec(db, "ROLLBACK")
		}
	}()
	if err := remoteExec(db, "DROP TABLE IF EXISTS item_sources"); err != nil {
		return err
	}
	if err := createCatalog(db); err != nil {
		return err
	}
	if err := remoteExec(db, "DROP TABLE IF EXISTS items"); err != nil {
		return err
	}
	sql = `
  CREATE TABLE items (
			id        INTEGER   PRIMARY KEY,
			text      VARCHAR,
 content_hash VARCHAR UNIQUE,
 sources VARCHAR,
			embedding FLOAT[%d]
		);
	`

	sql = fmt.Sprintf(sql, dimensions)

	if err = remoteExec(db, sql); err != nil {
		return fmt.Errorf("error creating table: %w", err)
	}

	// -------------------------------------------------------------------------

	fmt.Print("LOADING DATA...")
	t := time.Now()

	if err := loadChunks(db, krn, chunksFile, dimensions, opts); err != nil {
		return fmt.Errorf("error loading data: %w", err)
	}

	fmt.Printf("Loaded data in %v\n", time.Since(t))

	// -------------------------------------------------------------------------

	sql = `
		CREATE INDEX idx_embedding_%x_%d ON items
		USING HNSW (embedding)
		WITH (metric = 'cosine');
	`

	hnswStart := time.Now()
	if err = remoteExec(db, fmt.Sprintf(sql, sha256.Sum256([]byte(fingerprint)), time.Now().UnixNano())); err != nil {
		return fmt.Errorf("error creating HNSW index: %w", err)
	}

	fmt.Printf("Vector index built in %s\n", time.Since(hnswStart))
	if err := remoteExec(db, "DELETE FROM jar_index_metadata"); err != nil {
		return err
	}
	if err := remoteExec(db, "INSERT INTO jar_index_metadata VALUES (?, ?, ?, current_timestamp)", fingerprint, embeddingModel, dimensions); err != nil {
		return err
	}
	if err := remoteExec(db, "COMMIT"); err != nil {
		return err
	}
	success = true
	fmt.Printf("Total database load time: %s\n", time.Since(totalStart))
	return nil
}

func loadChunks(db *sqlpkg.DB, krn inference.Embedder, file string, dimensions int, opts LoadOptions) error {
	passages, duplicates, err := uniquePassages(file)
	if err != nil {
		return err
	}
	fmt.Printf("Deduplication: %d unique chunks; %d duplicate occurrences collapsed (all source locations retained).\n", len(passages), duplicates)
	fmt.Printf("Embedding workers: %d; embedding batch: %d; insert batch: %d; completed embedding buffer: %d batches (up to %d chunks)\n", opts.EmbeddingWorkers, opts.EmbeddingBatchSize, opts.InsertBatchSize, opts.bufferBatches(), opts.bufferBatches()*opts.EmbeddingBatchSize)
	return runPipeline(context.Background(), passages, opts, func(ctx context.Context, texts []string) ([][]float32, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		resp, err := krn.Embeddings(ctx, model.D{"input": texts, "truncate": true, "truncate_direction": "right"})
		if err != nil {
			return nil, err
		}
		vectors := make([][]float32, len(texts))
		seen := make([]bool, len(texts))
		for _, data := range resp.Data {
			if data.Index < 0 || data.Index >= len(texts) || seen[data.Index] {
				return nil, fmt.Errorf("invalid embedding response index %d", data.Index)
			}
			if len(data.Embedding) != dimensions {
				return nil, fmt.Errorf("embedding dimension %d does not match configured %d", len(data.Embedding), dimensions)
			}
			seen[data.Index] = true
			vectors[data.Index] = data.Embedding
		}
		return vectors, nil
	}, func(rows []embeddedPassage) error {
		query, err := insertBatchSQL(rows)
		if err != nil {
			return err
		}
		if err := remoteExec(db, query); err != nil {
			return err
		}
		origins, err := originInsert(rows)
		if err != nil {
			return err
		}
		if origins != "" {
			return remoteExec(db, origins)
		}
		return nil
	})
}

// =============================================================================

type Document struct {
	Origins    []catalog.Origin
	Sources    []string
	ID         int
	Text       string
	Embedding  []float64
	Similarity float64
}

func Search(db *sqlpkg.DB, queryVector []float32, limit int, filters ...SourceFilter) ([]Document, error) {
	filter := SourceFilter{}
	if len(filters) > 0 {
		filter = filters[0]
	}
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	where := ""
	args := []any{queryVector}
	if filter.Active() {
		has, err := hasCatalog(db)
		if err != nil {
			return nil, err
		}
		if !has {
			return nil, fmt.Errorf("source metadata is missing; run make metadata")
		}
		condition, more := sourceWhere(filter, "s.")
		where = "WHERE EXISTS (SELECT 1 FROM item_sources s WHERE s.chunk_id=items.id AND " + condition + ")"
		args = append(args, more...)
	}
	var sourceColumns int
	if err := remoteRow(db, "SELECT count(*) FROM information_schema.columns WHERE table_name='items' AND column_name='sources'").Scan(&sourceColumns); err != nil {
		return nil, err
	}
	sourceExpr := "NULL::VARCHAR"
	if sourceColumns > 0 {
		sourceExpr = "sources"
	}
	sql := `
  SELECT
			id,
			text,
 %s AS sources,
			embedding,
			array_cosine_similarity(embedding, ?::FLOAT[%d]) as similarity
		FROM
			items
 %s
		ORDER BY
			similarity DESC
		LIMIT %d;
	`

	sql = fmt.Sprintf(sql, sourceExpr, len(queryVector), where, limit)

	rows, err := remoteQuery(db, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("error querying similar items: %w", err)
	}
	defer rows.Close()

	var docs []Document

	for rows.Next() {
		var doc Document
		var embedding []any
		var sources sqlpkg.NullString

		if err := rows.Scan(&doc.ID, &doc.Text, &sources, &embedding, &doc.Similarity); err != nil {
			return nil, fmt.Errorf("error scanning row: %w", err)
		}

		if sources.Valid {
			if err := json.Unmarshal([]byte(sources.String), &doc.Sources); err != nil {
				return nil, fmt.Errorf("source metadata: %w", err)
			}
		}
		doc.Embedding = make([]float64, len(embedding))
		for i, v := range embedding {
			doc.Embedding[i] = float64(v.(float32))
		}

		docs = append(docs, doc)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := attachOrigins(db, docs, filter); err != nil {
		return nil, err
	}
	return docs, nil
}
