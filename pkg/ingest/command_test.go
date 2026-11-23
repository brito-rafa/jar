package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/brito-rafa/jar/pkg/connection"
	"strings"
	"testing"
)

func TestIndexRequiresDocumentDirectory(t *testing.T) {
	if err := Run(nil); err == nil || !strings.Contains(err.Error(), "-docs is required") {
		t.Fatalf("expected missing-directory error, got %v", err)
	}
}

func TestResetRejectsUnsafeArgumentsBeforeConnecting(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"-reset"}, "requires -confirm=erase"},
		{[]string{"-reset", "-confirm=no"}, "requires -confirm=erase"},
		{[]string{"-reset", "-confirm=erase", "-status"}, "cannot be combined"},
		{[]string{"-reset", "-confirm=erase", "-metadata"}, "cannot be combined"},
		{[]string{"-reset", "-confirm=erase", "-docs=fixture"}, "cannot be combined"},
	} {
		if err := Run(test.args); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("Run(%v): got %v, want %s", test.args, err, test.want)
		}
	}
}

func TestIndexRequiresEmbeddingEndpointBeforeConnecting(t *testing.T) {
	t.Setenv("EMBEDDING_URL", "")
	t.Setenv("DB_DISABLE_SSL", "")
	if err := Run([]string{"-docs=" + t.TempDir()}); err == nil || !strings.Contains(err.Error(), "embedding-url is required") {
		t.Fatalf("expected missing embedding endpoint error, got %v", err)
	}
}

func TestIndexRejectsOldProviderSelectionFlags(t *testing.T) {
	t.Setenv("DB_DISABLE_SSL", "")
	for _, name := range []string{"remote-embedding", "remote-embedding-model"} {
		if err := Run([]string{"-" + name + "=example.test"}); err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
			t.Fatalf("%s: expected unsupported flag, got %v", name, err)
		}
	}
}

// A local table macro exercises the normal remote-query wrapper without Quack,
// a network endpoint, or an extension download. The held connection remains
// available to inspect this disposable database after run closes its sql.DB.
func commandTestDB(t *testing.T, statements string) (*sql.DB, *sql.Conn) {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("CREATE SCHEMA jar_remote; CREATE MACRO jar_remote.query(statement) AS TABLE FROM query(statement)"); err != nil {
		t.Fatal(err)
	}
	if statements != "" {
		if _, err := db.Exec(statements); err != nil {
			t.Fatal(err)
		}
	}
	inspection, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inspection.Close() })
	return db, inspection
}

func clearCommandSettings(t *testing.T) {
	t.Helper()
	t.Setenv("DB_DISABLE_SSL", "")
	t.Setenv("EMBEDDING_URL", "")
}

func TestSkipIfPopulatedReturnsBeforeDocumentAndModelRequirements(t *testing.T) {
	clearCommandSettings(t)
	for _, metadata := range []bool{false, true} {
		t.Run(fmt.Sprintf("corruptMetadata=%t", metadata), func(t *testing.T) {
			statements := "CREATE TABLE items (id INTEGER, text VARCHAR); INSERT INTO items VALUES (42, 'Synthetic preserved passage')"
			if metadata {
				statements += "; CREATE TABLE jar_index_metadata (unexpected INTEGER); INSERT INTO jar_index_metadata VALUES (99)"
			}
			db, inspection := commandTestDB(t, statements)
			opened := 0
			err := run([]string{"-skip-if-populated", "-force=true"}, func(connection.Flags) (*sql.DB, error) {
				opened++
				return db, nil
			})
			if err != nil || opened != 1 {
				t.Fatalf("populated index should exit successfully after one open, got %v, opens=%d", err, opened)
			}
			var id int
			var text string
			if err := inspection.QueryRowContext(context.Background(), "SELECT id, text FROM items").Scan(&id, &text); err != nil {
				t.Fatal(err)
			}
			if id != 42 || text != "Synthetic preserved passage" {
				t.Fatalf("stored passage changed: %d, %q", id, text)
			}
			var catalogTables int
			if err := inspection.QueryRowContext(context.Background(), "SELECT count(*) FROM information_schema.tables WHERE table_name IN ('item_sources', 'jar_index_metadata')").Scan(&catalogTables); err != nil {
				t.Fatal(err)
			}
			wantTables := 0
			if metadata {
				wantTables = 1
				var value int
				if err := inspection.QueryRowContext(context.Background(), "SELECT unexpected FROM jar_index_metadata").Scan(&value); err != nil || value != 99 {
					t.Fatalf("metadata changed: %d, %v", value, err)
				}
			}
			if catalogTables != wantTables {
				t.Fatalf("skip created or repaired catalog tables: got %d, want %d", catalogTables, wantTables)
			}
		})
	}
}

func TestSkipIfEmptyContinuesNormalInputValidation(t *testing.T) {
	clearCommandSettings(t)
	for _, items := range []bool{false, true} {
		for _, validDocs := range []bool{false, true} {
			t.Run(fmt.Sprintf("itemsTable=%t/docsProvided=%t", items, validDocs), func(t *testing.T) {
				statements := ""
				if items {
					statements = "CREATE TABLE items (id INTEGER)"
				}
				db, inspection := commandTestDB(t, statements)
				args := []string{"-skip-if-populated"}
				want := "-docs is required"
				if validDocs {
					args = append(args, "-docs="+t.TempDir())
					want = "embedding-url is required"
				}
				opened := 0
				err := run(args, func(connection.Flags) (*sql.DB, error) {
					opened++
					return db, nil
				})
				if err == nil || !strings.Contains(err.Error(), want) || opened != 1 {
					t.Fatalf("empty index must continue validation: got %v, opens=%d; want %s", err, opened, want)
				}
				var tables int
				if err := inspection.QueryRowContext(context.Background(), "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'main'").Scan(&tables); err != nil {
					t.Fatal(err)
				}
				wantTables := 0
				if items {
					wantTables = 1
				}
				if tables != wantTables {
					t.Fatalf("validation modified database tables: got %d, want %d", tables, wantTables)
				}
			})
		}
	}
}

func TestSkipIfPopulatedPropagatesDatabaseErrors(t *testing.T) {
	clearCommandSettings(t)
	openFailure := errors.New("synthetic open failure")
	if err := run([]string{"-skip-if-populated"}, func(connection.Flags) (*sql.DB, error) {
		return nil, openFailure
	}); !errors.Is(err, openFailure) {
		t.Fatalf("database open failure was hidden: %v", err)
	}
	db, _ := commandTestDB(t, "DROP MACRO jar_remote.query")
	if err := run([]string{"-skip-if-populated"}, func(connection.Flags) (*sql.DB, error) {
		return db, nil
	}); err == nil || !strings.Contains(err.Error(), "check existing chunks") {
		t.Fatalf("chunk lookup failure should fail the command: %v", err)
	}
}

func TestIndexWithoutSkipValidatesBeforeOpeningDatabase(t *testing.T) {
	clearCommandSettings(t)
	for _, test := range []struct {
		args []string
		want string
	}{
		{nil, "-docs is required"},
		{[]string{"-docs=" + t.TempDir()}, "embedding-url is required"},
	} {
		err := run(test.args, func(connection.Flags) (*sql.DB, error) {
			t.Fatal("normal indexing opened the database before validating its inputs")
			return nil, nil
		})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("Run(%v): got %v; want %s", test.args, err, test.want)
		}
	}
}

func TestSkipIfPopulatedRejectsSpecialModesBeforeOpeningDatabase(t *testing.T) {
	clearCommandSettings(t)
	for _, mode := range []string{"-status", "-metadata", "-reset"} {
		err := run([]string{"-skip-if-populated", mode}, func(connection.Flags) (*sql.DB, error) {
			t.Fatal("incompatible modes opened the database")
			return nil, nil
		})
		if err == nil || !strings.Contains(err.Error(), "-skip-if-populated cannot be combined") {
			t.Fatalf("%s should conflict with skip-if-populated, got %v", mode, err)
		}
	}
}
