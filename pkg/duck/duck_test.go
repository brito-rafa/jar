package duck

import (
	"database/sql"
	"testing"
)

func TestInsertChunkBindsTextAndVector(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE items (id INTEGER, text VARCHAR, embedding FLOAT[2])"); err != nil {
		t.Fatal(err)
	}
	text := "HSM.DOC\nSÍ MISMO, 'cita'\x00 fin; DROP TABLE items;"
	query, err := bindRemote("INSERT INTO items VALUES (?, ?, ?)", 1, text, []float32{0.1, 0.2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.QueryRow("SELECT text FROM items WHERE id=1").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != text {
		t.Fatalf("text changed: %q", got)
	}
}
