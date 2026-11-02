package duck

import (
	"database/sql"
	"fmt"
	"github.com/brito-rafa/jar/pkg/catalog"
)

// BrowseSources lists distinct source files, not individual passage locations.
// Books includes non-conference documents because filenames alone cannot verify
// whether an archived document is a published book.
func BrowseSources(db *sql.DB, kind, query string) ([]catalog.Origin, error) {
	if kind != "books" && kind != "conferences" {
		return nil, fmt.Errorf("use books or conferences")
	}
	has, err := hasCatalog(db)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, fmt.Errorf("source metadata is missing; run make metadata")
	}
	where := "kind <> 'conference'"
	if kind == "conferences" {
		where = "kind = 'conference'"
	}
	rows, err := remoteQuery(db, `SELECT DISTINCT path,title,kind,date_key,precision,raw_code,search_key FROM item_sources WHERE `+where+` AND (contains(search_key,?) OR contains(date_key,?)) ORDER BY date_key,path`, catalog.Normalize(query), query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []catalog.Origin
	for rows.Next() {
		var o catalog.Origin
		if err := rows.Scan(&o.Path, &o.Title, &o.Kind, &o.Date, &o.Precision, &o.RawCode, &o.SearchKey); err != nil {
			return nil, err
		}
		o.Label = o.Path
		result = append(result, o)
	}
	return result, rows.Err()
}
