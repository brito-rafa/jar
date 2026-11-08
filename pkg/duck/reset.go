package duck

import "database/sql"

// Reset removes only JAR's index tables, preserving unrelated server data.
func Reset(db *sql.DB) error {
	if err := remoteExec(db, "BEGIN TRANSACTION"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = remoteExec(db, "ROLLBACK")
		}
	}()
	for _, table := range []string{"item_sources", "items", "jar_index_metadata"} {
		if err := remoteExec(db, "DROP TABLE IF EXISTS "+table); err != nil {
			return err
		}
	}
	if err := remoteExec(db, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}
