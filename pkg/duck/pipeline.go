package duck

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/unicode/norm"
)

type LoadOptions struct{ EmbeddingWorkers, EmbeddingBatchSize, InsertBatchSize int }

func (o LoadOptions) Validate() error {
	if o.EmbeddingWorkers < 1 || o.EmbeddingWorkers > 8 {
		return fmt.Errorf("embedding-workers must be between 1 and 8")
	}
	if o.EmbeddingBatchSize < 1 || o.EmbeddingBatchSize > 128 {
		return fmt.Errorf("embedding-batch-size must be between 1 and 128")
	}
	if o.InsertBatchSize < 1 || o.InsertBatchSize > 512 {
		return fmt.Errorf("insert-batch-size must be between 1 and 512")
	}
	return nil
}

type passage struct {
	ID         int
	Text, Hash string
	Sources    []string
}
type embeddedPassage struct {
	passage
	Vector []float32
}

func uniquePassages(file string) ([]passage, int, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, 0, err
	}
	blocks := regexp.MustCompile(`(?s)<CHUNK>(.*?)</CHUNK>`).FindAllStringSubmatch(string(data), -1)
	if len(blocks) == 0 {
		return nil, 0, fmt.Errorf("no document passages found")
	}
	var result []passage
	seen := map[string]int{}
	for _, block := range blocks {
		text := strings.TrimSpace(block[1])
		label := ""
		if strings.HasPrefix(text, "Source: ") {
			first, rest, _ := strings.Cut(text, "\n")
			label = strings.TrimPrefix(first, "Source: ")
			text = rest
		}
		text = norm.NFC.String(strings.Join(strings.Fields(text), " "))
		if text == "" {
			continue
		}
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
		if i, ok := seen[hash]; ok {
			exists := slices.Contains(result[i].Sources, label)
			if label != "" && !exists {
				result[i].Sources = append(result[i].Sources, label)
			}
			continue
		}
		p := passage{ID: len(result), Text: text, Hash: hash}
		if label != "" {
			p.Sources = []string{label}
		}
		seen[hash] = len(result)
		result = append(result, p)
	}
	if len(result) == 0 {
		return nil, 0, fmt.Errorf("no nonempty document passages found")
	}
	return result, len(blocks) - len(result), nil
}

type embedFunc func(context.Context, []string) ([][]float32, error)
type writeFunc func([]embeddedPassage) error

type pipelineMetrics struct {
	mu                                                              sync.Mutex
	requests, failures, inserts                                     int
	embeddingTotal, embeddingMax, insertTotal, insertMax, queueWait time.Duration
}

func (m *pipelineMetrics) embedding(elapsed time.Duration, failed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests++
	m.embeddingTotal += elapsed
	if elapsed > m.embeddingMax {
		m.embeddingMax = elapsed
	}
	if failed {
		m.failures++
	}
}
func (m *pipelineMetrics) queue(elapsed time.Duration) {
	m.mu.Lock()
	m.queueWait += elapsed
	m.mu.Unlock()
}
func (m *pipelineMetrics) insert(elapsed time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inserts++
	m.insertTotal += elapsed
	if elapsed > m.insertMax {
		m.insertMax = elapsed
	}
}
func (m *pipelineMetrics) report(written, total int, elapsed time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var embedAvg, insertAvg time.Duration
	if m.requests > 0 {
		embedAvg = m.embeddingTotal / time.Duration(m.requests)
	}
	if m.inserts > 0 {
		insertAvg = m.insertTotal / time.Duration(m.inserts)
	}
	rate := 0.0
	if elapsed > 0 {
		rate = float64(written) / elapsed.Seconds()
	}
	fmt.Printf("Load metrics: %d/%d written (pending commit); elapsed %.1fs; %.2f chunks/s | embedding requests %d, failed %d, avg %.3fs, max %.3fs | insert calls %d, avg %.3fs, max %.3fs, total %.3fs | result-queue wait %.3fs (summed across workers)\n", written, total, elapsed.Seconds(), rate, m.requests, m.failures, embedAvg.Seconds(), m.embeddingMax.Seconds(), m.inserts, insertAvg.Seconds(), m.insertMax.Seconds(), m.insertTotal.Seconds(), m.queueWait.Seconds())
}

// A bounded embedding pool overlaps GPU work with a single transactional writer.
// On errors, cancel and join every worker before the model can be unloaded.
func runPipeline(ctx context.Context, passages []passage, opts LoadOptions, embed embedFunc, write writeFunc) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	started := time.Now()
	var metrics pipelineMetrics
	written := 0
	defer func() { metrics.report(written, len(passages), time.Since(started)) }()
	jobs := make(chan []passage, opts.EmbeddingWorkers)
	type result struct {
		rows []embeddedPassage
		err  error
	}
	results := make(chan result, opts.EmbeddingWorkers)
	var wg sync.WaitGroup
	wg.Go(func() {
		defer close(jobs)
		for start := 0; start < len(passages); start += opts.EmbeddingBatchSize {
			batch := passages[start:min(start+opts.EmbeddingBatchSize, len(passages))]
			select {
			case jobs <- batch:
			case <-ctx.Done():
				return
			}
		}
	})
	for range opts.EmbeddingWorkers {
		wg.Go(func() {
			for batch := range jobs {
				if ctx.Err() != nil {
					return
				}
				texts := make([]string, len(batch))
				for i, p := range batch {
					texts[i] = p.Text
				}
				requestStart := time.Now()
				vectors, err := embed(ctx, texts)
				metrics.embedding(time.Since(requestStart), err != nil)
				if err != nil {
					err = fmt.Errorf("embed chunk batch starting at %d (%d chunks): %w", batch[0].ID, len(batch), err)
				}
				r := result{err: err}
				if err == nil && len(vectors) != len(batch) {
					r.err = fmt.Errorf("embedding response count %d does not match %d passages", len(vectors), len(batch))
				}
				if r.err == nil {
					for i, p := range batch {
						if len(vectors[i]) == 0 {
							r.err = fmt.Errorf("empty embedding for chunk %d", p.ID)
							break
						}
						r.rows = append(r.rows, embeddedPassage{p, vectors[i]})
					}
				}
				queueStart := time.Now()
				select {
				case results <- r:
					metrics.queue(time.Since(queueStart))
				case <-ctx.Done():
					return
				}
				if r.err != nil {
					return
				}
			}
		})
	}
	go func() { wg.Wait(); close(results) }()
	var firstErr error
	var pending []embeddedPassage
	lastProgress := time.Now()
	flush := func(n int) {
		insertStart := time.Now()
		err := write(pending[:n])
		metrics.insert(time.Since(insertStart))
		if err != nil {
			firstErr = err
			cancel()
			return
		}
		written += n
		pending = pending[n:]
		if time.Since(lastProgress) >= 500*time.Millisecond || written == len(passages) {
			fmt.Printf("Indexed %d of %d unique chunks\n", written, len(passages))
			lastProgress = time.Now()
			metrics.report(written, len(passages), time.Since(started))
		}
	}
	for r := range results {
		if firstErr != nil {
			continue
		}
		if r.err != nil {
			firstErr = r.err
			cancel()
			continue
		}
		pending = append(pending, r.rows...)
		for len(pending) >= opts.InsertBatchSize && firstErr == nil {
			flush(opts.InsertBatchSize)
		}
	}
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(pending) > 0 {
		flush(len(pending))
	}
	return firstErr
}

func insertBatchSQL(rows []embeddedPassage) (string, error) {
	var b strings.Builder
	b.WriteString("INSERT INTO items (id,text,embedding,content_hash,sources) VALUES ")
	for i, row := range rows {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('(')
		sources, err := json.Marshal(row.Sources)
		if err != nil {
			return "", err
		}
		for j, value := range []any{row.ID, row.Text, row.Vector, row.Hash, string(sources)} {
			if j > 0 {
				b.WriteByte(',')
			}
			literal, err := remoteLiteral(value)
			if err != nil {
				return "", err
			}
			b.WriteString(literal)
		}
		b.WriteByte(')')
	}
	return b.String(), nil
}
