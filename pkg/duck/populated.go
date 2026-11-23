package duck

import "database/sql"

// HasChunks reports whether the items table contains any row, independently of
// index metadata. It only reads the connected database and stops at the first row.
func HasChunks(db *sql.DB) (bool, error) {
	var exists bool
	if err := remoteRow(db, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'items')").Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if err := remoteRow(db, "SELECT EXISTS (SELECT 1 FROM items LIMIT 1)").Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}
