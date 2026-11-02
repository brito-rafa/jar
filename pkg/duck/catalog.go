package duck

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/brito-rafa/jar/pkg/catalog"
	"strings"
)

type SourceFilter struct{ Book, Date string }

func (f SourceFilter) Active() bool { return f.Book != "" || f.Date != "" }
func (f SourceFilter) Description() string {
	if f.Date != "" {
		return "filename date " + f.Date
	}
	if f.Book != "" {
		return "book/file " + f.Book
	}
	return "all sources"
}
func (f SourceFilter) Validate() error {
	if f.Book != "" && f.Date != "" {
		return fmt.Errorf("choose a book or a date filter")
	}
	if f.Date != "" {
		return catalog.ValidateDate(f.Date)
	}
	if f.Book != "" && catalog.Normalize(f.Book) == "" {
		return fmt.Errorf("book search must contain letters or digits")
	}
	return nil
}
func sourceWhere(f SourceFilter, alias string) (string, []any) {
	if f.Book != "" {
		return "contains(" + alias + "search_key, ?)", []any{catalog.Normalize(f.Book)}
	}
	if f.Date != "" {
		return "starts_with(" + alias + "date_key, ?)", []any{f.Date}
	}
	return "true", nil
}
func hasCatalog(db *sql.DB) (bool, error) {
	var n int
	err := remoteRow(db, "SELECT count(*) FROM information_schema.tables WHERE table_name='item_sources'").Scan(&n)
	return n > 0, err
}
func createCatalog(db *sql.DB) error {
	return remoteExec(db, `CREATE TABLE item_sources (chunk_id INTEGER, label VARCHAR, path VARCHAR, title VARCHAR, kind VARCHAR, date_key VARCHAR, precision VARCHAR, raw_code VARCHAR, search_key VARCHAR)`)
}
func originInsert(rows []embeddedPassage) (string, error) {
	var values []string
	for _, row := range rows {
		for _, label := range row.Sources {
			o := catalog.Parse(label)
			fields := []any{row.ID, o.Label, o.Path, o.Title, o.Kind, o.Date, o.Precision, o.RawCode, o.SearchKey}
			var parts []string
			for _, field := range fields {
				literal, err := remoteLiteral(field)
				if err != nil {
					return "", err
				}
				parts = append(parts, literal)
			}
			values = append(values, "("+strings.Join(parts, ",")+")")
		}
	}
	if len(values) == 0 {
		return "", nil
	}
	return "INSERT INTO item_sources VALUES " + strings.Join(values, ","), nil
}

// BuildCatalog derives metadata from stored source labels without re-embedding.
// It is explicit indexing work; chat never runs this migration itself.
func BuildCatalog(db *sql.DB) error {
	state, err := Status(db)
	if err != nil {
		return err
	}
	if !state.Ready {
		return fmt.Errorf("no ready index; load documents first")
	}
	if err := remoteExec(db, "BEGIN"); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			remoteExec(db, "ROLLBACK")
		}
	}()
	if err := remoteExec(db, "DROP TABLE IF EXISTS item_sources"); err != nil {
		return err
	}
	if err := createCatalog(db); err != nil {
		return err
	}
	var sourceColumns int
	if err := remoteRow(db, "SELECT count(*) FROM information_schema.columns WHERE table_name='items' AND column_name='sources'").Scan(&sourceColumns); err != nil {
		return err
	}
	expr := "NULL::VARCHAR"
	if sourceColumns > 0 {
		expr = "sources"
	}
	last := -1
	count := 0
	for {
		rows, err := remoteQuery(db, fmt.Sprintf("SELECT id,split_part(text,chr(10),1),%s FROM items WHERE id>? ORDER BY id LIMIT 256", expr), last)
		if err != nil {
			return err
		}
		var batch []embeddedPassage
		for rows.Next() {
			var p embeddedPassage
			var header string
			var sources sql.NullString
			if err := rows.Scan(&p.ID, &header, &sources); err != nil {
				rows.Close()
				return err
			}
			if sources.Valid {
				if err := json.Unmarshal([]byte(sources.String), &p.Sources); err != nil {
					rows.Close()
					return err
				}
			}
			if len(p.Sources) == 0 && strings.HasPrefix(header, "Source: ") {
				p.Sources = []string{strings.TrimPrefix(header, "Source: ")}
			}
			batch = append(batch, p)
			last = p.ID
		}
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return rowErr
		}
		if len(batch) == 0 {
			break
		}
		query, err := originInsert(batch)
		if err != nil {
			return err
		}
		if query != "" {
			if err := remoteExec(db, query); err != nil {
				return err
			}
		}
		count += len(batch)
	}
	if err := remoteExec(db, "COMMIT"); err != nil {
		return err
	}
	done = true
	fmt.Printf("Source metadata catalog ready (%d chunks scanned; embeddings unchanged).\n", count)
	return nil
}

func ListSources(db *sql.DB, filter SourceFilter) ([]catalog.Origin, int, error) {
	if err := filter.Validate(); err != nil {
		return nil, 0, err
	}
	has, err := hasCatalog(db)
	if err != nil {
		return nil, 0, err
	}
	if !has {
		return nil, 0, fmt.Errorf("source metadata is missing; run make metadata (no re-embedding needed)")
	}
	where, args := sourceWhere(filter, "")
	var count int
	rows, err := remoteQuery(db, "SELECT count(DISTINCT chunk_id) FROM item_sources WHERE "+where, args...)
	if err != nil {
		return nil, 0, err
	}
	if rows.Next() {
		err = rows.Scan(&count)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	rows, err = remoteQuery(db, "SELECT DISTINCT label,path,title,kind,date_key,precision,raw_code,search_key FROM item_sources WHERE "+where+" ORDER BY path,label LIMIT 20", args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var result []catalog.Origin
	for rows.Next() {
		var o catalog.Origin
		if err := rows.Scan(&o.Label, &o.Path, &o.Title, &o.Kind, &o.Date, &o.Precision, &o.RawCode, &o.SearchKey); err != nil {
			return nil, 0, err
		}
		result = append(result, o)
	}
	return result, count, rows.Err()
}

func attachOrigins(db *sql.DB, docs []Document, filter SourceFilter) error {
	if len(docs) == 0 {
		return nil
	}
	has, err := hasCatalog(db)
	if err != nil {
		return err
	}
	if !has {
		for i := range docs {
			for _, s := range docs[i].Sources {
				docs[i].Origins = append(docs[i].Origins, catalog.Parse(s))
			}
		}
		return nil
	}
	placeholders := make([]string, len(docs))
	args := make([]any, 0, len(docs)+1)
	byID := map[int]int{}
	for i, doc := range docs {
		placeholders[i] = "?"
		args = append(args, doc.ID)
		byID[doc.ID] = i
	}
	where, more := sourceWhere(filter, "")
	args = append(args, more...)
	rows, err := remoteQuery(db, "SELECT chunk_id,label,path,title,kind,date_key,precision,raw_code,search_key FROM item_sources WHERE chunk_id IN ("+strings.Join(placeholders, ",")+") AND "+where+" ORDER BY path,label", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for i := range docs {
		docs[i].Sources = nil
	}
	for rows.Next() {
		var id int
		var o catalog.Origin
		if err := rows.Scan(&id, &o.Label, &o.Path, &o.Title, &o.Kind, &o.Date, &o.Precision, &o.RawCode, &o.SearchKey); err != nil {
			return err
		}
		i := byID[id]
		docs[i].Origins = append(docs[i].Origins, o)
		docs[i].Sources = append(docs[i].Sources, o.Label)
	}
	return rows.Err()
}

// EnsureCatalog upgrades metadata only when it is missing.
func EnsureCatalog(db *sql.DB) error {
	has, err := hasCatalog(db)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	return BuildCatalog(db)
}
