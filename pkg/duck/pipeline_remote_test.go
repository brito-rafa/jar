package duck

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// Uses synthetic vectors to test remote ingestion without competing for the GPU.
func TestRemoteLoadPoolAndBatches(t *testing.T) {
	endpoint := os.Getenv("JAR_LOAD_TEST_URL")
	if endpoint == "" {
		t.Skip("set JAR_LOAD_TEST_URL for isolated remote load checks")
	}
	db, err := OpenRemote(endpoint, os.Getenv("JAR_DB_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var passages []passage
	for i := range 512 {
		passages = append(passages, passage{ID: i, Text: fmt.Sprintf("Passage %d", i), Hash: fmt.Sprintf("hash-%d", i), Sources: []string{"first.pdf | page 1", "copy.doc | section 1"}})
	}
	for _, tc := range []struct {
		name    string
		options LoadOptions
	}{{"serial", LoadOptions{EmbeddingWorkers: 1, EmbeddingBatchSize: 1, InsertBatchSize: 1}}, {"pooled", LoadOptions{EmbeddingWorkers: 2, EmbeddingBatchSize: 8, InsertBatchSize: 128}}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := remoteExec(db, "DROP TABLE IF EXISTS items"); err != nil {
				t.Fatal(err)
			}
			if err := remoteExec(db, "CREATE TABLE items (id INT PRIMARY KEY,text VARCHAR,embedding FLOAT[2],content_hash VARCHAR UNIQUE,sources VARCHAR)"); err != nil {
				t.Fatal(err)
			}
			if err := remoteExec(db, "BEGIN"); err != nil {
				t.Fatal(err)
			}
			committed := false
			defer func() {
				if !committed {
					remoteExec(db, "ROLLBACK")
				}
			}()
			calls := 0
			start := time.Now()
			err := runPipeline(context.Background(), passages, tc.options, func(_ context.Context, texts []string) ([][]float32, error) {
				v := make([][]float32, len(texts))
				for i := range texts {
					v[i] = []float32{1, 0}
				}
				return v, nil
			}, func(rows []embeddedPassage) error {
				calls++
				query, err := insertBatchSQL(rows)
				if err != nil {
					return err
				}
				return remoteExec(db, query)
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := remoteExec(db, "COMMIT"); err != nil {
				t.Fatal(err)
			}
			committed = true
			t.Logf("%s: rows=%d insert_requests=%d duration=%s (synthetic vectors; not an embedding benchmark)", tc.name, len(passages), calls, time.Since(start))
			docs, err := Search(db, []float32{1, 0}, 5)
			if err != nil {
				t.Fatal(err)
			}
			if len(docs) != 5 || len(docs[0].Sources) != 2 {
				t.Fatalf("lost sources: %+v", docs)
			}
		})
	}
}
