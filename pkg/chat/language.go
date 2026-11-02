package chat

import (
	"strings"
	"unicode"

	"github.com/abadojack/whatlanggo"
	"github.com/brito-rafa/jar/pkg/duck"
)

const contextLimit = 4

// Detect passage text rather than filenames or directory language labels.
func detectLanguage(text string) string {
	if strings.HasPrefix(text, "Source: ") {
		_, text, _ = strings.Cut(text, "\n")
	}
	letters := 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < 3 {
		return "und"
	}
	code := whatlanggo.DetectWithOptions(text, whatlanggo.Options{Whitelist: map[whatlanggo.Lang]bool{whatlanggo.Eng: true, whatlanggo.Por: true, whatlanggo.Spa: true}}).Lang.Iso6391()
	if code == "" {
		return "und"
	}
	return code
}

func answerLanguage(question string, override ...string) string {
	switch effectiveLanguage(question, override...) {
	case "en":
		return "English"
	case "pt":
		return "Portuguese"
	case "es":
		return "Spanish"
	default:
		return "the language used in the question"
	}
}

// Prefer up to two relevant same-language passages while reserving room for
// evidence in other languages. Input is ordered by semantic similarity.
func prioritizeLanguages(docs []duck.Document, question string, override ...string) []duck.Document {
	if len(docs) == 0 {
		return nil
	}
	lang := effectiveLanguage(question, override...)
	if lang != "en" && lang != "pt" && lang != "es" {
		return docs[:min(contextLimit, len(docs))]
	}
	var same, other []duck.Document
	for _, doc := range docs {
		// Do not promote a weak match simply because it uses the same language.
		if docs[0].Similarity-doc.Similarity > 0.10 {
			continue
		}
		if detectLanguage(doc.Text) == lang {
			same = append(same, doc)
		} else {
			other = append(other, doc)
		}
	}
	chosen := append([]duck.Document(nil), same[:min(2, len(same))]...)
	chosen = append(chosen, other[:min(2, len(other))]...)
	used := map[int]bool{}
	for _, doc := range chosen {
		used[doc.ID] = true
	}
	for _, doc := range docs {
		if len(chosen) == contextLimit {
			break
		}
		if !used[doc.ID] && docs[0].Similarity-doc.Similarity <= 0.10 {
			chosen = append(chosen, doc)
			used[doc.ID] = true
		}
	}
	return chosen
}

// Distinctive function words help with short questions that confuse trigram detection.
func questionLanguage(question string) string {
	words := strings.Fields(strings.ToLower(question))
	pt, es, en := 0, 0, 0
	for _, word := range words {
		word = strings.Trim(word, ".,!?¿:;\"()")
		switch word {
		case "passagens", "fala", "você", "vocês", "não", "ações", "decisões":
			pt++
		case "pasajes", "habla", "usted", "ustedes", "también", "decisiones":
			es++
		case "what", "how", "does", "which", "where", "please":
			en++
		}
	}
	if pt > es && pt > en {
		return "pt"
	}
	if es > pt && es > en {
		return "es"
	}
	if en > pt && en > es {
		return "en"
	}
	return detectLanguage(question)
}

func effectiveLanguage(question string, override ...string) string {
	if len(override) > 0 && (override[0] == "en" || override[0] == "pt" || override[0] == "es") {
		return override[0]
	}
	return questionLanguage(question)
}
