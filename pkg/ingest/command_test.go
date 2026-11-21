package ingest

import (
	"strings"
	"testing"
)

func TestIndexRequiresDocumentDirectory(t *testing.T) {
	if err := Run(nil); err == nil || !strings.Contains(err.Error(), "-docs is required") {
		t.Fatalf("expected missing-directory error, got %v", err)
	}
}

func TestResetRejectsUnsafeArgumentsBeforeConnecting(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"-reset"}, "requires -confirm=erase"},
		{[]string{"-reset", "-confirm=no"}, "requires -confirm=erase"},
		{[]string{"-reset", "-confirm=erase", "-status"}, "cannot be combined"},
		{[]string{"-reset", "-confirm=erase", "-metadata"}, "cannot be combined"},
		{[]string{"-reset", "-confirm=erase", "-docs=fixture"}, "cannot be combined"},
	} {
		if err := Run(test.args); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("Run(%v): got %v, want %s", test.args, err, test.want)
		}
	}
}

func TestIndexRequiresEmbeddingEndpointBeforeConnecting(t *testing.T) {
	t.Setenv("EMBEDDING_URL", "")
	t.Setenv("DB_DISABLE_SSL", "")
	if err := Run([]string{"-docs=" + t.TempDir()}); err == nil || !strings.Contains(err.Error(), "embedding-url is required") {
		t.Fatalf("expected missing embedding endpoint error, got %v", err)
	}
}

func TestIndexRejectsOldProviderSelectionFlags(t *testing.T) {
	t.Setenv("DB_DISABLE_SSL", "")
	for _, name := range []string{"remote-embedding", "remote-embedding-model"} {
		if err := Run([]string{"-" + name + "=example.test"}); err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
			t.Fatalf("%s: expected unsupported flag, got %v", name, err)
		}
	}
}
