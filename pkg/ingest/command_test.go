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
