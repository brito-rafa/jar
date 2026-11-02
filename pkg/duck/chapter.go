package duck

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/brito-rafa/jar/pkg/catalog"
)

// Only explicit chapter markers at a chunk boundary are trusted. Existing
// indexes have pages/sections but no authoritative chapter metadata.
const chapterPattern = `(?i)^\s*(?:chapter|cap[ií]tulo)\s+([0-9]+|[ivxlcdm]+)\b`

var chapterMarker = regexp.MustCompile(chapterPattern)

func passageLess(a, b conferencePassage) bool {
	if locationNumber(a.label) != locationNumber(b.label) {
		return locationNumber(a.label) < locationNumber(b.label)
	}
	return a.id < b.id
}
func chapterWindow(markers []conferencePassage, anchor conferencePassage) (*conferencePassage, *conferencePassage) {
	sort.Slice(markers, func(i, j int) bool { return passageLess(markers[i], markers[j]) })
	var headings []conferencePassage
	last := ""
	for _, p := range markers {
		m := chapterMarker.FindStringSubmatch(p.text)
		if m == nil {
			continue
		}
		key := strings.ToLower(m[1])
		if key == last {
			continue
		}
		last = key
		headings = append(headings, p)
	}
	// A lone marker may be a TOC entry; do not expand it to the whole book.
	if len(headings) < 2 {
		return nil, nil
	}
	for i := len(headings) - 1; i >= 0; i-- {
		if !passageLess(anchor, headings[i]) {
			start := headings[i]
			if i+1 < len(headings) {
				end := headings[i+1]
				return &start, &end
			}
			return &start, nil
		}
	}
	return nil, nil
}

// RetrieveReferenceContext expands conferences in full. For other documents it
// fetches chapter markers first, then only the matching chapter's body. Missing
// chapter boundaries fall back to the cited passage and bounded neighbors.
func RetrieveReferenceContext(db *sql.DB, paths []string, chunkID int) (string, error) {
	has, err := hasCatalog(db)
	if err != nil {
		return "", err
	}
	if !has {
		return "", fmt.Errorf("source metadata is missing; run make metadata")
	}
	var sections []string
	for _, path := range paths {
		if catalog.Parse(path).Kind == "conference" {
			text, err := RetrieveSourceFiles(db, []string{path})
			if err != nil {
				return "", err
			}
			sections = append(sections, text)
			continue
		}
		rows, err := remoteQuery(db, `SELECT DISTINCT s.path,s.label,i.id,i.text FROM item_sources s JOIN items i ON i.id=s.chunk_id WHERE s.path=? AND (i.id=? OR regexp_matches(i.text,?))`, path, chunkID, chapterPattern)
		if err != nil {
			return "", err
		}
		var anchor *conferencePassage
		var anchorLabels []string
		var anchors []conferencePassage
		var markers []conferencePassage
		for rows.Next() {
			var p conferencePassage
			if err = rows.Scan(&p.path, &p.label, &p.id, &p.text); err != nil {
				break
			}
			if p.id == chunkID {
				anchorLabels = append(anchorLabels, p.label)
				anchors = append(anchors, p)
				if anchor == nil {
					copy := p
					anchor = &copy
				}
			}
			if chapterMarker.MatchString(p.text) {
				markers = append(markers, p)
			}
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
		if rowErr != nil {
			return "", rowErr
		}
		if anchor == nil {
			return "", fmt.Errorf("saved reference no longer matches indexed file %s", path)
		}
		start, end := chapterWindow(markers, *anchor)
		if len(anchorLabels) > 1 {
			start, end = nil, nil
		}
		if start == nil {
			nearby, err := retrieveNearbyContext(db, path, anchors)
			if err != nil {
				return "", err
			}
			sections = append(sections, nearby)
			continue
		}
		location := `coalesce(try_cast(regexp_extract(s.label,'(page|section) ([0-9]+)$',2) AS INTEGER),0)`
		where := `s.path=?`
		args := []any{path}
		description := ""
		if start != nil {
			where += ` AND (` + location + `>? OR (` + location + `=? AND i.id>=?))`
			n := locationNumber(start.label)
			args = append(args, n, n, start.id)
			if end != nil {
				where += ` AND (` + location + `<? OR (` + location + `=? AND i.id<?))`
				n = locationNumber(end.label)
				args = append(args, n, n, end.id)
			}
			description = "Book context: explicit chapter boundaries inferred from indexed headings; original page/chunk order retained."

		}
		rows, err = remoteQuery(db, `SELECT DISTINCT s.path,s.label,i.id,i.text FROM item_sources s JOIN items i ON i.id=s.chunk_id WHERE `+where, args...)
		if err != nil {
			return "", err
		}
		var passages []conferencePassage
		for rows.Next() {
			var p conferencePassage
			if err = rows.Scan(&p.path, &p.label, &p.id, &p.text); err != nil {
				break
			}
			passages = append(passages, p)
		}
		rowErr = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
		if rowErr != nil {
			return "", rowErr
		}
		sections = append(sections, renderJoinedSources(passages, description, "No matching indexed chapter/page text."))
	}
	if len(sections) == 0 {
		return "", fmt.Errorf("reference has no source paths")
	}
	return strings.Join(sections, "\n\n"), nil
}
