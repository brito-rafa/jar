package duck

import (
	"os"
	"strings"
	"testing"
)

func TestRemoteTransactionRollback(t *testing.T) {
	if os.Getenv("JAR_TEST_REMOTE") == "" {
		t.Skip("remote test disabled")
	}
	db, err := OpenRemote("quack:127.0.0.1:9494", os.Getenv("JAR_DB_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("FROM jar_remote.query(?)", "SELECT 42").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 42 {
		t.Fatal(n)
	}
	if _, err := db.Exec("FROM jar_remote.query(?)", "CREATE TABLE IF NOT EXISTS jar_probe (id INT, text VARCHAR, embedding FLOAT[2])"); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("FROM jar_remote.query(?)", "DROP TABLE jar_probe")
	for _, query := range []string{"BEGIN", "INSERT INTO jar_probe VALUES (1, 'hello', [0.1,0.2])", "ROLLBACK"} {
		if _, err := db.Exec("FROM jar_remote.query(?)", query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}

	if err := db.QueryRow("FROM jar_remote.query(?)", "SELECT count(*) FROM jar_probe").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal(n)
	}
}

func TestRemoteValuesAndAuthentication(t *testing.T) {
	if os.Getenv("JAR_TEST_REMOTE") == "" {
		t.Skip("remote test disabled")
	}
	db, err := OpenRemote("quack:127.0.0.1:9494", os.Getenv("JAR_DB_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	text := "SÍ MISMO, 'cita'\x00; DROP TABLE items;"
	rows, err := remoteQuery(db, "SELECT ? AS text, array_cosine_similarity(?, ?) AS similarity", text, []float32{1, 0}, []float32{1, 0})
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing result")
	}
	var got string
	var similarity float64
	if err := rows.Scan(&got, &similarity); err != nil {
		t.Fatal(err)
	}
	if got != text || similarity != 1 {
		t.Fatalf("roundtrip: %q, %f", got, similarity)
	}
	bad, err := OpenRemote("quack:127.0.0.1:9494", "invalid-test-token")
	if bad != nil {
		bad.Close()
	}
	if err == nil {
		t.Fatal("invalid token accepted")
	}
	if strings.Contains(err.Error(), "invalid-test-token") {
		t.Fatal("token exposed in error")
	}
}
