package duck

import (
	"database/sql"
	"fmt"
)

// ChunkSourcePaths returns every indexed source file that contains a chunk.
// Deduplicated chunks keep all of their source locations, so one chunk may
// belong to several files. It only reads the source metadata catalog.
func ChunkSourcePaths(db *sql.DB, chunkID int) ([]string, error) {
	if chunkID < 0 {
		return nil, fmt.Errorf("chunk id must not be negative")
	}
	has, err := hasCatalog(db)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, fmt.Errorf("source metadata is missing; run make metadata")
	}
	rows, err := remoteQuery(db, "SELECT DISTINCT path FROM item_sources WHERE chunk_id=? ORDER BY path", chunkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("chunk %d is not in the index", chunkID)
	}
	return paths, nil
}
