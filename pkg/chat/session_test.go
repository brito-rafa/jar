package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/brito-rafa/jar/pkg/catalog"
	"github.com/brito-rafa/jar/pkg/duck"
)

func fixtureSession() *chatSession {
	return &chatSession{
		translate: func(ctx context.Context, text, lang string) (string, error) {
			return "Translated original text in " + lang, nil
		},
		refs: &referenceStore{},
		expand: func(paths []string, _ int) (string, error) {
			return "Source: " + strings.Join(paths, "; ") + "\nBeginning of document. A journal helps us reflect on decisions. Continued text from the next indexed chunk.", nil
		},
		lookup: func(f duck.SourceFilter) ([]catalog.Origin, int, error) {
			return []catalog.Origin{catalog.Parse("Books/Reflection.pdf | page 1")}, 1, nil
		},
		browse: func(kind, q string) ([]catalog.Origin, error) {
			if kind == "conferences" {
				return []catalog.Origin{catalog.Parse("Conf/580508RN.DOC | section 1")}, nil
			}
			return []catalog.Origin{catalog.Parse("Books/Reflection.pdf | page 1")}, nil
		},
		retrieve: func(f duck.SourceFilter) (string, error) {
			return "Entire indexed conference: " + f.Date + "\nSource: Conf/580508RN.DOC\n\n" + strings.Repeat("La observación consciente ayuda a comprender nuestras decisiones y aprender de la experiencia.\n\n", 40), nil
		},
		search: func(ctx context.Context, m []model.D, f duck.SourceFilter, lang string) ([]duck.Document, error) {
			return []duck.Document{{ID: 99, Text: "A journal helps us reflect on decisions.", Sources: []string{"Books/Reflection.pdf | page 1"}}}, nil
		},
		generate: func(ctx context.Context, m []model.D) (string, error) {
			return "A journal helps us reflect on decisions [1].", nil
		},
	}
}
func TestSharedChatSession(t *testing.T) {
	s := fixtureSession()
	ctx := context.Background()
	for _, input := range []string{"/help", "/list books", "/list conferences", "/retrieve 580508", "/language pt", "/search book Reflection"} {
		r, err := s.handle(ctx, input)
		if err != nil || r.Text == "" {
			t.Fatal(input, r, err)
		}
	}
	s.generate = func(ctx context.Context, m []model.D) (string, error) {
		if !strings.Contains(m[len(m)-1]["content"].(string), "Answer entirely in Portuguese") {
			return "", fmt.Errorf("language override lost")
		}
		return "Um diário ajuda a refletir sobre decisões [1].", nil
	}
	r, err := s.handle(ctx, "What does a journal help with?")
	if err != nil || len(r.References) != 1 || r.Language != "pt" || !strings.Contains(r.Text, "Sources:") {
		t.Fatal(r, err)
	}
	before := len(s.messages)
	r, err = s.handle(ctx, "/retrieveref 1")
	if err != nil || !strings.Contains(r.Text, "A journal helps us reflect on decisions.") || len(s.messages) != before {
		t.Fatal(r, err)
	}
	r, err = s.handle(ctx, "/clearsearch")
	if err != nil || r.Scope != "all sources" || len(s.refs.snapshot()) != 1 || len(s.messages) != before {
		t.Fatal(r, err)
	}
	r, err = s.handle(ctx, "/reset")
	if err != nil || len(s.messages) != 0 || len(r.References) != 0 || r.Language != "pt" {
		t.Fatal(r, err)
	}
	if got := s.complete("/retrieveref "); len(got) != 0 {
		t.Fatal(got)
	}
	r, err = s.handle(ctx, "/quit")
	if err != nil || !r.Quit {
		t.Fatal(r, err)
	}
}
func TestChatFailurePreservesHistory(t *testing.T) {
	s := fixtureSession()
	ctx := context.Background()
	if _, err := s.handle(ctx, "question"); err != nil {
		t.Fatal(err)
	}
	before := len(s.messages)
	s.search = func(context.Context, []model.D, duck.SourceFilter, string) ([]duck.Document, error) {
		return nil, fmt.Errorf("test failure")
	}
	if _, err := s.handle(ctx, "failed question"); err == nil || len(s.messages) != before {
		t.Fatal(err, s.messages)
	}
	s.search = func(context.Context, []model.D, duck.SourceFilter, string) ([]duck.Document, error) { return nil, nil }
	if _, err := s.handle(ctx, "missing question"); err != nil || len(s.messages) != before {
		t.Fatal(err, s.messages)
	}
}

func TestRetrieveDownloadOnlyForMatches(t *testing.T) {
	s := fixtureSession()
	r, err := s.handle(context.Background(), "/retrieve 580508")
	if err != nil || r.DownloadName != "conference-1958-05-08.txt" {
		t.Fatal(r, err)
	}
	s.retrieve = func(duck.SourceFilter) (string, error) {
		return "No indexed conferences match filename date 1958-05-08.", nil
	}
	r, err = s.handle(context.Background(), "/retrieve 580508")
	if err != nil || r.DownloadName != "" {
		t.Fatal(r, err)
	}
}

func TestReferenceExpandsExactSavedFiles(t *testing.T) {
	s := fixtureSession()
	ctx := context.Background()
	if _, err := s.handle(ctx, "question"); err != nil {
		t.Fatal(err)
	}
	s.expand = func(paths []string, _ int) (string, error) {
		if len(paths) != 1 || paths[0] != "Books/Reflection.pdf" {
			t.Fatal(paths)
		}
		return "Full beginning. A journal helps us reflect on decisions. Continued after the cited chunk.", nil
	}
	r, err := s.handle(ctx, "/retrieveref 1")
	if err != nil || r.DownloadName != "reference-1.txt" || !strings.Contains(r.Text, "Continued after the cited chunk.") || !strings.Contains(r.Text, "exact indexed passage") {
		t.Fatal(r, err)
	}
}
