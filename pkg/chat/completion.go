package chat

import (
	"bufio"
	"database/sql"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/brito-rafa/jar/pkg/catalog"
	"github.com/brito-rafa/jar/pkg/duck"
	"github.com/chzyer/readline"
)

type catalogLookup func(kind, query string) ([]catalog.Origin, error)
type slashCompleter struct {
	lookup     catalogLookup
	references func() []source
}

func (c slashCompleter) Do(line []rune, pos int) ([][]rune, int) {
	if pos < 0 || pos > len(line) {
		return nil, 0
	}
	prefix := string(line[:pos])
	if !strings.HasPrefix(prefix, "/") {
		return nil, 0
	}
	candidates := []string{"/help", "/quit", "/reset", "/clearsearch", "/language ", "/retrieve ", "/retrieveref ", "/search ", "/list "}
	add := func(base string, values ...string) {
		for _, v := range values {
			candidates = append(candidates, base+v)
		}
	}
	if c.references != nil {
		for _, s := range c.references() {
			add("/retrieveref ", strconv.Itoa(s.Number))
		}
	}
	add("/language ", "en", "pt", "es", "auto")
	add("/search ", "book ", "file ", "title ", "date ", "conference ", "clear", "all")
	add("/list ", "books ", "conferences ")
	for _, base := range []string{"/search book ", "/search file ", "/search title ", "/list books ", "/list conferences ", "/search date ", "/search conference ", "/retrieve ", "/retrieve date "} {
		if !strings.HasPrefix(strings.ToLower(prefix), base) || c.lookup == nil {
			continue
		}
		kind := "conferences"
		if strings.Contains(base, "book ") || strings.Contains(base, "file ") || strings.Contains(base, "title ") || base == "/list books " {
			kind = "books"
		}
		origins, err := c.lookup(kind, "")
		if err != nil {
			return nil, 0
		} // /list reports errors; Tab must not corrupt the editing line.
		for _, o := range origins {
			if kind == "books" || base == "/list conferences " {
				add(base, o.Title, o.Path)
				if base == "/list conferences " && o.Date != "" {
					add(base, o.Date)
				}
			} else if o.Date != "" {
				add(base, o.Date)
			}
		}
	}
	sort.Strings(candidates)
	var matches [][]rune
	seen := map[string]bool{}
	for _, candidate := range candidates {
		r := []rune(candidate)
		if len(r) <= pos || !strings.EqualFold(string(r[:pos]), prefix) || seen[candidate] {
			continue
		}
		seen[candidate] = true
		matches = append(matches, r[pos:])
	}
	return matches, pos
}

// Piped input keeps the existing buffered reader. Interactive terminals get
// tab completion and editing without persisting questions to a history file.
func newChatInput(db *sql.DB, references ...func() []source) (func() (string, error), func(), error) {
	if !readline.DefaultIsTerminal() {
		reader := bufio.NewReader(os.Stdin)
		return func() (string, error) {
			fmt.Print("\nUSER> ")
			text, err := reader.ReadString('\n')
			if err == io.EOF && len(text) > 0 {
				err = nil
			}
			return text, err
		}, func() {}, nil
	}
	var refs func() []source
	if len(references) > 0 {
		refs = references[0]
	}
	editor, err := readline.NewEx(&readline.Config{
		Prompt: "USER> ", AutoComplete: slashCompleter{references: refs, lookup: func(kind, query string) ([]catalog.Origin, error) { return duck.BrowseSources(db, kind, query) }},
		HistoryLimit: -1,
	})
	if err != nil {
		return nil, nil, err
	}
	return func() (string, error) {
		for {
			line, err := editor.Readline()
			if err == readline.ErrInterrupt {
				continue
			}
			return line, err
		}
	}, func() { _ = editor.Close() }, nil
}

func renderSourceList(kind string, origins []catalog.Origin) string {
	if len(origins) == 0 {
		return "No indexed " + kind + " match."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Indexed %s: %d source files (filename-derived metadata).\n", kind, len(origins))
	for _, o := range origins {
		fmt.Fprintf(&b, "- %s\n", o.Description())
	}
	if kind == "books" {
		b.WriteString("Books includes all non-conference documents; titles are inferred from filenames.\n")
	}
	return b.String()
}
