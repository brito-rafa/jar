package chat

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/brito-rafa/jar/pkg/catalog"
)

var nameWords = regexp.MustCompile(`[\p{L}][\p{L}\p{M}'’\-]*`)
var roleComplement = regexp.MustCompile(`(?i)^[*_]*\s+(?:do|da|de|del|de la|of the|of)\s+[*_]*(?:mestre|teacher|autor|autora|author|professor|professora)\b`)
var identityCue = regexp.MustCompile(`(?:^|[^\p{L}])(?i:presumivelmente|presumiblemente|presumably|identified as|identificado como|se chama|se llama|named|called|é|es|is|era|was|Dr\.?|Dra\.?|Sr\.?|Sra\.?|Mr\.?|Mrs\.?)\s+[*_"“]*(\p{Lu}[\p{L}\p{M}'’\-]*)`)

// This is a conservative name-candidate check, not full named-entity
// recognition or proof of identity. It deliberately does not use question,
// conversation history, filenames, or the model's knowledge as evidence.
func unsupportedNames(answer string, sources []source) []string {
	candidates := map[string]string{}
	add := func(name string) {
		key := catalog.Normalize(name)
		if key == "" {
			return
		}
		for _, s := range sources {
			if strings.Contains(" "+catalog.Normalize(s.Text)+" ", " "+key+" ") {
				return
			}
		}
		candidates[key] = name
	}
	type span struct{ start, end int }
	var workTitles []span
	matches := nameWords.FindAllStringIndex(answer, -1)
	for i := 0; i < len(matches); {
		word := answer[matches[i][0]:matches[i][1]]
		if !capitalized(word) || genericIdentityWord(word) {
			i++
			continue
		}
		end := i + 1
		capitals := 1
		for end < len(matches) {
			gap := answer[matches[end-1][1]:matches[end][0]]
			if strings.ContainsAny(gap, ".!?;:\n\r,()[]/") {
				break
			}
			next := answer[matches[end][0]:matches[end][1]]
			if capitalized(next) && !genericIdentityWord(next) && !nameParticle(next) {
				capitals++
				end++
				continue
			}
			if nameParticle(next) && end+1 < len(matches) && capitalized(answer[matches[end+1][0]:matches[end+1][1]]) && !genericIdentityWord(answer[matches[end+1][0]:matches[end+1][1]]) {
				end++
				continue
			}
			break
		}
		// A generic role does not turn the preceding ordinary noun into a
		// personal name, even when a heading capitalizes both words.
		if capitals == 1 {
			finish := matches[i][1]
			if complement := roleComplement.FindStringIndex(answer[finish:]); complement != nil {
				workTitles = append(workTitles, span{matches[i][0], finish + complement[1]})
			}
		}
		if capitals >= 2 {
			var words []string
			for _, m := range matches[i:end] {
				words = append(words, answer[m[0]:m[1]])
			}
			phrase := strings.Join(words, " ")
			start, finish := matches[i][0], matches[end-1][1]
			if nonPersonTitle(phrase, answer[:start]) {
				workTitles = append(workTitles, span{start, finish})
			} else {
				add(phrase)
			}
		}
		i = end
	}
	for _, m := range identityCue.FindAllStringSubmatchIndex(answer, -1) {
		word := answer[m[2]:m[3]]
		title := false
		for _, s := range workTitles {
			if m[2] >= s.start && m[2] < s.end {
				title = true
				break
			}
		}
		if !title && !genericIdentityWord(word) {
			add(word)
		}
	}

	// Prefer a full name over a duplicate single-word candidate.
	var result []string
	for key, name := range candidates {
		contained := false
		for other := range candidates {
			if other != key && strings.Contains(" "+other+" ", " "+key+" ") {
				contained = true
				break
			}
		}
		if !contained {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}
func capitalized(word string) bool {
	r := []rune(word)
	return len(r) > 1 && unicode.IsUpper(r[0])
}
func nameParticle(word string) bool {
	switch strings.ToLower(word) {
	case "de", "da", "do", "das", "dos", "del", "la", "y", "van", "von":
		return true
	}
	return false
}
func genericIdentityWord(word string) bool {
	// Descriptive heading nouns and connectors are not personal-name tokens.
	// This handles role headings without any collection-specific identity.
	if nameParticle(word) {
		return true
	}
	switch catalog.Normalize(word) {
	case "ensinamentos", "ensenanzas", "teachings", "palavras", "palabras", "words", "licoes", "lecciones", "lessons", "ideias", "ideas", "opinioes", "opiniones", "views":
		return true
	}

	switch strings.ToLower(word) {
	case "a", "an", "the", "this", "that", "he", "she", "it", "um", "uma", "o", "os", "as", "ele", "ela", "un", "una", "el", "él", "ella", "mestre", "teacher", "autor", "autora", "author", "professor", "professora":
		return true
	}
	return false
}

var workTitleCue = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:livro|obra|título|titulo|book|work|title|artigo|artículo|article|conferência|conferencia|conference)(?:\s+(?:intitulado|intitulada|chamado|chamada|denominado|denominada|called|titled|o|a|el|la|the))*\s+[*_"“‘]*$`)

// Capitalization alone does not distinguish people from translated titles.
// Exclude explicit work-title contexts and multiword abstract subject phrases.
// This classifies candidate type only; it does not validate the title or claim.
func nonPersonTitle(phrase, before string) bool {
	if workTitleCue.MatchString(before) {
		return true
	}
	concepts := map[string]bool{}
	for _, word := range strings.Fields(catalog.Normalize(phrase)) {
		key := ""
		switch word {
		case "concepcao", "concepcion", "conception":
			key = "conception"
		case "politica", "politico", "politics", "political":
			key = "politics"
		case "vida", "life":
			key = "life"
		case "consciente", "conscious":
			key = "conscious"
		case "conhecimento", "conocimiento", "knowledge":
			key = "knowledge"
		case "mental", "mente", "mind":
			key = "mind"
		case "conduta", "conducta", "conduct":
			key = "conduct"
		case "humano", "humana", "human":
			key = "human"
		case "deficiencias", "deficiencies":
			key = "deficiencies"
		case "propensoes", "propensiones", "propensities":
			key = "propensities"
		case "logosofia", "logosophy":
			key = "logosophy"
		case "espirito", "espiritu", "spirit":
			key = "spirit"
		case "etica", "ethics":
			key = "ethics"
		case "filosofia", "philosophy":
			key = "philosophy"
		case "ciencia", "science":
			key = "science"
		case "metodo", "method":
			key = "method"
		case "ensino", "ensenanza", "teaching":
			key = "teaching"
		case "tratado", "treatise":
			key = "treatise"
		case "elemental", "elementary":
			key = "elementary"
		}
		if key != "" {
			concepts[key] = true
		}
	}
	return len(concepts) >= 2
}
