package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMultipleDocumentDirectories(t *testing.T) {
	base := t.TempDir()
	one := filepath.Join(base, "first collection")
	two := filepath.Join(base, "second collection")
	for _, dir := range []string{one, two} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(one, "book.txt"), []byte("First document."), 0600)
	os.WriteFile(filepath.Join(two, "book.txt"), []byte("Second document."), 0600)
	prepared, err := prepareDocuments(one+" ; "+two, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(prepared)
	raw, _ := os.ReadFile(prepared)
	text := string(raw)
	if strings.Count(text, "<CHUNK>") != 2 || !strings.Contains(text, "Source: first collection/book.txt") || !strings.Contains(text, "Source: second collection/book.txt") {
		t.Fatal(text)
	}
	repeated, err := prepareDocuments(two+";"+one+";"+one, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(repeated)
	rawAgain, _ := os.ReadFile(repeated)
	if string(rawAgain) != text {
		t.Fatal("root ordering or duplicates changed prepared data")
	}
}
func TestOverlappingDirectoriesReadFileOnce(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	os.Mkdir(nested, 0755)
	os.WriteFile(filepath.Join(nested, "book.txt"), []byte("Only once."), 0600)
	prepared, err := prepareDocuments(root+";"+nested, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(prepared)
	raw, _ := os.ReadFile(prepared)
	if strings.Count(string(raw), "<CHUNK>") != 1 {
		t.Fatal(string(raw))
	}
}
func TestDirectoriesValidateEveryEntry(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	os.WriteFile(file, []byte("x"), 0600)
	for _, value := range []string{"", " ", root + ";", root + ";;" + root, root + ";" + file, root + ";" + filepath.Join(root, "missing")} {
		if _, err := documentDirectories(value); err == nil {
			t.Fatal("accepted", value)
		}
	}
}
func TestSameBasenameRootsHaveDistinctLabels(t *testing.T) {
	base := t.TempDir()
	var dirs []string
	for _, parent := range []string{"a", "b"} {
		dir := filepath.Join(base, parent, "books")
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "same.txt"), []byte(parent), 0600)
		dirs = append(dirs, dir)
	}
	prepared, err := prepareDocuments(strings.Join(dirs, ";"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(prepared)
	raw, _ := os.ReadFile(prepared)
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "Source: ") {
			if seen[line] {
				t.Fatal("colliding source label", line)
			}
			seen[line] = true
		}
	}
	if len(seen) != 2 {
		t.Fatal(string(raw))
	}
}
