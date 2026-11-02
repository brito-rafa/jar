package chat

import (
	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/brito-rafa/jar/pkg/duck"
	"strings"
	"testing"
)

func TestLanguageCommands(t *testing.T) {
	for input, want := range map[string]string{"en": "en", "English": "en", "pt": "pt", "português": "pt", "español": "es", "es": "es", "auto": ""} {
		r := processCommand("/language "+input, duck.SourceFilter{}, nil)
		if r.Language == nil || *r.Language != want || !r.Reset || r.Scope != nil {
			t.Fatalf("%s: %+v", input, r)
		}
	}
	r := processCommand("/language french", duck.SourceFilter{}, nil)
	if r.Language != nil || r.Reset {
		t.Fatal("invalid input changed state")
	}
	if r := processCommand("/language", duck.SourceFilter{}, nil, "pt"); !strings.Contains(r.Output, "pt") {
		t.Fatal(r)
	}
	q := "What is responsibility?"
	if answerLanguage(q, "pt") != "Portuguese" || effectiveLanguage(q, "es") != "es" || effectiveLanguage(q, "") != "en" {
		t.Fatal("override not honored")
	}
	messages := []model.D{model.TextMessage("user", q)}
	prompt := addContextPrompt([]duck.Document{{ID: 1, Text: "Evidence"}}, messages, "es")
	if !strings.Contains(prompt[0]["content"].(string), "Answer entirely in Spanish") {
		t.Fatal(prompt)
	}
	_, _, err := citedAnswer([]duck.Document{{ID: 1, Text: "Evidence"}}, messages, func(p []model.D) (string, error) {
		if !strings.Contains(p[0]["content"].(string), "Answer entirely in Portuguese") {
			t.Fatal(p)
		}
		return "Evidence [1]", nil
	}, "pt")
	if err != nil {
		t.Fatal(err)
	}
}

func TestRetrieveCommands(t *testing.T) {
	for input, want := range map[string]string{"580508": "1958-05-08", "1958-05-08": "1958-05-08", "date 1958-05": "1958-05", "1958": "1958"} {
		r := processCommand("/retrieve "+input, duck.SourceFilter{Book: "old"}, nil)
		if r.Retrieve == nil || r.Retrieve.Date != want || r.Scope != nil || r.Reset {
			t.Fatalf("%s: %+v", input, r)
		}
	}
	for _, input := range []string{"/retrieve", "/retrieve 1958-02-30", "/retrieve nonsense"} {
		if r := processCommand(input, duck.SourceFilter{}, nil); r.Retrieve != nil {
			t.Fatal(r)
		}
	}
}
