package chat

import (
	"bufio"
	"io"
	"strings"
	"testing"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/brito-rafa/jar/pkg/duck"
)

func TestBufferedInput(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("primera\nsegunda\nquit\n"))
	var messages []model.D
	for _, want := range []string{"primera\n", "segunda\n"} {
		var err error
		messages, err = userInput(reader, messages)
		if err != nil {
			t.Fatal(err)
		}
		if got := messages[len(messages)-1]["content"]; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if _, err := userInput(reader, messages); err != io.EOF {
		t.Fatalf("quit: %v", err)
	}
}

func TestContextPreservesQuestionAndHistory(t *testing.T) {
	messages := []model.D{model.TextMessage("user", "¿Qué es prudencia?")}
	got := addContextPrompt([]duck.Document{{ID: 7, Text: "Deliberación práctica."}}, messages)
	if len(got) != 1 {
		t.Fatalf("duplicate question: %d messages", len(got))
	}
	content := got[0]["content"].(string)
	if strings.Count(content, "Deliberación práctica.") != 1 || !strings.Contains(content, "[1]") {
		t.Fatal(content)
	}
	if messages[0]["content"] != "¿Qué es prudencia?" {
		t.Fatal("history mutated")
	}
}

func TestShortSpanishPreview(t *testing.T) {
	if got := preview("Ética\npráctica"); got != "Ética práctica" {
		t.Fatal(got)
	}
}
