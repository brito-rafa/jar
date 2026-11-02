package chat

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/brito-rafa/jar/pkg/catalog"
	"github.com/brito-rafa/jar/pkg/duck"
)

var errCitationValidation = errors.New("answer citations could not be validated")
var citationPattern = regexp.MustCompile(`\[(?:Chunk\s+)?[0-9][^\]]*\]`)

type source struct {
	Number      int
	ChunkID     int
	Label, Text string
	Paths       []string
}

func contextSources(documents []duck.Document) []source {
	var result []source
	for i, doc := range documents {
		if i == contextLimit {
			break
		}
		label := fmt.Sprintf("Indexed chunk %d (source filename unavailable)", doc.ID)
		text := doc.Text
		first, rest, found := strings.Cut(text, "\n")
		if after, ok := strings.CutPrefix(first, "Source: "); ok {
			label = after
			if found {
				text = rest
			}
		}
		if len(doc.Origins) > 0 {
			var descriptions []string
			for _, o := range doc.Origins {
				descriptions = append(descriptions, o.Description())
			}
			label = strings.Join(descriptions, "; ")
		} else if len(doc.Sources) > 0 {
			label = strings.Join(doc.Sources, "; ")
		}
		var paths []string
		seen := map[string]bool{}
		addPath := func(path string) {
			if path != "" && !seen[path] {
				paths = append(paths, path)
				seen[path] = true
			}
		}
		for _, o := range doc.Origins {
			addPath(o.Path)
		}
		if len(paths) == 0 {
			for _, s := range doc.Sources {
				addPath(catalog.Parse(s).Path)
			}
		}
		if len(paths) == 0 && strings.HasPrefix(first, "Source: ") {
			addPath(catalog.Parse(strings.TrimPrefix(first, "Source: ")).Path)
		}
		result = append(result, source{Number: i + 1, ChunkID: doc.ID, Label: label, Text: text, Paths: paths})
	}
	return result
}

func validateCitations(answer string, sources []source) ([]source, error) {
	matches := citationPattern.FindAllString(answer, -1)
	if len(matches) == 0 {
		return nil, errCitationValidation
	}
	cited := map[int]bool{}
	for _, match := range matches {
		// Multiple valid references such as [1, 2] still point only to supplied IDs.
		for part := range strings.SplitSeq(match[1:len(match)-1], ",") {
			n, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || n < 1 || n > len(sources) {
				return nil, errCitationValidation
			}
			cited[n] = true
		}
	}

	var used []source
	for _, source := range sources {
		if cited[source.Number] {
			used = append(used, source)
		}
	}
	return used, nil
}

// Citation failures retry once. Name candidates are advisory and never block
// an otherwise citation-valid answer.
func citedAnswer(docs []duck.Document, messages []model.D, generate func([]model.D) (string, error), language ...string) (string, []source, error) {
	answer, sources, _, err := groundedAnswer(docs, messages, generate, language...)
	return answer, sources, err
}
func groundedAnswer(docs []duck.Document, messages []model.D, generate func([]model.D) (string, error), language ...string) (string, []source, []string, error) {
	sources := contextSources(docs)
	if len(sources) > 0 {
		for attempt := range 2 {
			prompt := addContextPrompt(docs, messages, language...)
			if attempt > 0 {
				last := len(prompt) - 1
				prompt[last] = model.TextMessage("user", prompt[last]["content"].(string)+"\nYour previous draft failed citation validation. Rewrite using explicit numbered citations and only source IDs present in the supplied context. Do not guess identities.")
			}
			answer, err := generate(prompt)
			if err != nil {
				return "", sources, nil, err
			}
			used, citationErr := validateCitations(answer, sources)
			if citationErr != nil {
				continue
			}
			var warnings []string
			if names := unsupportedNames(answer, used); len(names) > 0 {
				warnings = append(warnings, "Advisory name check: candidates not found in cited passage text: "+strings.Join(names, ", ")+". The answer is displayed; verify these names against the sources. This heuristic can flag non-name phrases.")
			}
			answer, used = renumberCitations(answer, used)
			return answer, used, warnings, nil
		}
	}
	question := ""
	if len(messages) > 0 {
		question, _ = messages[len(messages)-1]["content"].(string)
	}
	failure := "I could not produce an answer with valid source references. Please review the retrieved sources or rephrase the question."
	switch effectiveLanguage(question, language...) {
	case "pt":
		failure = "Não foi possível produzir uma resposta com referências válidas. Consulte as fontes recuperadas ou reformule a pergunta."
	case "es":
		failure = "No fue posible producir una respuesta con referencias válidas. Consulte las fuentes recuperadas o reformule la pregunta."
	}
	return failure, sources, nil, errCitationValidation
}

func printSources(w io.Writer, sources []source, unverified bool) {
	heading := "Sources"
	if unverified {
		heading = "Retrieved sources (no validated answer)"
	}
	fmt.Fprintf(w, "\n%s:\n", heading)
	if len(sources) == 0 {
		fmt.Fprintln(w, "No source passages were retrieved.")
		return
	}
	for _, source := range sources {
		fmt.Fprintf(w, "[%d] %s\n", source.Number, source.Label)
	}
}

// Read the exact displayed citation snapshot, not a new vector search or model
// reconstruction. Visible numbers use the final answer citation mapping.
func renderReference(sources []source, number int) string {
	if len(sources) == 0 {
		return "No references are available. Ask a question first; conversation resets clear references."
	}
	for _, s := range sources {
		if s.Number == number {
			return fmt.Sprintf("Reference [%d] — exact indexed passage (original language):\nSource: %s\n\n%s", number, s.Label, s.Text)
		}
	}
	return fmt.Sprintf("Reference [%d] is not in the latest Sources list. Use a number shown there.", number)
}

// Terminal completion can read references independently of the chat loop.
type referenceStore struct {
	mu      sync.RWMutex
	sources []source
}

func (r *referenceStore) set(sources []source) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources = append([]source(nil), sources...)
}
func (r *referenceStore) snapshot() []source {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]source(nil), r.sources...)
}

// Rewrite all references in one pass; avoid cascading replacements such as
// old [4] -> [2] -> [1]. The visible list and /retrieveref share this mapping.
func renumberCitations(answer string, sources []source) (string, []source) {
	numbers := map[int]int{}
	for _, match := range citationPattern.FindAllString(answer, -1) {
		for part := range strings.SplitSeq(match[1:len(match)-1], ",") {
			old, err := strconv.Atoi(strings.TrimSpace(part))
			if err == nil && numbers[old] == 0 {
				numbers[old] = len(numbers) + 1
			}
		}
	}
	result := append([]source(nil), sources...)
	for i := range result {
		result[i].Number = numbers[result[i].Number]
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Number < result[j].Number })
	answer = citationPattern.ReplaceAllStringFunc(answer, func(match string) string {
		var refs []string
		for part := range strings.SplitSeq(match[1:len(match)-1], ",") {
			old, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				return match
			}
			n, ok := numbers[old]
			if !ok {
				return match
			}
			refs = append(refs, strconv.Itoa(n))
		}
		return "[" + strings.Join(refs, ", ") + "]"
	})
	return answer, result
}
