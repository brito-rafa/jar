package chat

import (
	"os"
	"strings"
	"testing"

	"github.com/brito-rafa/jar/pkg/catalog"
	"github.com/brito-rafa/jar/pkg/duck"
)

func TestCompletion(t *testing.T) {
	c := slashCompleter{lookup: func(kind, query string) ([]catalog.Origin, error) {
		if kind == "books" {
			return []catalog.Origin{catalog.Parse("Books/La_Herencia_de_Sí_Mismo.pdf"), catalog.Parse("Books/La_Herencia_de_Sí_Mismo.pdf")}, nil
		}
		return []catalog.Origin{catalog.Parse("Conf/580508RN.DOC")}, nil
	}}
	for input, want := range map[string]string{
		"/cl": "/clearsearch", "/he": "/help", "/li": "/list ", "/list b": "/list books ", "/list c": "/list conferences ",
		"/language p": "/language pt", "/search book La Herencia de Sí": "/search book La Herencia de Sí Mismo",
		"/retrieve 1958-05": "/retrieve 1958-05-08", "/search date 1958-": "/search date 1958-05-08",
		"/list conferences 1958": "/list conferences 1958-05-08",
	} {
		suffixes, n := c.Do([]rune(input), len([]rune(input)))
		found := false
		seen := map[string]bool{}
		for _, suffix := range suffixes {
			got := input + string(suffix)
			if seen[got] {
				t.Fatal("duplicate completion", got)
			}
			seen[got] = true
			if got == want {
				found = true
			}
		}
		if !found || n != len([]rune(input)) {
			t.Fatalf("%q missing %q: %+v", input, want, suffixes)
		}
	}
	if got, _ := c.Do([]rune("ordinary question"), 17); len(got) != 0 {
		t.Fatal(got)
	}
	if got, _ := c.Do([]rune("/help"), 100); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestListCommands(t *testing.T) {
	for input, kind := range map[string]string{"/list books": "books", "/list conferences 1958-05": "conferences", "/list book Herencia": "books"} {
		r := processCommand(input, duck.SourceFilter{Book: "old"}, nil)
		if r.ListKind != kind || r.Reset || r.Scope != nil {
			t.Fatalf("%s: %+v", input, r)
		}
	}
	if r := processCommand("/list books La Herencia", duck.SourceFilter{}, nil); r.ListQuery != "La Herencia" {
		t.Fatal(r)
	}
	if r := processCommand("/list invalid", duck.SourceFilter{}, nil); r.ListKind != "" {
		t.Fatal(r)
	}
	if got := renderSourceList("books", []catalog.Origin{catalog.Parse("book.pdf")}); !strings.Contains(got, "book.pdf") {
		t.Fatal(got)
	}
}

// Run manually in a PTY and enter /he<Tab><Enter>, /language p<Tab><Enter>.
func TestInteractiveCompletion(t *testing.T) {
	if os.Getenv("JAR_TEST_TERMINAL") != "1" {
		t.Skip("requires interactive PTY")
	}
	read, closeInput, err := newChatInput(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeInput()
	for _, want := range []string{"/help", "/language pt"} {
		got, err := read()
		if err != nil || got != want {
			t.Fatalf("got %q, want %q: %v", got, want, err)
		}
	}
}
