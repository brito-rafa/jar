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
