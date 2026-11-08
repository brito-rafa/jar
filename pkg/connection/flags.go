package connection

import (
	"database/sql"
	"flag"
	"fmt"
	"github.com/brito-rafa/jar/pkg/duck"
	"os"
	"strconv"
	"strings"
)

type Flags struct {
	Endpoint   string
	DisableSSL bool
}

func (c *Flags) Register(f *flag.FlagSet) {
	endpoint := "quack:127.0.0.1:9494"
	if remote := os.Getenv("JAR_REMOTE_DB"); remote != "" {
		endpoint = "quack:" + strings.TrimPrefix(remote, "quack:")
	}
	f.StringVar(&c.Endpoint, "db-url", endpoint, "DuckDB Quack Endpoint over TCP; token from JAR_DB_TOKEN")
	f.BoolVar(&c.DisableSSL, "db-disable-ssl", false, "Use plain HTTP for DuckDB instead of its default transport")
}
func (c Flags) Open() (*sql.DB, error) {
	token := os.Getenv("JAR_DB_TOKEN")
	remote := os.Getenv("JAR_REMOTE_DB")
	if remote != "" && c.Endpoint == "quack:"+strings.TrimPrefix(remote, "quack:") {
		token = os.Getenv("JAR_REMOTE_DB_TOKEN")
		if value := os.Getenv("JAR_REMOTE_DB_DISABLE_SSL"); value != "" {
			disable, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf("remote-db-disable-ssl must be true or false")
			}
			c.DisableSSL = c.DisableSSL || disable
		}
	}
	return duck.OpenRemote(c.Endpoint, token, c.DisableSSL)
}
