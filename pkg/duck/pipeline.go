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

// A bounded embedding pool overlaps GPU work with a single transactional writer.
// On errors, cancel and join every worker before the model can be unloaded.
func runPipeline(ctx context.Context, passages []passage, opts LoadOptions, embed embedFunc, write writeFunc) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
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
				vectors, err := embed(ctx, texts)
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
				select {
				case results <- r:
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
	written := 0
	lastProgress := time.Now()
	flush := func(n int) {
		if err := write(pending[:n]); err != nil {
			firstErr = err
			cancel()
			return
		}
		written += n
		pending = pending[n:]
		if time.Since(lastProgress) >= 500*time.Millisecond || written == len(passages) {
			fmt.Printf("Indexed %d of %d unique chunks\n", written, len(passages))
			lastProgress = time.Now()
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
