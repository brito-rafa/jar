package connection

import (
	"flag"
	"strings"
	"testing"
)

func TestDatabaseEnvironmentAndCLIOverrides(t *testing.T) {
	t.Setenv("DB_URL", "quack:database.test:9495")
	t.Setenv("DB_DISABLE_SSL", "true")
	var c Flags
	f := flag.NewFlagSet("test", flag.ContinueOnError)
	if err := c.Register(f); err != nil {
		t.Fatal(err)
	}
	if c.Endpoint != "quack:database.test:9495" || !c.DisableSSL {
		t.Fatalf("environment defaults: %+v", c)
	}
	if err := f.Parse([]string{"-db-url=quack:other.test:9496", "-db-disable-ssl=false"}); err != nil {
		t.Fatal(err)
	}
	if c.Endpoint != "quack:other.test:9496" || c.DisableSSL {
		t.Fatalf("explicit flags must override environment: %+v", c)
	}
}

func TestDatabaseConfigurationFailsBeforeConnecting(t *testing.T) {
	t.Setenv("DB_URL", "")
	t.Setenv("DB_DISABLE_SSL", "")
	t.Setenv("JAR_REMOTE_DB", "legacy.test:9494")
	var c Flags
	if err := c.Register(flag.NewFlagSet("test", flag.ContinueOnError)); err != nil {
		t.Fatal(err)
	}
	if c.DisableSSL {
		t.Fatal("remote database must default to TLS")
	}
	if _, err := c.Open(); err == nil || !strings.Contains(err.Error(), "db-url is required") {
		t.Fatalf("expected missing endpoint error, got %v", err)
	}
	t.Setenv("DB_DISABLE_SSL", "invalid")
	if err := c.Register(flag.NewFlagSet("test", flag.ContinueOnError)); err == nil || !strings.Contains(err.Error(), "DB_DISABLE_SSL") {
		t.Fatalf("expected invalid transport setting error, got %v", err)
	}
}
