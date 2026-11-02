package duck

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// RetrieveConferences returns all indexed passages, without vector top-k or
// generation limits. Original extraction/chunk boundaries may contain overlap.
func RetrieveConferences(db *sql.DB, filter SourceFilter) (string, error) {
	if filter.Book != "" || filter.Date == "" {
		return "", fmt.Errorf("a conference date is required")
	}
	if err := filter.Validate(); err != nil {
		return "", err
	}
	has, err := hasCatalog(db)
	if err != nil {
		return "", err
	}
	if !has {
		return "", fmt.Errorf("source metadata is missing; run make metadata")
	}
	where, args := sourceWhere(filter, "s.")
	rows, err := remoteQuery(db, `SELECT DISTINCT s.path,s.label,i.id,i.text FROM item_sources s JOIN items i ON i.id=s.chunk_id WHERE s.kind='conference' AND `+where, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var passages []conferencePassage
	for rows.Next() {
		var p conferencePassage
		if err := rows.Scan(&p.path, &p.label, &p.id, &p.text); err != nil {
			return "", err
		}
		passages = append(passages, p)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return renderConferences(passages, filter.Date), nil
}

type conferencePassage struct {
	path, label, text string
	id                int
}

func locationNumber(label string) int {
	_, suffix, _ := strings.Cut(label, " | ")
	fields := strings.Fields(suffix)
	if len(fields) > 1 {
		n, _ := strconv.Atoi(fields[len(fields)-1])
		return n
	}
	return 0
}
func renderConferences(passages []conferencePassage, date string) string {
	return renderJoinedSources(passages, "Conference text for filename date "+date+" (inferred).", "No indexed conferences match filename date "+date+".")
}
func renderJoinedSources(passages []conferencePassage, header, empty string) string {
	if len(passages) == 0 {
		return empty
	}
	sort.Slice(passages, func(i, j int) bool {
		a, b := passages[i], passages[j]
		if a.path != b.path {
			return a.path < b.path
		}
		if locationNumber(a.label) != locationNumber(b.label) {
			return locationNumber(a.label) < locationNumber(b.label)
		}
		return a.id < b.id
	})

	var b strings.Builder
	fmt.Fprintf(&b, "%s Indexed text joined in source order; extraction omissions may remain. Original language retained.\n", header)
	for i := 0; i < len(passages); {
		path := passages[i].path
		var pieces []string
		for i < len(passages) && passages[i].path == path {
			text := passages[i].text
			if strings.HasPrefix(text, "Source: ") {
				_, text, _ = strings.Cut(text, "\n")
			}
			pieces = append(pieces, text)
			i++
		}
		fmt.Fprintf(&b, "\nSource: %s\n%s\n", path, joinIndexedPassages(pieces))
	}
	return b.String()
}

// The indexer uses a 30-word overlap. Remove only that exact overlap at
// adjacent boundaries, retaining other repeated phrases and all source files.
func joinIndexedPassages(pieces []string) string {
	var b strings.Builder
	var previous []string
	for _, piece := range pieces {
		words := strings.Fields(piece)
		if len(previous) >= 30 && len(words) >= 30 && strings.Join(previous[len(previous)-30:], " ") == strings.Join(words[:30], " ") {
			piece = dropWords(piece, 30)
			if strings.TrimSpace(piece) != "" {
				b.WriteByte(' ')
			}
		} else if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.TrimSpace(piece))
		previous = words
	}
	return b.String()
}
func dropWords(text string, n int) string {
	count := 0
	inWord := false
	for i, r := range text {
		if unicode.IsSpace(r) {
			inWord = false
			continue
		}
		if !inWord {
			count++
			inWord = true
			if count > n {
				return text[i:]
			}
		}
	}
	return ""
}

// RetrieveSourceFiles matches the saved citation's exact paths, never a title
// substring or date that could accidentally select a different document.
func RetrieveSourceFiles(db *sql.DB, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("reference has no source path")
	}
	has, err := hasCatalog(db)
	if err != nil {
		return "", err
	}
	if !has {
		return "", fmt.Errorf("source metadata is missing; run make metadata")
	}
	var placeholders []string
	var args []any
	for _, path := range paths {
		placeholders = append(placeholders, "?")
		args = append(args, path)
	}
	rows, err := remoteQuery(db, `SELECT DISTINCT s.path,s.label,i.id,i.text FROM item_sources s JOIN items i ON i.id=s.chunk_id WHERE s.path IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var passages []conferencePassage
	for rows.Next() {
		var p conferencePassage
		if err := rows.Scan(&p.path, &p.label, &p.id, &p.text); err != nil {
			return "", err
		}
		passages = append(passages, p)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(passages) == 0 {
		return "", fmt.Errorf("referenced source files are no longer in the index")
	}
	return renderJoinedSources(passages, "Full indexed source text for the saved reference.", ""), nil
}
