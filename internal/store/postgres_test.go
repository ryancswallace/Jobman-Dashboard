package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

func testDB(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("JOBMAN_DASHBOARD_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set JOBMAN_DASHBOARD_TEST_DATABASE_URL for isolated PostgreSQL integration")
	}
	ctx := context.Background()
	admin, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal("integration database unavailable")
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	schema := "dashboard_test_" + hex.EncodeToString(b[:])
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Pool.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	// The dedicated Lab migration role has a small connection ceiling. Two
	// pooled connections still exercise concurrent transactions; other goroutines
	// wait in pgx instead of exceeding that operator-enforced role limit.
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("cannot open isolated schema")
	}
	s := &Store{Pool: pool}
	t.Cleanup(func() {
		s.Close()
		_, err := admin.Pool.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE")
		admin.Close()
		if err != nil {
			t.Error("temporary schema cleanup failed")
		}
	})
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPostgresMigrationCursorAndPreferenceConcurrency(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal("migration replay:", err)
	}
	id, err := s.Create(ctx, []byte(`{"accountId":"one","queryHash":"test","buffer":[1,2,3]}`), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	data, v, err := s.Load(ctx, id)
	if err != nil || v != 1 || len(data) == 0 {
		t.Fatal("cursor load failed")
	}
	if err = s.Advance(ctx, id, v, []byte(`{"accountId":"one","queryHash":"test","buffer":[3]}`)); err != nil {
		t.Fatal(err)
	}
	if err = s.Advance(ctx, id, v, []byte(`{"accountId":"intruder","queryHash":"test"}`)); err == nil {
		t.Fatal("stale cursor update succeeded")
	}
	if err = s.Advance(ctx, id, v+1, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Load(ctx, id); err == nil {
		t.Fatal("completed cursor retained")
	}
	const account = "44444444-4444-4444-8444-444444444444"
	if _, err = s.Pool.Exec(ctx, "INSERT INTO dashboard_accounts(id,directory_id,display_name) VALUES($1,$1,'Synthetic Alice')", account); err != nil {
		t.Fatal(err)
	}
	p := api.DefaultPreferences()
	p.Timezone = "America/New_York"
	updated, err := s.UpdatePreferences(ctx, account, "1", p)
	if err != nil || updated.Revision != "2" {
		t.Fatalf("first preferences update: %#v %v", updated, err)
	}
	if _, err = s.UpdatePreferences(ctx, account, "1", p); err == nil {
		t.Fatal("stale preferences overwrote newer values")
	}
	p.Timezone = "not/a/zone"
	if _, err = s.UpdatePreferences(ctx, account, "2", p); err == nil {
		t.Fatal("invalid timezone accepted")
	}
}

func TestPostgresExpiredCursorCleanupAndMigrationIntegrity(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	// PostgreSQL is the expiry authority; a resumed synthetic VM may have a
	// different clock from the host until its time service synchronizes.
	var databaseNow time.Time
	if err := s.Pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	id, err := s.Create(ctx, []byte(`{"accountId":"one","queryHash":"test"}`), databaseNow.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Load(ctx, id); err == nil {
		t.Fatal("expired cursor accepted")
	}
	n, err := s.PruneCursors(ctx)
	if err != nil || n != 1 {
		t.Fatalf("cleanup: %d %v", n, err)
	}
	if _, err = s.Pool.Exec(ctx, "UPDATE dashboard_schema_migrations SET sha256='tampered'"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err == nil {
		t.Fatal("modified migration accepted")
	}
}

func TestPostgresPollingBoundAndVisitedPageRetention(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	expires := time.Now().Add(15 * time.Minute)
	data := []byte(`{"accountId":"one","queryHash":"jobs","initial":true}`)
	visited, err := s.Create(ctx, data, expires)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Advance(ctx, visited, 1, data); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, err = s.Create(ctx, data, expires); err != nil {
			t.Fatal(err)
		}
	}
	var records int
	if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM dashboard_browse_sessions").Scan(&records); err != nil {
		t.Fatal(err)
	}
	if records != 4 {
		t.Fatalf("polling retained %d rows, expected one visited and three unused", records)
	}
	if _, version, err := s.Load(ctx, visited); err != nil || version != 2 {
		t.Fatalf("visited page lost: %d %v", version, err)
	}
}
