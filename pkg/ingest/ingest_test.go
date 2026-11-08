package ingest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecursiveDocuments(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "filosofia", "nested")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "ethics.TXT"), []byte("La prudencia orienta nuestras decisiones."), 0600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "ignored.csv"), []byte("ignored"), 0600)
	os.Mkdir(filepath.Join(root, ".hidden"), 0755)
	os.WriteFile(filepath.Join(root, ".hidden", "secret.txt"), []byte("hidden"), 0600)
	os.Symlink(filepath.Join(nested, "ethics.TXT"), filepath.Join(root, "link.txt"))
	chunks, err := prepareDocuments(root)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(chunks)
	b, err := os.ReadFile(chunks)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "Source: filosofia/nested/ethics.TXT") || !strings.Contains(text, "prudencia") || strings.Count(text, "<CHUNK>") != 1 {
		t.Fatal(text)
	}
}

func TestWordExtraction(t *testing.T) {
	if _, err := exec.LookPath("textutil"); err != nil {
		t.Skip("macOS textutil unavailable")
	}
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	os.WriteFile(input, []byte("La ética práctica requiere reflexión."), 0600)
	for _, format := range []string{"doc", "docx"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(root, "philosophy."+format)
			if b, err := exec.Command("textutil", "-convert", format, "-output", path, "--", input).CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, b)
			}
			got, err := extractText(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, "ética práctica") {
				t.Fatal(got)
			}
		})
	}
}

func TestPDFExtraction(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("Poppler unavailable")
	}
	path := filepath.Join(t.TempDir(), "sample.pdf")
	// Small real PDF with a valid xref table, used only as a parser fixture.
	stream := "BT /F1 12 Tf 40 100 Td (Practical wisdom guides action.) Tj ET"
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 6\n0000000000 65535 f \n")
	for _, off := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := extractText(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Practical wisdom guides action.") {
		t.Fatal(got)
	}
}

func TestSplitTextOverlap(t *testing.T) {
	got := splitText("uno dos tres cuatro cinco seis siete", 4, 1)
	if len(got) != 2 || got[0] != "uno dos tres cuatro" || got[1] != "cuatro cinco seis siete" {
		t.Fatal(got)
	}
}

func TestEmptyDocuments(t *testing.T) {
	if _, err := prepareDocuments(t.TempDir()); err == nil {
		t.Fatal("empty directory accepted")
	}
}

func TestLegacyWordFallbackAndEmptySkip(t *testing.T) {
	tools := t.TempDir()
	for name, script := range map[string]string{
		"textutil": "#!/bin/sh\nprintf '\\n'\n",
		"soffice":  "#!/bin/sh\nname=${7##*/}; printf \"\\n\" > \"$6/${name%.*}.txt\"\n",
		"wvWare":   "#!/bin/sh\ncase \"$6\" in *empty.doc) printf '\\n';; *) printf 'Legacy Word text';; esac\n",
	} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools)
	root := t.TempDir()
	for _, name := range []string{"legacy.doc", "empty.doc"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := extractText(filepath.Join(root, "legacy.doc"))
	if err != nil || got != "Legacy Word text" {
		t.Fatalf("fallback: %q, %v", got, err)
	}
	chunks, err := prepareDocuments(root)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(chunks)
	b, err := os.ReadFile(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Source: empty.doc") || !strings.Contains(string(b), "Legacy Word text") {
		t.Fatal(string(b))
	}
}

func TestPasswordPDFSkippedButOtherErrorsFail(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		wantError     bool
	}{
		{"password", "Command Line Error: Incorrect password", false},
		{"corrupt", "Syntax Error: Invalid PDF", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools := t.TempDir()
			script := "#!/bin/sh\nprintf '%s\\n' '" + tc.message + "' >&2\nexit 1\n"
			if err := os.WriteFile(filepath.Join(tools, "pdftotext"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", tools)
			root := t.TempDir()
			os.WriteFile(filepath.Join(root, "locked.pdf"), []byte("fixture"), 0600)
			os.WriteFile(filepath.Join(root, "valid.txt"), []byte("Texto accesible."), 0600)
			chunks, err := prepareDocuments(root)
			if tc.wantError {
				if err == nil {
					os.Remove(chunks)
					t.Fatal("non-password error was ignored")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(chunks)
			b, err := os.ReadFile(chunks)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "locked.pdf") || !strings.Contains(string(b), "Texto accesible.") {
				t.Fatal(string(b))
			}
		})
	}
}

func TestEmptyPDFSkipped(t *testing.T) {
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "pdftotext"), []byte("#!/bin/sh\nprintf '\\n\\f\\n'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "scanned.pdf"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "valid.txt"), []byte("Texto accesible."), 0600); err != nil {
		t.Fatal(err)
	}
	chunks, err := prepareDocuments(root)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(chunks)
	b, err := os.ReadFile(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "scanned.pdf") || !strings.Contains(string(b), "Texto accesible.") {
		t.Fatal(string(b))
	}
}

func TestDocumentWorkersKeepDeterministicCorpus(t *testing.T) {
	root := t.TempDir()
	for i := range 40 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%03d.txt", i)), []byte(fmt.Sprintf("Texto de exemplo %d.", i)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	serial, err := prepareDocuments(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(serial)
	parallel, err := prepareDocuments(root, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(parallel)
	a, _ := os.ReadFile(serial)
	b, _ := os.ReadFile(parallel)
	if string(a) != string(b) {
		t.Fatal("worker count changed corpus ordering")
	}
}

func TestWordExtractionWithoutTextutil(t *testing.T) {
	for _, ext := range []string{"DOC", "docx"} {
		t.Run(ext, func(t *testing.T) {
			tools := t.TempDir()
			// Check the absolute source path, private profile, and output directory.
			script := `#!/bin/sh
case "$1" in -env:UserInstallation=file://*) ;; *) exit 2;; esac
[ "$2" = "--headless" ] || exit 3
[ "$7" = "$SOURCE" ] || exit 4
[ "$PWD" = "$6" ] || exit 5
name=${7##*/}
printf '\357\273\277Practical wisdom.' > "$6/${name%.*}.txt"
`
			if err := os.WriteFile(filepath.Join(tools, "soffice"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", tools)
			path := filepath.Join(t.TempDir(), "sample with spaces."+ext)
			if err := os.WriteFile(path, []byte("synthetic document"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SOURCE", path)
			got, err := extractText(path)
			if err != nil || got != "Practical wisdom." {
				t.Fatalf("got %q, %v", got, err)
			}
			b, err := os.ReadFile(path)
			if err != nil || string(b) != "synthetic document" {
				t.Fatalf("source changed: %q, %v", b, err)
			}
		})
	}
}

func TestDOCExtractionWithOnlyWvWare(t *testing.T) {
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "wvWare"), []byte("#!/bin/sh\nprintf 'Legacy Word text'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools)
	got, err := extractText(filepath.Join(t.TempDir(), "sample.DOC"))
	if err != nil || got != "Legacy Word text" {
		t.Fatalf("got %q, %v", got, err)
	}
}
