package duck

import (
	"database/sql"
	"testing"
)

func populatedTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("CREATE SCHEMA jar_remote; CREATE MACRO jar_remote.query(statement) AS TABLE FROM query(statement)"); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestHasChunksIgnoresIndexMetadata(t *testing.T) {
	for _, test := range []struct {
		name string
		sql  string
		want bool
	}{
		{name: "missing items table"},
		{name: "empty items table", sql: "CREATE TABLE items (id INTEGER)"},
		{name: "populated without metadata", sql: "CREATE TABLE items (id INTEGER); INSERT INTO items VALUES (1)", want: true},
		{name: "populated with empty metadata", sql: "CREATE TABLE items (id INTEGER); INSERT INTO items VALUES (1); CREATE TABLE jar_index_metadata (fingerprint VARCHAR)", want: true},
		{name: "items in unrelated schema", sql: "CREATE SCHEMA unrelated; CREATE TABLE unrelated.items (id INTEGER); INSERT INTO unrelated.items VALUES (1)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := populatedTestDB(t)
			if test.sql != "" {
				if _, err := db.Exec(test.sql); err != nil {
					t.Fatal(err)
				}
			}
			got, err := HasChunks(db)
			if err != nil || got != test.want {
				t.Fatalf("HasChunks: got %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func TestHasChunksPropagatesQueryErrors(t *testing.T) {
	db := populatedTestDB(t)
	if _, err := db.Exec("DROP MACRO jar_remote.query"); err != nil {
		t.Fatal(err)
	}
	if got, err := HasChunks(db); got || err == nil {
		t.Fatalf("missing transport should fail, got %v, %v", got, err)
	}
}
