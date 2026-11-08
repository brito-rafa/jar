package duck

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeduplicatePreservesOriginsAndMeaningfulDifferences(t *testing.T) {
	file := filepath.Join(t.TempDir(), "input")
	data := "<CHUNK>Source: a.pdf | page 1\nCafé   consciente.</CHUNK>\n<CHUNK>Source: b.doc | section 1\nCafe\u0301 consciente.</CHUNK>\n<CHUNK>Source: c.txt | section 1\nCafe consciente.</CHUNK>\n<CHUNK>Source: d.txt | section 1\nConscious coffee.</CHUNK>"
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	p, duplicates, err := uniquePassages(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 3 || duplicates != 1 || len(p[0].Sources) != 2 {
		t.Fatalf("%+v duplicates=%d", p, duplicates)
	}
	if p[0].Text != "Café consciente." || p[0].Sources[1] != "b.doc | section 1" {
		t.Fatal(p[0])
	}
}

func TestEmbeddingPoolIsBoundedAndKeepsVectorAssociation(t *testing.T) {
	opts := LoadOptions{EmbeddingWorkers: 2, EmbeddingBatchSize: 1, InsertBatchSize: 3}
	var passages []passage
	for i := range 9 {
		passages = append(passages, passage{ID: i, Text: strconv.Itoa(i)})
	}
	var active, peak atomic.Int32
	entered := make(chan struct{}, 2)
	gate := make(chan struct{})
	got := map[int]float32{}
	batches := []int{}
	done := make(chan error, 1)
	go func() {
		done <- runPipeline(context.Background(), passages, opts, func(ctx context.Context, texts []string) ([][]float32, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old; old = peak.Load() {
				if peak.CompareAndSwap(old, n) {
					break
				}
			}
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			v, _ := strconv.Atoi(texts[0])
			return [][]float32{{float32(v)}}, nil
		}, func(rows []embeddedPassage) error {
			batches = append(batches, len(rows))
			for _, row := range rows {
				got[row.ID] = row.Vector[0]
			}
			return nil
		})
	}()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			close(gate)
			t.Fatal("workers did not run concurrently")
		}
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if peak.Load() != 2 || active.Load() != 0 || len(got) != 9 {
		t.Fatalf("peak=%d active=%d got=%v", peak.Load(), active.Load(), got)
	}
	for id, v := range got {
		if float32(id) != v {
			t.Fatalf("vector mismatch id=%d vector=%f", id, v)
		}
	}
	for _, size := range batches {
		if size > 3 {
			t.Fatal("oversized insert", size)
		}
	}
}

func TestPipelineCancelsAndJoinsOnWriteFailure(t *testing.T) {
	expected := errors.New("insert failed")
	var active atomic.Int32
	passages := make([]passage, 20)
	done := make(chan error, 1)
	go func() {
		done <- runPipeline(context.Background(), passages, LoadOptions{EmbeddingWorkers: 2, EmbeddingBatchSize: 1, InsertBatchSize: 1}, func(ctx context.Context, _ []string) ([][]float32, error) {
			active.Add(1)
			defer active.Add(-1)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
				return [][]float32{{1}}, nil
			}
		}, func([]embeddedPassage) error { return expected })
	}()
	select {
	case err := <-done:
		if !errors.Is(err, expected) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pipeline leaked workers")
	}
	if active.Load() != 0 {
		t.Fatal("workers still running")
	}
}

func TestBatchInsertKeepsUnicodeNullsAndSources(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE items (id INT, text VARCHAR, embedding FLOAT[2], content_hash VARCHAR, sources VARCHAR)"); err != nil {
		t.Fatal(err)
	}
	rows := []embeddedPassage{{passage{ID: 1, Text: "citação '\x00", Hash: "one", Sources: []string{"a.pdf | page 1", "b.doc | section 1"}}, []float32{1, 2}}, {passage{ID: 2, Text: "second", Hash: "two"}, []float32{3, 4}}}
	query, err := insertBatchSQL(rows)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
	var text, sources string
	if err := db.QueryRow("SELECT text,sources FROM items WHERE id=1").Scan(&text, &sources); err != nil {
		t.Fatal(err)
	}
	if text != rows[0].Text || !strings.Contains(sources, "b.doc") {
		t.Fatalf("%q %s", text, sources)
	}
}

// A single embedding worker must continue filling the configured buffer while
// the database writer is blocked, rather than being limited to one queued batch.
func TestEmbeddingBufferIndependentOfWorkerCount(t *testing.T) {
	opts := LoadOptions{EmbeddingWorkers: 1, EmbeddingBatchSize: 1, InsertBatchSize: 1, EmbeddingBufferBatches: 3}
	passages := make([]passage, 8)
	for i := range passages {
		passages[i] = passage{ID: i, Text: strconv.Itoa(i)}
	}
	embedded := make(chan int, 8)
	writing := make(chan struct{})
	release := make(chan struct{})
	var closeOnce sync.Once
	unblock := func() { closeOnce.Do(func() { close(release) }) }
	defer unblock()
	done := make(chan error, 1)
	writes := 0
	go func() {
		done <- runPipeline(context.Background(), passages, opts, func(ctx context.Context, texts []string) ([][]float32, error) {
			id, _ := strconv.Atoi(texts[0])
			embedded <- id
			return [][]float32{{1, 0}}, nil
		}, func(rows []embeddedPassage) error {
			if writes == 0 {
				close(writing)
				<-release
			}
			writes += len(rows)
			return nil
		})
	}()
	select {
	case <-writing:
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not start")
	}
	// First batch is held by the writer; three more fit in the queue. The worker
	// can then compute the fifth batch before blocking on delivery.
	for i := 0; i < 5; i++ {
		select {
		case <-embedded:
		case <-time.After(3 * time.Second):
			t.Fatal("worker stalled before filling the independent buffer")
		}
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pipeline did not finish")
	}
	if writes != len(passages) {
		t.Fatalf("wrote %d of %d passages", writes, len(passages))
	}
}
