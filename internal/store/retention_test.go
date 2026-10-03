package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/auth"
)

func TestSessionCapacityAuditAndBoundedRetention(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	if err := s.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := s.ResolveIdentity(ctx, auth.Identity{Issuer: "https://synthetic.example", Subject: "alice", DirectoryID: "44444444-4444-4444-8444-444444444444", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err := s.Pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	csrf := sha256.Sum256([]byte("synthetic-csrf"))
	for i := 0; i < 21; i++ {
		hash := sha256.Sum256([]byte(fmt.Sprintf("synthetic-cookie-%d", i)))
		if err := s.CreateSession(ctx, auth.Session{TokenHash: hash[:], CSRFHash: csrf[:], Actor: a, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	var active, created, revoked int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_sessions WHERE revoked_at IS NULL`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE action='session.created'),count(*) FILTER(WHERE action='session.capacity_revoked') FROM dashboard_audit`).Scan(&created, &revoked); err != nil {
		t.Fatal(err)
	}
	if active != 20 || created != 21 || revoked != 1 {
		t.Fatalf("capacity/audit: %d %d %d", active, created, revoked)
	}
	hash := sha256.Sum256([]byte("synthetic-cookie-20"))
	if err := s.RevokeSession(ctx, hash[:]); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeSession(ctx, hash[:]); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_audit WHERE action='session.revoked'`).Scan(&revoked); err != nil || revoked != 1 {
		t.Fatalf("logout was not audited exactly once: %d %v", revoked, err)
	}
	if err := s.PruneAuthentication(ctx); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_sessions`).Scan(&rows); err != nil || rows != 19 {
		t.Fatalf("expired credential retention: %d %v", rows, err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_audit SET recorded_at=clock_timestamp()-interval '91 days'`); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneAuthentication(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_audit`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("audit retention: %d %v", rows, err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO dashboard_schema_migrations(name,sha256) VALUES('future-migration','unrecognized')`); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckSchema(ctx); err == nil {
		t.Fatal("runtime accepted newer schema")
	}
}
