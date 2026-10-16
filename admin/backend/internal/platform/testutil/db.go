// Package testutil provides real-PostgreSQL test databases and fixtures.
//
// Each call to NewDB clones a fresh database from a template that has all
// migrations applied, so tests are isolated and fast. Set TEST_DATABASE_URL to a
// superuser-capable connection string (default: the local dev container on 55432).
package testutil

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/migrations"
)

const defaultURL = "postgres://jantaxi:jantaxi@localhost:55432/postgres?sslmode=disable"

var (
	templateOnce sync.Once
	templateName string
	templateErr  error
)

// AdminURL is the maintenance connection string used to create databases.
func AdminURL() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultURL
}

// NewDB returns a pool for a brand-new migrated database that is dropped when the
// test finishes. Tests fail (not skip) when PostgreSQL is unreachable, unless
// JT_SKIP_DB_TESTS=1 is set.
func NewDB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("JT_SKIP_DB_TESTS") == "1" {
		t.Skip("JT_SKIP_DB_TESTS=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	templateOnce.Do(func() { templateName, templateErr = ensureTemplate(ctx) })
	if templateErr != nil {
		t.Fatalf("test database template: %v (is PostGIS running? see TEST_DATABASE_URL)", templateErr)
	}

	admin, err := pgx.Connect(ctx, AdminURL())
	if err != nil {
		t.Fatalf("connect admin db: %v", err)
	}
	defer admin.Close(ctx)

	name := "jt_test_" + randomHex(6)
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s TEMPLATE %s`, name, templateName)); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	pool, err := db.Open(ctx, withDatabase(AdminURL(), name), 20)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, err := pgx.Connect(context.Background(), AdminURL())
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name))
	})
	return pool
}

// ensureTemplate creates (once per migration set) a template database with all
// migrations applied. Concurrent test binaries coordinate with an advisory lock.
func ensureTemplate(ctx context.Context) (string, error) {
	sum, err := migrationsHash()
	if err != nil {
		return "", err
	}
	name := "jt_tpl_" + sum[:12]

	admin, err := pgx.Connect(ctx, AdminURL())
	if err != nil {
		return "", err
	}
	defer admin.Close(ctx)

	if _, err := admin.Exec(ctx, `SELECT pg_advisory_lock(424242)`); err != nil {
		return "", err
	}
	defer func() { _, _ = admin.Exec(context.Background(), `SELECT pg_advisory_unlock(424242)`) }()

	var exists bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1 AND datistemplate)`, name).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return name, nil
	}

	_, _ = admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name))
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, name)); err != nil {
		return "", err
	}
	pool, err := db.Open(ctx, withDatabase(AdminURL(), name), 2)
	if err != nil {
		return "", err
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return "", fmt.Errorf("migrate template: %w", err)
	}
	pool.Close()
	if _, err := admin.Exec(ctx, fmt.Sprintf(`ALTER DATABASE %s WITH is_template true ALLOW_CONNECTIONS false`, name)); err != nil {
		return "", err
	}
	return name, nil
}

func migrationsHash() (string, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := fs.ReadFile(migrations.FS, n)
		if err != nil {
			return "", err
		}
		h.Write([]byte(n))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func withDatabase(raw, name string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Path = "/" + strings.TrimPrefix(name, "/")
	return u.String()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
