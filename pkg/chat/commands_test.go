package chat

import (
	"github.com/brito-rafa/jar/pkg/catalog"
	"github.com/brito-rafa/jar/pkg/duck"
	"strings"
	"testing"
)

func TestSlashCommands(t *testing.T) {
	calls := 0
	lookup := func(f duck.SourceFilter) ([]catalog.Origin, int, error) {
		calls++
		return []catalog.Origin{catalog.Parse("Conferencias/580508RN.DOC | section 1")}, 2, nil
	}
	scope := duck.SourceFilter{Book: "old"}
	for _, cmd := range []string{"/help", "/reset", "/clearsearch", "/quit", "/search", "/bad", ""} {
		r := processCommand(cmd, scope, lookup)
		if !r.Handled {
			t.Fatal(cmd)
		}
		if cmd == "/reset" && (!r.Reset || r.Scope == nil || r.Scope.Active() || r.Language != nil) {
			t.Fatal("reset must clear history and scope while preserving language")
		}
		if cmd == "/clearsearch" && (r.Reset || r.Scope == nil || r.Scope.Active() || r.Language != nil) {
			t.Fatal("clearsearch must clear scope only")
		}
		if cmd == "/quit" && !r.Quit {
			t.Fatal("quit did not exit")
		}
	}
	if calls != 0 {
		t.Fatal("non-search command queried the database")
	}
	r := processCommand(`/search book "La Herencia de Sí Mismo"`, scope, lookup)
	if !r.Reset || r.Scope == nil || r.Scope.Book != "La Herencia de Sí Mismo" {
		t.Fatalf("%+v", r)
	}
	r = processCommand("/search date 580508", scope, lookup)
	if r.Scope == nil || r.Scope.Date != "1958-05-08" || !strings.Contains(r.Output, "inferred") {
		t.Fatalf("%+v", r)
	}
	r = processCommand("/search clear", scope, lookup)
	if !r.Reset || r.Scope == nil || r.Scope.Active() {
		t.Fatal(r)
	}
	r = processCommand("ordinary question", scope, lookup)
	if r.Handled {
		t.Fatal("question treated as command")
	}
	r = processCommand("/search date 1958-02-30", scope, lookup)
	if r.Scope != nil {
		t.Fatal("invalid date changed scope")
	}
	r = processCommand("/search book missing", scope, func(duck.SourceFilter) ([]catalog.Origin, int, error) { return nil, 0, nil })
	if r.Scope != nil || r.Reset {
		t.Fatal("missing match changed conversation")
	}
}
func TestReportedPortugueseQuery(t *testing.T) {
	q := "procura passagens que o autor fala sobre culpa"
	if got := questionLanguage(q); got != "pt" {
		t.Fatal(got)
	}
	if answerLanguage(q) != "Portuguese" {
		t.Fatal("wrong answer language")
	}
}
