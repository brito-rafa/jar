// Package connection configures the DuckDB service connection.
package connection

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/brito-rafa/jar/pkg/duck"
)

type Flags struct {
	Endpoint   string
	DisableSSL bool
}

func (c *Flags) Register(f *flag.FlagSet) error {
	disableSSL := false
	if value := os.Getenv("DB_DISABLE_SSL"); value != "" {
		var err error
		disableSSL, err = strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("DB_DISABLE_SSL must be true or false")
		}
	}
	f.StringVar(&c.Endpoint, "db-url", os.Getenv("DB_URL"), "DuckDB Quack endpoint (required); token from DB_TOKEN")
	f.BoolVar(&c.DisableSSL, "db-disable-ssl", disableSSL, "Use plain HTTP for DuckDB instead of its default transport")
	return nil
}

func (c Flags) Open() (*sql.DB, error) {
	if strings.TrimSpace(c.Endpoint) == "" {
		return nil, fmt.Errorf("db-url is required (or set DB_URL)")
	}
	return duck.OpenRemote(c.Endpoint, os.Getenv("DB_TOKEN"), c.DisableSSL)
}
