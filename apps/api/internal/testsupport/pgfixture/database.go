// Package pgfixture creates disposable databases for persistence tests.
package pgfixture

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// DatabaseURL requires a dedicated test server with CREATEDB privileges.
// It never clears or migrates the database named in TEST_DATABASE_URL.
func DatabaseURL(t testing.TB) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") || !strings.Contains(strings.ToLower(u.Path), "test") {
		t.Fatal("TEST_DATABASE_URL must be a postgres URL naming a dedicated test database")
	}
	admin, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	var suffix [12]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	name := "agentflow_fixture_" + hex.EncodeToString(suffix[:])
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err = admin.ExecContext(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, `DROP DATABASE "`+name+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop fixture database: %v", err)
		}
	})
	u.Path = "/" + name
	return u.String()
}
