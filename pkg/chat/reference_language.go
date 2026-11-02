package chat

import (
	"strings"

	"github.com/abadojack/whatlanggo"
)

// Strip generated reference labels before detection: an English filename or
// UI header is not evidence that the document itself is English.
func referenceBody(text string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		skip := false
		for _, prefix := range []string{"Reference [", "Source:", "Included locations:", "---", "Full indexed source text", "Conference text for filename date", "Book context:", "Book chapter boundaries unavailable:", "This is one indexed chunk;", "Could not expand the source:"} {
			if strings.HasPrefix(trimmed, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
func alreadyInLanguage(text, target string) bool {
	if target != "en" && target != "pt" && target != "es" {
		return false
	}
	body := referenceBody(text)
	words := strings.Fields(body)
	if len(words) < 24 {
		return false
	} // Uncertain short fragments still use translation.
	info := whatlanggo.Detect(body)
	if info.Lang.Iso6391() != target || !info.IsReliable() {
		return false
	}
	// Check separate halves too, rather than trusting a majority-language result
	// for mixed-language content.
	half := len(words) / 2
	return detectLanguage(strings.Join(words[:half], " ")) == target && detectLanguage(strings.Join(words[half:], " ")) == target
}
