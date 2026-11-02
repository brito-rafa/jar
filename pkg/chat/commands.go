package chat

import (
	"fmt"
	"github.com/brito-rafa/jar/pkg/catalog"
	"github.com/brito-rafa/jar/pkg/duck"
	"strconv"
	"strings"
)

const slashHelp = `Chat commands:
  /help                         Show this help (Tab completes commands/arguments)
  /list books [filter]          List indexed books/documents
  /list conferences [filter]    List indexed conferences by name or date
  /quit                         Exit (plain quit also works)
  /reset                        Clear conversation and search filter
  /clearsearch                  Clear only the search filter; keep conversation
  /language [en|pt|es|auto]     Show/force answer language; auto restores detection
  /retrieveref <number>         Show cited chunk and scoped source context
  /retrieve <date>              Print all indexed conference text for a date
  /search                       Show the current search filter
  /search book <title or file>   Filter by a book title/filename fragment
  /search date YYYY-MM-DD        Filter by filename-derived date
  /search date YYYY-MM           Filter by month (YYYY selects a year)
  /search date 580508            Legacy YYMMDD date, interpreted as 1958-05-08
  /search clear                 Search all sources and clear conversation
Changing a search filter clears old conversation context.
Book titles and dates come from filenames, not verified bibliographic metadata.`

type commandResult struct {
	Handled, Quit, Reset bool
	Scope                *duck.SourceFilter
	Retrieve             *duck.SourceFilter
	Language             *string
	ListKind, ListQuery  string
	Reference            int
	Output               string
}
type sourceLookup func(duck.SourceFilter) ([]catalog.Origin, int, error)

func processCommand(line string, scope duck.SourceFilter, lookup sourceLookup, language ...string) commandResult {
	line = strings.TrimSpace(line)
	if line == "" {
		return commandResult{Handled: true}
	}
	if !strings.HasPrefix(line, "/") {
		return commandResult{}
	}
	name := strings.Fields(line)[0]
	args := strings.TrimSpace(strings.TrimPrefix(line, name))
	r := commandResult{Handled: true}
	switch strings.ToLower(name) {
	case "/help":
		r.Output = slashHelp
	case "/quit":
		r.Quit = true
	case "/reset":
		r.Reset = true
		empty := duck.SourceFilter{}
		r.Scope = &empty
		r.Output = "Conversation context and search filter cleared."
	case "/clearsearch":
		empty := duck.SourceFilter{}
		r.Scope = &empty
		r.Output = "Search filter cleared. Conversation context retained."
	case "/list":
		fields := strings.Fields(args)
		if len(fields) == 0 {
			r.Output = "Use /list books [name] or /list conferences [name or date]."
			return r
		}
		kind := strings.ToLower(fields[0])
		switch kind {
		case "book":
			kind = "books"
		case "conference":
			kind = "conferences"
		}
		if kind != "books" && kind != "conferences" {
			r.Output = "Use /list books or /list conferences."
			return r
		}
		r.ListKind = kind
		r.ListQuery = strings.Trim(strings.TrimSpace(strings.TrimPrefix(args, fields[0])), "\"")
	case "/language":
		current := "auto"
		if len(language) > 0 && language[0] != "" {
			current = language[0]
		}
		if args == "" {
			r.Output = "Language: " + current
			return r
		}
		code, ok := parseLanguage(args)
		if !ok {
			r.Output = "Use /language en, pt, es, or auto."
			return r
		}
		r.Language = &code
		r.Reset = true
		display := code
		if display == "" {
			display = "auto"
		}
		r.Output = "Language: " + display + ". Conversation context reset."
	case "/retrieveref":
		n, err := strconv.Atoi(args)
		if err != nil || n < 1 {
			r.Output = "Use /retrieveref <positive reference number>, for example /retrieveref 1."
			return r
		}
		r.Reference = n
	case "/retrieve":
		args = strings.TrimSpace(strings.TrimPrefix(args, "date "))
		if args == "" {
			r.Output = "Use /retrieve YYYY-MM-DD (also YYYY-MM, YYYY, or YYMMDD)."
			return r
		}
		filter, err := parseSearch("date " + args)
		if err != nil {
			r.Output = "Retrieve error: " + err.Error()
			return r
		}
		r.Retrieve = &filter
	case "/search":
		if args == "" {
			r.Output = "Search scope: " + scope.Description() + "\nUse /search book <name>, /search date <date>, or /search clear."
			return r
		}
		if strings.EqualFold(args, "clear") || strings.EqualFold(args, "all") {
			empty := duck.SourceFilter{}
			r.Scope = &empty
			r.Reset = true
			r.Output = "Search scope cleared; conversation context reset."
			return r
		}
		filter, err := parseSearch(args)
		if err != nil {
			r.Output = "Search error: " + err.Error()
			return r
		}
		origins, count, err := lookup(filter)
		if err != nil {
			r.Output = "Search error: " + err.Error()
			return r
		}
		if count == 0 {
			r.Output = "No matching indexed passages. Scope unchanged: " + scope.Description()
			return r
		}
		r.Scope = &filter
		r.Reset = true
		var b strings.Builder
		fmt.Fprintf(&b, "Search scope: %s (%d unique chunks). Conversation context reset.\nMatching source locations (up to 20):\n", filter.Description(), count)
		for _, o := range origins {
			fmt.Fprintf(&b, "- %s\n", o.Description())
		}
		r.Output = b.String()
	default:
		r.Output = "Unknown command. Use /help."
	}
	return r
}
func parseSearch(args string) (duck.SourceFilter, error) {
	parts := strings.Fields(args)
	kind := strings.ToLower(parts[0])
	value := strings.TrimSpace(strings.TrimPrefix(args, parts[0]))
	switch kind {
	case "book", "file", "title":
	case "date", "conference":
		kind = "date"
	default:
		kind = "book"
		value = args
		if catalog.ValidateDate(args) == nil {
			kind = "date"
		}
	}
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") && len(value) >= 2 {
		value = value[1 : len(value)-1]
	}
	if value == "" {
		return duck.SourceFilter{}, fmt.Errorf("use /search book <name> or /search date YYYY-MM-DD")
	}
	filter := duck.SourceFilter{Book: value}
	if kind == "date" {
		if len(value) == 6 {
			p := catalog.Parse(value + "CF.DOC")
			if p.Date != "" {
				value = p.Date
			}
		}
		filter = duck.SourceFilter{Date: value}
	}
	return filter, filter.Validate()
}

func parseLanguage(value string) (string, bool) {
	switch catalog.Normalize(value) {
	case "auto", "detect":
		return "", true
	case "en", "english", "ingles":
		return "en", true
	case "pt", "portuguese", "portugues":
		return "pt", true
	case "es", "spanish", "espanol":
		return "es", true
	default:
		return "", false
	}
}
