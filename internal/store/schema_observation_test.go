package store

import (
	"context"
	"errors"
	"testing"
)

func TestSchemaMismatchDistinguishesOutageAndLedgerIntegrity(t *testing.T) {
	s := testDB(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.CheckSchema(ctx); err == nil || errors.Is(err, ErrSchemaMismatch) {
		t.Fatal("outage classified as schema mismatch", err)
	}
	deviceExec(t, s, `UPDATE dashboard_schema_migrations SET sha256='changed' WHERE name=(SELECT min(name) FROM dashboard_schema_migrations)`)
	if err := s.CheckSchema(t.Context()); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatal("ledger checksum mismatch not typed", err)
	}
	deviceExec(t, s, `DELETE FROM dashboard_schema_migrations WHERE name=(SELECT min(name) FROM dashboard_schema_migrations)`)
	if err := s.CheckSchema(t.Context()); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatal("ledger version mismatch not typed", err)
	}
}
