package chat

import (
	"strings"
	"testing"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/brito-rafa/jar/pkg/duck"
)

const englishPassage = "Practical wisdom helps us reflect on our decisions, recognize habits, and choose actions that improve our daily lives. Keeping a journal supports this reflection."
const spanishPassage = "La sabiduría práctica nos permite reflexionar sobre nuestras decisiones, reconocer nuestros hábitos y elegir acciones que mejoren nuestra vida cotidiana. Llevar un diario facilita esta reflexión."
const portuguesePassage = "A sabedoria prática permite refletir sobre nossas decisões, reconhecer nossos hábitos e escolher ações que melhorem nossa vida cotidiana. Escrever um diário facilita essa reflexão."

func TestPrimaryQuestionLanguages(t *testing.T) {
	for _, tc := range []struct{ question, code, name string }{
		{"How can keeping a journal help us make better decisions?", "en", "English"},
		{"¿Cómo puede llevar un diario ayudarnos a tomar mejores decisiones?", "es", "Spanish"},
		{"Como escrever um diário pode nos ajudar a tomar decisões melhores?", "pt", "Portuguese"},
	} {
		if got := detectLanguage(tc.question); got != tc.code {
			t.Errorf("%q: got %s want %s", tc.question, got, tc.code)
		}
		prompt := addContextPrompt([]duck.Document{{ID: 1, Text: englishPassage}}, []model.D{model.TextMessage("user", tc.question)})[0]["content"].(string)
		if !strings.Contains(prompt, "Answer entirely in "+tc.name) || !strings.Contains(prompt, "Translate that evidence") {
			t.Fatal(prompt)
		}
	}
	if got := detectLanguage("Source: English/book.pdf | page 28\n" + spanishPassage); got != "es" {
		t.Fatal("filename biased detection:", got)
	}
}

func TestPreferSameLanguageWithoutExcludingOthers(t *testing.T) {
	docs := []duck.Document{
		{ID: 1, Text: spanishPassage, Similarity: 0.94},
		{ID: 2, Text: englishPassage, Similarity: 0.93},
		{ID: 3, Text: portuguesePassage, Similarity: 0.92},
		{ID: 4, Text: englishPassage, Similarity: 0.91},
		{ID: 5, Text: englishPassage, Similarity: 0.3},
	}
	got := prioritizeLanguages(docs, "How can keeping a journal help us make better decisions?")
	want := []int{2, 4, 1, 3}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("position %d: got %d want %d", i, got[i].ID, id)
		}
	}
	got = prioritizeLanguages(docs[:1], "How can keeping a journal help us make better decisions?")
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatal("cross-language fallback lost", got)
	}
	got = prioritizeLanguages(docs, "Como escrever um diário pode nos ajudar a tomar decisões melhores?")
	if got[0].ID != 3 {
		t.Fatal("Portuguese not prioritized", got)
	}
	got = prioritizeLanguages(docs, "¿Cómo puede llevar un diario ayudarnos a tomar mejores decisiones?")
	if got[0].ID != 1 {
		t.Fatal("Spanish not prioritized", got)
	}
}
