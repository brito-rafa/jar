package connection

import (
	"database/sql"
	"flag"
	"github.com/brito-rafa/jar/pkg/duck"
	"os"
)

type Flags struct{ Endpoint string }

func (c *Flags) Register(f *flag.FlagSet) {
	f.StringVar(&c.Endpoint, "db-url", "quack:127.0.0.1:9494", "DuckDB Quack Endpoint over TCP; token from JAR_DB_TOKEN")
}
func (c Flags) Open() (*sql.DB, error) {
	return duck.OpenRemote(c.Endpoint, os.Getenv("JAR_DB_TOKEN"))
}
