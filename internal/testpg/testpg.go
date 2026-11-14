// Package testpg gives integration tests an isolated, migrated PostgreSQL
// database.
//
// Each test binary builds one template database with every migration applied,
// and each test clones it into a database of its own: tests never share rows and
// never replay the migrations.
//
// The server is the one MAXOS_TEST_DATABASE_URL points at (see `make check`);
// its role must be allowed to create databases. Tests that ask for a database
// are skipped when the variable is unset. A test package that uses New must call
// Run from TestMain so the template is dropped when its tests finish.
package testpg

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maxjkfc/maxos/migrations"
	"github.com/maxjkfc/maxos/pkg/postgres"
)

// envURL names the variable that holds the URL of the PostgreSQL server to use.
// The helper connects to the database the URL names only to create and drop
// databases next to it.
const envURL = "MAXOS_TEST_DATABASE_URL"

var (
	templateOnce sync.Once
	templateName string // set as soon as the template exists, even if migrating it failed
	templateErr  error

	// cloneMu serialises CREATE DATABASE ... TEMPLATE, so that tests running in
	// parallel do not race on the template.
	cloneMu  sync.Mutex
	cloneSeq atomic.Int64
)

// New returns a pool on a database of its own: an empty clone of the migrated
// template, dropped when the test ends. It skips the test when
// MAXOS_TEST_DATABASE_URL is unset.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	adminURL := os.Getenv(envURL)
	if adminURL == "" {
		t.Skipf("%s is not set; skipping database test (see `make check`)", envURL)
	}
	ctx := context.Background()

	templateOnce.Do(func() { templateErr = buildTemplate(ctx, adminURL) })
	if templateErr != nil {
		t.Fatalf("prepare template database: %v", templateErr)
	}

	name := fmt.Sprintf("maxos_test_%d_%d", os.Getpid(), cloneSeq.Add(1))
	cloneMu.Lock()
	err := execAdmin(ctx, adminURL, "CREATE DATABASE "+quote(name)+" TEMPLATE "+quote(templateName))
	cloneMu.Unlock()
	if err != nil {
		t.Fatalf("clone template database: %v", err)
	}
	t.Cleanup(func() {
		if err := execAdmin(ctx, adminURL, dropSQL(name)); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})

	pool, err := openPool(ctx, adminURL, name)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close) // runs before the drop above
	return pool
}

// Run runs the tests of the calling package, then drops the template database
// built for them. Call it from TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(testpg.Run(m)) }
func Run(m *testing.M) int {
	code := m.Run()
	if templateName != "" {
		if err := execAdmin(context.Background(), os.Getenv(envURL), dropSQL(templateName)); err != nil {
			fmt.Fprintf(os.Stderr, "testpg: drop template database %s: %v\n", templateName, err)
		}
	}
	return code
}

// buildTemplate creates the template database and applies every migration to
// it.
func buildTemplate(ctx context.Context, adminURL string) error {
	name := fmt.Sprintf("maxos_test_template_%d", os.Getpid())
	// A crashed earlier run may have left a template with this name behind.
	if err := execAdmin(ctx, adminURL, dropSQL(name)); err != nil {
		return err
	}
	if err := execAdmin(ctx, adminURL, "CREATE DATABASE "+quote(name)); err != nil {
		return err
	}
	templateName = name

	pool, err := openPool(ctx, adminURL, name)
	if err != nil {
		return err
	}
	// Closing the pool before the first clone leaves nothing connected to the
	// template, which CREATE DATABASE ... TEMPLATE requires.
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool, migrations.FS); err != nil {
		return fmt.Errorf("migrate template database: %w", err)
	}
	return nil
}

// openPool connects to the database called name on the server adminURL points
// at.
func openPool(ctx context.Context, adminURL, name string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(adminURL)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.Database = name
	return pgxpool.NewWithConfig(ctx, cfg)
}

// execAdmin runs one statement on the database adminURL names. CREATE and DROP
// DATABASE cannot run inside the database they act on, so they go through here.
func execAdmin(ctx context.Context, adminURL, sql string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, sql)
	return err
}

func dropSQL(name string) string {
	return "DROP DATABASE IF EXISTS " + quote(name) + " WITH (FORCE)"
}

func quote(name string) string {
	return pgx.Identifier{name}.Sanitize()
}
