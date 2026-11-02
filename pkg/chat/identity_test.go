package chat

import (
	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/brito-rafa/jar/pkg/duck"
	"strings"
	"testing"
)

func TestNameWarningsDoNotBlockAnswer(t *testing.T) {
	docs := []duck.Document{{ID: 1, Text: "O Autor fala sobre o tema."}}
	history := []model.D{model.TextMessage("user", "O que o autor fala?")}
	calls := 0
	answer, sources, warnings, err := groundedAnswer(docs, history, func(prompt []model.D) (string, error) {
		calls++
		if !strings.Contains(prompt[len(prompt)-1]["content"].(string), "Never guess a person's identity") {
			t.Fatal("identity instruction missing")
		}
		return "O Autor é Pessoa Inventada [1].", nil
	}, "pt")
	if err != nil || calls != 1 || answer != "O Autor é Pessoa Inventada [1]." || len(sources) != 1 || len(warnings) != 1 || !strings.Contains(warnings[0], "Advisory") || strings.Contains(warnings[0], "withheld") {
		t.Fatal(answer, warnings, err)
	}
}
func TestAdvisoryNamesUseOnlyCitedEvidence(t *testing.T) {
	docs := []duck.Document{{ID: 1, Text: "O Autor fala sobre o tema."}, {ID: 2, Text: "Pessoa Inventada foi mencionado em outro trecho."}}
	calls := 0
	answer, _, warnings, err := groundedAnswer(docs, []model.D{model.TextMessage("user", "Quem?")}, func([]model.D) (string, error) { calls++; return "Pessoa Inventada [1].", nil })
	if err != nil || calls != 1 || answer == "" || len(warnings) != 1 {
		t.Fatal(answer, warnings, err)
	}
}

func TestAnswerCitationsAndSavedReferenceUseSameNumber(t *testing.T) {
	docs := []duck.Document{{ID: 1, Text: "First passage"}, {ID: 2, Text: "Second passage", Sources: []string{"second.doc | section 1"}}}
	answer, sources, _, err := groundedAnswer(docs, []model.D{model.TextMessage("user", "Question")}, func([]model.D) (string, error) { return "Evidence [2].", nil })
	if err != nil || answer != "Evidence [1]." || len(sources) != 1 || sources[0].Number != 1 || sources[0].Paths[0] != "second.doc" {
		t.Fatal(answer, sources, err)
	}
}

func TestTranslatedWorkTitlesAreNotPersonalNames(t *testing.T) {
	sources := []source{{Text: "El Autor dió enseñanzas, por ejemplo, en ‘Conocimiento Consciente’ al hablar de política. El tema es mencionado."}}
	for _, answer := range []string{
		"O Autor menciona Conhecimento Consciente [1].",
		"O título é Conhecimento Consciente [1].",
		"Na obra Conhecimento Consciente (Conocimiento Consciente), fala de política [1].",
		"The book Conscious Knowledge discusses politics [1].",
		"O livro Livro Exemplo é mencionado [1].",
	} {
		if names := unsupportedNames(answer, sources); len(names) != 0 {
			t.Fatal(answer, names)
		}
	}
	for _, answer := range []string{
		"O Autor é Pessoa Inventada e menciona Conhecimento Consciente [1].",
		"Na obra Conhecimento Consciente, Pessoa Inventada aparece [1].",
	} {
		names := unsupportedNames(answer, sources)
		if len(names) != 1 || names[0] != "Pessoa Inventada" {
			t.Fatal(answer, names)
		}
	}
}
func TestReportedTitleDoesNotWithholdGroundedAnswer(t *testing.T) {
	docs := []duck.Document{{ID: 1, Text: "En Conocimiento Consciente el Autor dió enseñanzas sobre política."}}
	calls := 0
	answer, _, warnings, err := groundedAnswer(docs, []model.D{model.TextMessage("user", "o que Autor fala sobre o tema?")}, func([]model.D) (string, error) {
		calls++
		return "O trecho menciona Conhecimento Consciente (Conocimiento Consciente) [1].", nil
	}, "pt")
	if err != nil || calls != 1 || len(warnings) != 0 || !strings.Contains(answer, "Conhecimento Consciente") {
		t.Fatal(answer, warnings, err)
	}
}

func TestRolePhrasesAreNotPersonalNames(t *testing.T) {
	sources := []source{{Text: "El Autor dio enseñanzas sobre el tema."}}
	for _, answer := range []string{
		"Ensinamentos do Autor [1].",
		"Ensinamentos Do Autor [1].",
		"ENSINAMENTOS DO AUTOR [1].",
		"O tema é Ensinamentos do Autor [1].",
		"Enseñanzas del Autor [1].",
		"Teachings of the Teacher [1].",
		"Palavras do Autor [1].",
	} {
		if names := unsupportedNames(answer, sources); len(names) > 0 {
			t.Fatal(answer, names)
		}
	}
	for _, answer := range []string{
		"O Autor é Pessoa Inventada [1].",
		"Ensinamentos do Autor: Pessoa Inventada escreveu o trecho [1].",
		"Pessoa Inventada do Autor [1].",
	} {
		names := unsupportedNames(answer, sources)
		if len(names) != 1 || names[0] != "Pessoa Inventada" {
			t.Fatal(answer, names)
		}
	}
}
func TestRoleHeadingDoesNotWithholdAnswer(t *testing.T) {
	docs := []duck.Document{{ID: 1, Text: "El Autor dio enseñanzas sobre el tema."}}
	calls := 0
	answer, _, warnings, err := groundedAnswer(docs, []model.D{model.TextMessage("user", "O que o autor fala sobre o tema?")}, func([]model.D) (string, error) {
		calls++
		return "**Ensinamentos do Autor**: o trecho apresenta ensinamentos sobre o tema [1].", nil
	}, "pt")
	if err != nil || calls != 1 || len(warnings) != 0 || !strings.Contains(answer, "Ensinamentos do Autor") {
		t.Fatal(answer, warnings, err)
	}
}

func TestDescriptiveHeadingsDoNotRequireKnownRole(t *testing.T) {
	sources := []source{{Text: "O trecho apresenta ensinamentos sobre o tema."}}
	for _, answer := range []string{"Ensinamentos do Orientador [1].", "ENSINAMENTOS DO ORIENTADOR [1].", "Teachings of the Guide [1].", "O tema é Ensinamentos do Orientador [1]."} {
		if names := unsupportedNames(answer, sources); len(names) > 0 {
			t.Fatal(answer, names)
		}
	}
	names := unsupportedNames("Ensinamentos de Pessoa Inventada [1].", sources)
	if len(names) != 1 || names[0] != "Pessoa Inventada" {
		t.Fatal(names)
	}
}

func TestNameExpansionIsAdvisory(t *testing.T) {
	docs := []duck.Document{{ID: 1, Text: "Pessoa pede reflexão."}}
	calls := 0
	answer, _, warnings, err := groundedAnswer(docs, []model.D{model.TextMessage("user", "Pessoa")}, func(prompt []model.D) (string, error) {
		calls++
		if !strings.Contains(prompt[len(prompt)-1]["content"].(string), "A one-word query") {
			t.Fatal("short-query rule missing")
		}
		return "Pessoa de Lugar pede reflexão [1].", nil
	}, "pt")
	if err != nil || calls != 1 || answer != "Pessoa de Lugar pede reflexão [1]." || len(warnings) != 1 {
		t.Fatal(answer, warnings, err)
	}
}

func TestNameWarningDoesNotBypassCitationValidation(t *testing.T) {
	docs := []duck.Document{{ID: 1, Text: "Evidence"}}
	calls := 0
	answer, _, warnings, err := groundedAnswer(docs, []model.D{model.TextMessage("user", "Question")}, func([]model.D) (string, error) { calls++; return "Pessoa Inventada [99].", nil })
	if err == nil || calls != 2 || strings.Contains(answer, "Pessoa Inventada") || len(warnings) != 0 {
		t.Fatal(answer, warnings, err, calls)
	}
}
