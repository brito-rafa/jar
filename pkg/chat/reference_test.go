package chat

import (
	"github.com/brito-rafa/jar/pkg/duck"
	"strings"
	"testing"
)

func TestRetrieveReferenceCommand(t *testing.T) {
	r := processCommand("/retrieveref 1", duck.SourceFilter{Book: "HSM"}, nil)
	if !r.Handled || r.Reference != 1 || r.Reset || r.Scope != nil {
		t.Fatal(r)
	}
	for _, input := range []string{"/retrieveref", "/retrieveref 0", "/retrieveref -1", "/retrieveref 1 2", "/retrieveref abc", "/retrieveref 9999999999999999999999"} {
		r := processCommand(input, duck.SourceFilter{}, nil)
		if r.Reference != 0 || !strings.Contains(r.Output, "Use /retrieveref") {
			t.Fatal(input, r)
		}
	}
}

func TestExactReferenceSnapshot(t *testing.T) {
	original := "Autor mismo, y lo que el discípulo haga.\n\nSecond paragraph with [brackets]."
	sources := []source{{Number: 1, Label: "Aquarius 1931.doc | section 11", Text: original}, {Number: 4, Label: "Another.doc | section 8", Text: "Different passage"}}
	store := &referenceStore{}
	store.set(sources)
	sources[0].Text = "mutated"
	snapshot := store.snapshot()
	got := renderReference(snapshot, 1)
	if !strings.HasSuffix(got, original) || !strings.Contains(got, "Aquarius 1931.doc | section 11") || strings.Contains(got, "Different passage") {
		t.Fatal(got)
	}
	if got := renderReference(snapshot, 4); !strings.HasSuffix(got, "Different passage") {
		t.Fatal(got)
	}
	if got := renderReference(snapshot, 2); !strings.Contains(got, "not in the latest Sources list") {
		t.Fatal(got)
	}
	snapshot[0].Text = "changed"
	if store.snapshot()[0].Text != original {
		t.Fatal("snapshot mutated stored references")
	}
	c := slashCompleter{references: store.snapshot}
	suffixes, _ := c.Do([]rune("/retrieveref "), 13)
	if len(suffixes) != 2 || string(suffixes[0]) != "1" || string(suffixes[1]) != "4" {
		t.Fatal(suffixes)
	}
	store.set(nil)
	if got := renderReference(store.snapshot(), 1); !strings.Contains(got, "No references") {
		t.Fatal(got)
	}
}

func TestVisibleReferencesRenumberInFirstCitationOrder(t *testing.T) {
	sources := []source{{Number: 2, Label: "two", Text: "second", Paths: []string{"second.pdf"}}, {Number: 4, Label: "four", Text: "fourth", Paths: []string{"fourth.pdf"}}}
	answer, refs := renumberCitations("Claim [4]. Another [2, 4]. Again [2].", sources)
	if answer != "Claim [1]. Another [2, 1]. Again [2]." || len(refs) != 2 || refs[0].Number != 1 || refs[0].Label != "four" || refs[1].Number != 2 || refs[1].Label != "two" {
		t.Fatal(answer, refs)
	}
	if sources[0].Number != 2 {
		t.Fatal("mutated original references")
	}
}
