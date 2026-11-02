package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var errPasswordProtected = errors.New("document requires a password")

// prepareDocuments walks in lexical order, skips symlinks and hidden directories,
// and writes temporary chunks. It never modifies the source directory.
func prepareDocuments(root string, workerCounts ...int) (string, error) {
	roots, err := documentDirectories(root)
	if err != nil {
		return "", err
	}
	workers := 4
	if len(workerCounts) > 0 {
		workers = workerCounts[0]
	}
	if workers < 1 || workers > 64 {
		return "", fmt.Errorf("document-workers must be between 1 and 64")
	}
	var paths []string
	owners := map[string]string{}
	labels := map[string]string{}
	basenames := map[string]int{}
	for _, dir := range roots {
		basenames[filepath.Base(dir)]++
	}
	for _, root := range roots {
		prefix := ""
		if len(roots) > 1 {
			prefix = filepath.Base(root)
			if prefix == string(filepath.Separator) {
				prefix = "root"
			}
			if basenames[filepath.Base(root)] > 1 {
				prefix += fmt.Sprintf("-%x", sha256.Sum256([]byte(root)))[:9]
			}
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if d.IsDir() {
				if path != root && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "~$") {
				return nil
			}
			switch strings.ToLower(filepath.Ext(path)) {
			case ".doc", ".docx", ".pdf", ".txt":
				if _, seen := owners[path]; !seen {
					paths = append(paths, path)
					owners[path] = root
					labels[path] = prefix
				}
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	type result struct {
		chunks  []string
		skipped bool
		err     error
	}
	results := make([]result, len(paths))
	jobs := make(chan int, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				results[i].chunks, results[i].skipped, results[i].err = readDocument(owners[paths[i]], paths[i])
				if labels[paths[i]] != "" {
					for j, chunk := range results[i].chunks {
						results[i].chunks[j] = strings.Replace(chunk, "Source: ", "Source: "+labels[paths[i]]+"/", 1)
					}
				}
			}
		})
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	var chunks []string
	files := 0
	for i, result := range results {
		if result.err != nil {
			return "", fmt.Errorf("read %s: %w", paths[i], result.err)
		}
		if !result.skipped {
			files++
			chunks = append(chunks, result.chunks...)
		}
	}
	if len(chunks) == 0 {
		return "", fmt.Errorf("no supported documents in %s; use .doc, .docx, .pdf, or .txt", strings.Join(roots, "; "))
	}
	f, err := os.CreateTemp("", "jar-documents-*.chunks")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(f.Name())
		}
	}()
	for _, chunk := range chunks {
		// Keep document text from introducing chunk delimiters.
		chunk = strings.ReplaceAll(strings.ReplaceAll(chunk, "<CHUNK>", "&lt;CHUNK&gt;"), "</CHUNK>", "&lt;/CHUNK&gt;")
		if _, err := fmt.Fprintf(f, "<CHUNK>\n%s\n</CHUNK>\n", chunk); err != nil {
			return "", err
		}
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	ok = true
	fmt.Printf("Imported %d documents recursively into %d chunks\n", files, len(chunks))
	return f.Name(), nil
}

func extractText(path string) (string, error) {
	if strings.EqualFold(filepath.Ext(path), ".txt") {
		b, err := os.ReadFile(path)
		return string(b), err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	if strings.EqualFold(filepath.Ext(path), ".pdf") {
		tool, err := exec.LookPath("pdftotext")
		if err != nil {
			return "", fmt.Errorf("pdf extraction requires pdftotext: install with brew install poppler")
		}
		cmd = exec.CommandContext(ctx, tool, "-enc", "UTF-8", path, "-")
	} else {
		tool, err := exec.LookPath("textutil")
		if err != nil {
			return "", fmt.Errorf("word extraction requires macOS textutil")
		}
		cmd = exec.CommandContext(ctx, tool, "-convert", "txt", "-stdout", "-encoding", "UTF-8", "-noload", "--", path)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil && strings.EqualFold(filepath.Ext(path), ".pdf") && strings.Contains(strings.ToLower(stderr.String()), "incorrect password") {
		return "", errPasswordProtected
	}
	binaryOutput := bytes.ContainsRune(b, 0)
	if strings.EqualFold(filepath.Ext(path), ".doc") && (err != nil || strings.TrimSpace(string(b)) == "" || bytes.ContainsRune(b, 0)) {
		legacy, lookupErr := exec.LookPath("wvWare")
		if lookupErr != nil {
			return "", fmt.Errorf("textutil could not extract this legacy Word document; install fallback with brew install wv")
		}
		fallback := exec.CommandContext(ctx, legacy, "-x", "wvText.xml", "-c", "UTF-8", "-1", path)
		// Keep any images or auxiliary conversion files away from the originals.
		scratch, tempErr := os.MkdirTemp("", "jar-word-*")
		if tempErr != nil {
			return "", tempErr
		}
		defer os.RemoveAll(scratch)
		fallback.Dir = scratch
		stderr.Reset()
		fallback.Stderr = &stderr
		b, err = fallback.Output()
		if err != nil || strings.TrimSpace(string(b)) == "" || bytes.ContainsRune(b, 0) {
			// Word 2.0 can be misread as raw text by textutil and is not reliably
			// supported by wvWare. Try LibreOffice without writing beside the source.
			office, lookupErr := exec.LookPath("soffice")
			if lookupErr != nil {
				office = "/Applications/LibreOffice.app/Contents/MacOS/soffice"
				if _, statErr := os.Stat(office); statErr != nil {
					office = ""
				}
			}
			if office != "" {
				profile := (&url.URL{Scheme: "file", Path: filepath.Join(scratch, "profile")}).String()
				convert := exec.CommandContext(ctx, office, "-env:UserInstallation="+profile, "--headless", "--convert-to", "txt:Text", "--outdir", scratch, path)
				convert.Dir = scratch
				if output, convertErr := convert.CombinedOutput(); convertErr != nil {
					return "", fmt.Errorf("legacy Word conversion: %w: %s", convertErr, strings.TrimSpace(string(output)))
				}
				outputPath := filepath.Join(scratch, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".txt")
				b, err = os.ReadFile(outputPath)
			} else if binaryOutput {
				return "", fmt.Errorf("legacy Word converter returned binary text; install LibreOffice (brew install --cask libreoffice) or convert this file to DOCX")
			}
		}
	}
	if err != nil {
		return "", fmt.Errorf("extract text: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if bytes.ContainsRune(b, 0) {
		return "", fmt.Errorf("converter returned binary data rather than document text")
	}
	return strings.TrimPrefix(string(b), "\ufeff"), nil
}

// splitText keeps Unicode words intact and overlaps adjacent passages.
func splitText(text string, size, overlap int) []string {
	words := strings.Fields(text)
	var chunks []string
	for start := 0; start < len(words); start += size - overlap {
		end := min(start+size, len(words))
		chunks = append(chunks, strings.Join(words[start:end], " "))
		if end == len(words) {
			break
		}
	}
	return chunks
}

// Workers return results by file index so concurrency never changes corpus order.
func readDocument(root, path string) ([]string, bool, error) {
	text, err := extractText(path)
	if errors.Is(err, errPasswordProtected) {
		fmt.Fprintf(os.Stderr, "WARNING: skipping password-protected document: %s\n", path)
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintf(os.Stderr, "WARNING: skipping document with no extractable text (scanned PDFs may need OCR): %s\n", path)
		return nil, true, nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, false, err
	}
	var chunks []string
	for page, text := range strings.Split(text, "\f") {
		for _, chunk := range splitText(text, 180, 30) {
			label := "section"
			if strings.EqualFold(filepath.Ext(path), ".pdf") {
				label = "page"
			}
			chunks = append(chunks, fmt.Sprintf("Source: %s | %s %d\n%s", filepath.ToSlash(rel), label, page+1, chunk))
		}
	}
	return chunks, false, nil
}

// All roots are validated before database/model work. Canonical roots and file
// ownership make repeated/overlapping inputs deterministic without re-reading files.
func documentDirectories(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("-docs is required for indexing")
	}
	var roots []string
	seen := map[string]bool{}
	for _, part := range strings.Split(value, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("-docs requires nonempty directories separated by ';'")
		}
		root, err := filepath.Abs(part)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("invalid -docs directory %q: %w", part, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("-docs must contain existing directories: %s", part)
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return nil, err
		}
		if !seen[root] {
			roots = append(roots, root)
			seen[root] = true
		}
	}
	sort.Strings(roots)
	return roots, nil
}
