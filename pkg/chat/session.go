package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ardanlabs/kronk/sdk/kronk"
	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/brito-rafa/jar/pkg/catalog"
	"github.com/brito-rafa/jar/pkg/duck"
)

// One local conversation, shared by browser tabs; the HTTP layer serializes
// requests so model contexts and DuckDB transactions cannot interleave.
type chatSession struct {
	messages          []model.D
	scope             duck.SourceFilter
	language          string
	referenceLanguage string
	refs              *referenceStore
	lookup            sourceLookup
	browse            catalogLookup
	expand            func([]string, int) (string, error)
	retrieve          func(duck.SourceFilter) (string, error)
	search            func(context.Context, []model.D, duck.SourceFilter, string) ([]duck.Document, error)
	translate         func(context.Context, string, string) (string, error)
	generate          func(context.Context, []model.D) (string, error)
}
type chatReply struct {
	Warnings     []string `json:"warnings,omitempty"`
	DownloadText string   `json:"downloadText,omitempty"`
	DownloadName string   `json:"downloadName,omitempty"`
	Text         string   `json:"text"`
	Quit         bool     `json:"quit"`
	Scope        string   `json:"scope"`
	Language     string   `json:"language"`
	References   []int    `json:"references"`
}

func newChatSession(db *sql.DB, embed, chat *kronk.Kronk) *chatSession {
	return &chatSession{
		refs:      &referenceStore{},
		translate: translationModel(chat),
		expand:    func(paths []string, id int) (string, error) { return duck.RetrieveReferenceContext(db, paths, id) },
		lookup:    func(f duck.SourceFilter) ([]catalog.Origin, int, error) { return duck.ListSources(db, f) },
		browse:    func(kind, query string) ([]catalog.Origin, error) { return duck.BrowseSources(db, kind, query) },
		retrieve:  func(f duck.SourceFilter) (string, error) { return duck.RetrieveConferences(db, f) },
		search: func(ctx context.Context, m []model.D, f duck.SourceFilter, lang string) ([]duck.Document, error) {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			return vectorSearch(ctx, embed, db, m, f, lang)
		},
		generate: func(ctx context.Context, m []model.D) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
			defer cancel()
			ch, err := performChat(ctx, chat, model.D{"messages": m, "max_tokens": 2048, "temperature": 0.2, "stream_options": model.D{"include_usage": true}})
			if err != nil {
				return "", err
			}
			answer, err := collectAnswer(ch)
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return answer, err
		},
	}
}
func (s *chatSession) reply(text string, quit bool) chatReply {
	lang := s.language
	if lang == "" {
		lang = "auto"
	}
	r := chatReply{Text: text, Quit: quit, Scope: s.scope.Description(), Language: lang, References: []int{}}
	for _, ref := range s.refs.snapshot() {
		r.References = append(r.References, ref.Number)
	}
	return r
}
func (s *chatSession) handle(ctx context.Context, input string) (chatReply, error) {
	return s.handleProgress(ctx, input, nil)
}
func (s *chatSession) handleProgress(ctx context.Context, input string, emit func(chatProgress)) (chatReply, error) {
	report := func(p chatProgress) {
		if emit != nil {
			emit(p)
		}
	}
	if strings.TrimSpace(input) == "quit" {
		input = "/quit"
	}
	command := processCommand(input, s.scope, s.lookup, s.language)
	if command.Handled {
		if command.Reset {
			s.messages = nil
			s.refs.set(nil)
			s.referenceLanguage = ""
		}
		if command.Scope != nil {
			s.scope = *command.Scope
		}
		if command.Language != nil {
			s.language = *command.Language
		}
		if command.Quit {
			s.messages = nil
			s.scope = duck.SourceFilter{}
			s.refs.set(nil)
			s.referenceLanguage = ""
			return s.reply("Conversation ended.", true), nil
		}
		text := command.Output
		var err error
		if command.Reference > 0 {
			saved := s.refs.snapshot()
			text = renderReference(saved, command.Reference)
			for _, ref := range saved {
				if ref.Number == command.Reference {
					if s.expand != nil && len(ref.Paths) > 0 {
						full, err := s.expand(ref.Paths, ref.ChunkID)
						if err != nil {
							text += "\n\nCould not expand the source: " + err.Error() + ". The exact cited chunk above is still available."
						} else {
							text += "\n\n--- Source context (book chapter or nearby-passage fallback; full conference) ---\n\n" + full
							return s.referenceDownload(ctx, text, ref.Number, report), nil
						}
					} else {
						text += "\n\nThis is one indexed chunk; it may end mid-sentence. Full source context is unavailable for this reference."
					}
					return s.referenceDownload(ctx, text, ref.Number, report), nil
				}
			}
		}
		if command.ListKind != "" {
			var origins []catalog.Origin
			origins, err = s.browse(command.ListKind, command.ListQuery)
			if err == nil {
				text = renderSourceList(command.ListKind, origins)
			}
		}
		if command.Retrieve != nil {
			text, err = s.retrieve(*command.Retrieve)
			if err == nil && text != "" && !strings.HasPrefix(text, "No indexed conferences match") {
				reply := s.reply(text, false)
				reply.DownloadName = "conference-" + command.Retrieve.Date + ".txt"
				return reply, nil
			}
		}
		return s.reply(text, false), err
	}
	s.refs.set(nil)
	s.referenceLanguage = ""
	// Keep history unchanged when a query fails or has no matching passages.
	messages := append(append([]model.D(nil), s.messages...), model.TextMessage("user", input))
	report(chatProgress{Stage: "searching"})
	docs, err := s.search(ctx, messages, s.scope, s.language)
	if err != nil {
		return chatReply{}, err
	}
	report(retrievalProgress(docs, answerLanguage(input, s.language)))
	if len(docs) == 0 {
		return s.reply("No matching passages were found in the current search scope.", false), nil
	}
	answer, sources, warnings, err := groundedAnswer(docs, messages, func(m []model.D) (string, error) {
		report(chatProgress{Stage: "generating"})
		answer, err := s.generate(ctx, m)
		report(chatProgress{Stage: "validating"})
		return answer, err
	}, s.language)
	if err != nil && !errors.Is(err, errCitationValidation) {
		return chatReply{}, err
	}
	var out strings.Builder
	fmt.Fprintln(&out, answer)
	printSources(&out, sources, errors.Is(err, errCitationValidation))
	s.refs.set(sources)
	s.referenceLanguage = effectiveLanguage(input, s.language)
	s.messages = append(messages, model.TextMessage("assistant", answer))
	reply := s.reply(out.String(), false)
	reply.Warnings = warnings
	return reply, nil
}
func (s *chatSession) complete(line string) []string {
	suffixes, _ := (slashCompleter{lookup: s.browse, references: s.refs.snapshot}).Do([]rune(line), len([]rune(line)))
	result := []string{}
	for _, suffix := range suffixes {
		result = append(result, line+string(suffix))
	}
	return result
}

func (s *chatSession) referenceDownload(ctx context.Context, text string, number int, report func(chatProgress)) chatReply {
	reply := s.reply(text, false)
	reply.DownloadName = fmt.Sprintf("reference-%d.txt", number)
	target := s.referenceLanguage
	if s.language != "" {
		target = s.language
	}
	if alreadyInLanguage(text, target) {
		reply.DownloadText = text + "\n\n--- Translation skipped ---\nOriginal content is already in " + answerLanguage("", target) + "; original wording retained.\n"
		reply.Text += "\n\nOriginal content is already in " + answerLanguage("", target) + "; translation skipped."
		return reply
	}
	translated, warnings, err := translateReference(ctx, text, target, s.translate, report)
	reply.Warnings = append(reply.Warnings, warnings...)
	if err != nil {
		reply.Warnings = append(reply.Warnings, "Translation was not appended: "+err.Error()+". The download contains the complete original text only.")
		reply.DownloadText = text + "\n\n--- Translation unavailable ---\n" + err.Error() + "\n"
		return reply
	}
	reply.DownloadText = text + "\n\n--- Translation: " + answerLanguage("", target) + " (model-generated; original text above) ---\n\n" + translated + "\n"
	if len(reply.Warnings) > 0 {
		reply.DownloadText += "\n--- Advisory warnings ---\n" + strings.Join(reply.Warnings, "\n") + "\n"
	}
	reply.Text += "\n\nThe download includes the original text followed by its translation into " + answerLanguage("", target) + "."
	return reply
}
