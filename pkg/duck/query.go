package duck

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Quack 2.0 CONNECT currently fails through the Go prepared-statement API.
// Execute SQL through the attached query macro instead. The outer query uses a
// bound parameter. Inner values use a restricted encoder, never raw document SQL.
func bindRemote(query string, args ...any) (string, error) {
	for _, arg := range args {
		i := strings.IndexByte(query, '?')
		if i < 0 {
			return "", fmt.Errorf("too many query parameters")
		}
		value, err := remoteLiteral(arg)
		if err != nil {
			return "", err
		}
		query = query[:i] + value + query[i+1:]
	}
	if strings.Contains(query, "?") {
		return "", fmt.Errorf("missing query parameter")
	}
	return query, nil
}

func remoteLiteral(value any) (string, error) {
	switch v := value.(type) {
	case string:
		// Base64 keeps quotes, NULs, and SQL syntax out of the SQL literal.
		return "decode(from_base64('" + base64.StdEncoding.EncodeToString([]byte(v)) + "'))", nil
	case int:
		return strconv.Itoa(v), nil
	case []float32:
		if len(v) == 0 {
			return "", fmt.Errorf("empty embedding vector")
		}
		var b strings.Builder
		b.WriteByte('[')
		for i, n := range v {
			if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) {
				return "", fmt.Errorf("non-finite embedding value")
			}
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.FormatFloat(float64(n), 'g', -1, 32))
		}
		fmt.Fprintf(&b, "]::FLOAT[%d]", len(v))
		return b.String(), nil
	default:
		return "", fmt.Errorf("unsupported query parameter type %T", value)
	}
}

func remoteExec(db *sql.DB, query string, args ...any) error {
	bound, err := bindRemote(query, args...)
	if err != nil {
		return err
	}
	_, err = db.Exec("FROM jar_remote.query(?)", bound)
	return err
}
func remoteQuery(db *sql.DB, query string, args ...any) (*sql.Rows, error) {
	bound, err := bindRemote(query, args...)
	if err != nil {
		return nil, err
	}
	return db.Query("FROM jar_remote.query(?)", bound)
}
func remoteRow(db *sql.DB, query string) *sql.Row {
	return db.QueryRow("FROM jar_remote.query(?)", query)
}
