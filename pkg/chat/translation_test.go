package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestTranslationPartsPreserveEveryByte(t *testing.T) {
	original := strings.Repeat("observación\tdecisões\n", 550)
	parts := translationParts(original, 400)
	if strings.Join(parts, "") != original || len(parts) != 3 {
		t.Fatal("split lost text", len(parts))
	}
	for _, p := range parts {
		if len(strings.Fields(p)) > 400 {
			t.Fatal("oversized part")
		}
	}
}
func TestReferenceDownloadAppendsQueryLanguage(t *testing.T) {
	for _, tc := range []struct{ query, override, want string }{
		{"o que o autor fala sobre o tema?", "", "pt"},
		{"What helps reflection?", "", "en"},
		{"¿Qué dice sobre las decisiones?", "", "es"},
		{"What helps reflection?", "pt", "pt"},
	} {
		s := fixtureSession()
		s.language = tc.override
		if _, err := s.handle(context.Background(), tc.query); err != nil {
			t.Fatal(err)
		}
		s.expand = func([]string, int) (string, error) {
			return "Continued text from the next indexed chunk.\n" + strings.Repeat(englishPassage+"\n", 4), nil
		}
		originalHistory := len(s.messages)
		calls := 0
		s.translate = func(ctx context.Context, text, lang string) (string, error) {
			calls++
			if lang != tc.want {
				t.Fatalf("query %q: got %s, want %s", tc.query, lang, tc.want)
			}
			return "tradução completa", nil
		}
		r, err := s.handle(context.Background(), "/retrieveref 1")
		if tc.want == "en" {
			if err != nil || calls != 0 || !strings.Contains(r.DownloadText, "Translation skipped") || strings.Contains(r.DownloadText, "--- Translation:") {
				t.Fatal(r, err, calls)
			}
			continue
		}
		if err != nil || calls == 0 || !strings.Contains(r.DownloadText, "exact indexed passage") || !strings.Contains(r.DownloadText, "Continued text from the next indexed chunk") || !strings.Contains(r.DownloadText, "--- Translation:") || !strings.HasSuffix(r.DownloadText, "tradução completa\n") || len(s.messages) != originalHistory {
			t.Fatal(r, err)
		}
	}
}
func TestFailedTranslationKeepsOriginalWithoutPartialText(t *testing.T) {
	s := fixtureSession()
	s.referenceLanguage = "pt"
	s.translate = func(context.Context, string, string) (string, error) { return "partial", fmt.Errorf("token limit") }
	r := s.referenceDownload(context.Background(), "exact original", 1, func(chatProgress) {})
	if !strings.HasPrefix(r.DownloadText, "exact original") || strings.Contains(r.DownloadText, "partial") || len(r.Warnings) == 0 || !strings.Contains(r.DownloadText, "Translation unavailable") {
		t.Fatal(r)
	}
}
func TestLongTranslationReportsEveryPart(t *testing.T) {
	original := strings.Repeat("palavra ", 850)
	calls := 0
	events := 0
	translated, _, err := translateReference(context.Background(), original, "es", func(context.Context, string, string) (string, error) { calls++; return "traducción", nil }, func(p chatProgress) {
		events++
		if p.Stage != "translating" || p.Part != events || p.Parts != 3 {
			t.Fatal(p)
		}
	})
	if err != nil || calls != 3 || events != 3 || strings.Count(translated, "traducción") != 3 {
		t.Fatal(translated, err, calls)
	}
}

func TestTranslationNameWarningsAreAdvisory(t *testing.T) {
	translated, warnings, err := translateReference(context.Background(), "Texto original.", "pt", func(context.Context, string, string) (string, error) {
		return "Pessoa Inventada comentou o texto.", nil
	}, func(chatProgress) {})
	if err != nil || !strings.Contains(translated, "Pessoa Inventada") || len(warnings) != 1 {
		t.Fatal(translated, warnings, err)
	}
}

func TestReferenceLanguageDetectionIgnoresLabelsAndMixedText(t *testing.T) {
	for code, body := range map[string]string{"en": englishPassage, "pt": portuguesePassage, "es": spanishPassage} {
		original := "Reference [1] — exact indexed passage\nSource: English/book.pdf | page 2\n\n" + strings.Repeat(body+"\n", 4)
		if !alreadyInLanguage(original, code) {
			t.Fatal("same language not recognized", code)
		}
		for _, other := range []string{"en", "pt", "es"} {
			if other != code && alreadyInLanguage(original, other) {
				t.Fatal("wrong language", code, other)
			}
		}
	}
	if alreadyInLanguage(strings.Repeat(englishPassage, 3)+"\n"+strings.Repeat(spanishPassage, 3), "en") {
		t.Fatal("mixed content treated as English-only")
	}
}
func TestSameLanguageDownloadDoesNotInvokeModel(t *testing.T) {
	s := fixtureSession()
	s.referenceLanguage = "pt"
	s.translate = func(context.Context, string, string) (string, error) {
		t.Fatal("unnecessary model call")
		return "", nil
	}
	original := strings.Repeat(portuguesePassage+"\n", 3)
	r := s.referenceDownload(context.Background(), original, 1, func(chatProgress) {})
	if !strings.HasPrefix(r.DownloadText, original) || !strings.Contains(r.DownloadText, "Translation skipped") || strings.Contains(r.DownloadText, "--- Translation:") {
		t.Fatal(r)
	}
}
