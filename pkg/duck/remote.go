package duck

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// OpenRemote creates an in-memory client attached to the Quack server.
// One session is retained for the lifetime of a command.
func OpenRemote(endpoint, token string, disableSSL ...bool) (*sql.DB, error) {
	if !strings.HasPrefix(endpoint, "quack:") {
		return nil, fmt.Errorf("db-url must use the quack: scheme")
	}
	if len(token) < 4 {
		return nil, fmt.Errorf("set JAR_DB_TOKEN to the token configured for the DuckDB container")
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, "INSTALL quack; LOAD quack;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("load Quack client: %w", err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	options := "TOKEN " + quote(token)
	if len(disableSSL) > 0 && disableSSL[0] {
		options += ", DISABLE_SSL true"
	}
	attach := "ATTACH " + quote(endpoint) + " AS jar_remote (" + options + ")"
	if _, err := db.ExecContext(ctx, attach); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to DuckDB endpoint %s: %s", endpoint, strings.ReplaceAll(strings.ReplaceAll(err.Error(), quote(token), "[redacted]"), token, "[redacted]"))
	}

	return db, nil
}

type DatabaseStatus struct {
	Ready          bool
	Chunks         int
	Fingerprint    string
	EmbeddingModel string
	Dimensions     int
	IndexedAt      string
}

// Status reads metadata without creating tables or loading a model.
func Status(db *sql.DB) (DatabaseStatus, error) {
	var s DatabaseStatus
	var tables int
	if err := remoteRow(db, "SELECT count(*) FROM information_schema.tables WHERE table_name IN ('items', 'jar_index_metadata')").Scan(&tables); err != nil {
		return s, err
	}
	if tables != 2 {
		return s, nil
	}
	err := remoteRow(db, "SELECT fingerprint, embedding_model, dimensions, CAST(indexed_at AS VARCHAR) FROM jar_index_metadata LIMIT 1").Scan(&s.Fingerprint, &s.EmbeddingModel, &s.Dimensions, &s.IndexedAt)
	if err == sql.ErrNoRows {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := remoteRow(db, "SELECT count(*) FROM items").Scan(&s.Chunks); err != nil {
		return s, err
	}
	s.Ready = s.EmbeddingModel != "" && s.Dimensions > 0
	return s, nil
}
