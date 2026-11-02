package duck

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

var paragraphBreak = regexp.MustCompile(`\r?\n[\t ]*\r?\n`)

func neighborParagraph(text string, previous bool) string {
	if strings.HasPrefix(text, "Source: ") {
		_, text, _ = strings.Cut(text, "\n")
	}
	parts := paragraphBreak.Split(strings.TrimSpace(text), -1)
	if previous {
		return strings.TrimSpace(parts[len(parts)-1])
	}
	return strings.TrimSpace(parts[0])
}
func adjacentPassage(db *sql.DB, path string, anchor conferencePassage, previous bool) (*conferencePassage, error) {
	location := `coalesce(try_cast(regexp_extract(s.label,'(page|section) ([0-9]+)$',2) AS INTEGER),0)`
	op, order := ">", "ASC"
	if previous {
		op, order = "<", "DESC"
	}
	query := `SELECT DISTINCT s.path,s.label,i.id,i.text,` + location + ` AS location FROM item_sources s JOIN items i ON i.id=s.chunk_id WHERE s.path=? AND i.id<>? AND (` + location + op + `? OR (` + location + `=? AND i.id` + op + `?)) ORDER BY location ` + order + `,i.id ` + order + ` LIMIT 1`
	n := locationNumber(anchor.label)
	rows, err := remoteQuery(db, query, path, anchor.id, n, n, anchor.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var p conferencePassage
	var loc int
	if err := rows.Scan(&p.path, &p.label, &p.id, &p.text, &loc); err != nil {
		return nil, err
	}
	return &p, rows.Err()
}
func retrieveNearbyContext(db *sql.DB, path string, anchors []conferencePassage) (string, error) {
	var out strings.Builder
	out.WriteString("Book chapter boundaries unavailable or ambiguous: cited passage plus previous and next paragraph where available. Paragraph breaks were flattened by existing indexing; an adjacent indexed chunk is used when an exact paragraph boundary is unavailable. The full book was not assembled.\n")
	for _, anchor := range anchors {
		previous, err := adjacentPassage(db, path, anchor, true)
		if err != nil {
			return "", err
		}
		next, err := adjacentPassage(db, path, anchor, false)
		if err != nil {
			return "", err
		}
		var pieces, locations []string
		if previous != nil {
			pieces = append(pieces, neighborParagraph(previous.text, true))
			locations = append(locations, previous.label)
		}
		pieces = append(pieces, neighborText(anchor.text))
		locations = append(locations, anchor.label)
		if next != nil {
			pieces = append(pieces, neighborParagraph(next.text, false))
			locations = append(locations, next.label)
		}
		fmt.Fprintf(&out, "\nSource: %s\nIncluded locations: %s\n%s\n", anchor.label, strings.Join(locations, "; "), joinIndexedPassages(pieces))
	}
	return out.String(), nil
}
func neighborText(text string) string {
	if strings.HasPrefix(text, "Source: ") {
		_, text, _ = strings.Cut(text, "\n")
	}
	return text
}
