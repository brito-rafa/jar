package chat

import (
	"bytes"
	"errors"
	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/brito-rafa/jar/pkg/duck"
	"strings"
	"testing"
)

func TestCitationsOnlyAllowProvidedContext(t *testing.T) {
	docs := []duck.Document{{ID: 88, Text: "Source: book.pdf | page 28\nTexto."}, {ID: 99, Text: "Source: notes.doc | section 1\nNotas."}, {ID: 100, Text: "Not in context"}}
	sources := contextSources(docs)
	for _, bad := range []string{"No citation", "Claim [4]", "Claim [1] and [999]", "Claim [Chunk 88]", "Claim [1,99]"} {
		if _, err := validateCitations(bad, sources); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	used, err := validateCitations("Afirmación [2]. Otra [2].", sources)
	if err != nil || len(used) != 1 || used[0].Number != 2 {
		t.Fatalf("%v %v", used, err)
	}
	var out bytes.Buffer
	printSources(&out, used, false)
	if !strings.Contains(out.String(), "[2] notes.doc | section 1") || strings.Contains(out.String(), "book.pdf") {
		t.Fatal(out.String())
	}
}

func TestRetryAndFailureDoNotExposeInvalidDraft(t *testing.T) {
	docs := []duck.Document{{ID: 77, Text: "Source: libro.pdf | page 4\nTexto."}}
	messages := []model.D{model.TextMessage("user", "¿Qué dice?")}
	calls := 0
	answer, sources, err := citedAnswer(docs, messages, func(prompt []model.D) (string, error) {
		calls++
		if calls == 1 {
			return "Invalid answer [999]", nil
		}
		if !strings.Contains(prompt[0]["content"].(string), "failed citation validation") {
			t.Fatal("missing retry instruction")
		}
		return "Respuesta [1].", nil
	})
	if err != nil || calls != 2 || answer != "Respuesta [1]." || len(sources) != 1 {
		t.Fatalf("%q %v", answer, err)
	}
	calls = 0
	answer, sources, err = citedAnswer(docs, messages, func([]model.D) (string, error) { calls++; return "Uncited private draft", nil })
	if !errors.Is(err, errCitationValidation) || calls != 2 || strings.Contains(answer, "private draft") {
		t.Fatalf("%q %v", answer, err)
	}
	var out bytes.Buffer
	printSources(&out, sources, true)
	if !strings.Contains(out.String(), "libro.pdf | page 4") {
		t.Fatal(out.String())
	}
	if messages[0]["content"] != "¿Qué dice?" {
		t.Fatal("question history mutated")
	}
}

func TestSourcesMatchOnlyPromptPassages(t *testing.T) {
	docs := []duck.Document{{ID: 7, Text: "Source: original.pdf | page 2\nFirst passage"}, {ID: 8, Text: "Legacy passage"}, {ID: 9, Text: "Third passage"}, {ID: 10, Text: "Fourth passage"}, {ID: 11, Text: "Excluded passage"}}
	prompt := addContextPrompt(docs, []model.D{model.TextMessage("user", "Question")})[0]["content"].(string)
	if strings.Contains(prompt, "Excluded passage") || !strings.Contains(prompt, "original.pdf | page 2") {
		t.Fatal(prompt)
	}
	sources := contextSources(docs)
	if len(sources) != 4 || !strings.Contains(sources[1].Label, "filename unavailable") {
		t.Fatal(sources)
	}
}

func TestGroupedValidCitations(t *testing.T) {
	sources := contextSources([]duck.Document{{ID: 1, Text: "One"}, {ID: 2, Text: "Two"}})
	used, err := validateCitations("Supported by both passages [1, 2].", sources)
	if err != nil || len(used) != 2 {
		t.Fatalf("%v %v", used, err)
	}
	if _, err := validateCitations("Unknown source [1, 99].", sources); err == nil {
		t.Fatal("unknown ID accepted")
	}
}

func TestDeduplicatedPassageShowsAllSourceLocations(t *testing.T) {
	sources := contextSources([]duck.Document{{ID: 1, Text: "Shared passage", Sources: []string{"conf/a.doc | section 1", "Conferencias/a.doc | section 1"}}})
	if len(sources) != 1 || !strings.Contains(sources[0].Label, "conf/a.doc") || !strings.Contains(sources[0].Label, "Conferencias/a.doc") {
		t.Fatal(sources)
	}
}
