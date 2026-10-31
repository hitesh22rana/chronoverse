package main

import (
	"net"
	"net/url"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/hitesh22rana/chronoverse/internal/config"
)

func TestPostgresMigrationDSN(t *testing.T) {
	for _, host := range []string{"localhost", "2001:db8::1"} {
		for _, tlsMode := range []string{"disabled", "enabled", "certificates"} {
			t.Run(host+"/"+tlsMode, func(t *testing.T) {
				cfg := config.Postgres{Host: host, Port: 5432, User: "migration user+@:/", Password: "secret +@:/?#%", Database: "database name"}
				cfg.TLS.Enabled = tlsMode != "disabled"
				if tlsMode == "certificates" {
					cfg.TLS.CAFile = "/certs/root +&?.pem"
					cfg.TLS.CertFile = "/certs/client +&?.pem"
					cfg.TLS.KeyFile = "/certs/client +&?.key"
				}
				got, err := url.Parse(postgresMigrationDSN(cfg))
				if err != nil {
					t.Fatal(err)
				}
				assertPostgresDSNAddress(t, cfg, got)
				assertPostgresDSNTLS(t, cfg, got.Query())
			})
		}
	}
}

func assertPostgresDSNAddress(t *testing.T, cfg config.Postgres, got *url.URL) {
	t.Helper()
	password, hasPassword := got.User.Password()
	if got.Scheme != "postgresql" || got.User.Username() != cfg.User || !hasPassword || password != cfg.Password {
		t.Fatalf("credential round trip failed: scheme %q, username %q, password present %t", got.Scheme, got.User.Username(), hasPassword)
	}
	if got.Host != net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)) || got.Path != "/"+cfg.Database {
		t.Fatalf("database address changed: %s", got.Redacted())
	}
	if !cfg.TLS.Enabled {
		driverConfig, err := pgx.ParseConfig(got.String())
		if err != nil {
			t.Fatal(err)
		}
		if driverConfig.User != cfg.User || driverConfig.Password != cfg.Password || driverConfig.Database != cfg.Database || driverConfig.Host != cfg.Host {
			t.Fatal("PostgreSQL driver did not preserve database credentials and address")
		}
	}
}

func assertPostgresDSNTLS(t *testing.T, cfg config.Postgres, query url.Values) {
	t.Helper()
	wantMode := "disable"
	if cfg.TLS.Enabled {
		wantMode = "verify-full"
	}
	if query.Get("sslmode") != wantMode {
		t.Fatalf("sslmode = %q, want %q", query.Get("sslmode"), wantMode)
	}
	for key, want := range map[string]string{"sslrootcert": cfg.TLS.CAFile, "sslcert": cfg.TLS.CertFile, "sslkey": cfg.TLS.KeyFile} {
		if query.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, query.Get(key), want)
		}
		if want == "" && query.Has(key) {
			t.Errorf("empty %s must be omitted", key)
		}
	}
}
