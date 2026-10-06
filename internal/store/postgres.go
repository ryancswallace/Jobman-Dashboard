// Package store owns Dashboard state only. It never connects to a Control database.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("invalid Dashboard database configuration")
	}
	cfg.MaxConns = 16
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("Dashboard database connection failed")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("Dashboard database is unavailable")
	}
	return &Store{Pool: pool}, nil
}
func (s *Store) Close() { s.Pool.Close() }

// CheckSchema lets a runtime identity verify the migration ledger without any
// DDL authority. Newer or modified schemas require an explicit operator upgrade.
// ErrSchemaMismatch identifies a completed ledger comparison, not an outage.
var ErrSchemaMismatch = errors.New("Dashboard schema is incompatible with this binary")

func (s *Store) CheckSchema(ctx context.Context) error {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(ctx, "SELECT name,sha256 FROM dashboard_schema_migrations")
	if err != nil {
		return fmt.Errorf("Dashboard schema is unavailable; run the explicit migration command")
	}
	defer rows.Close()
	actual := map[string]string{}
	for rows.Next() {
		var name, sum string
		if err := rows.Scan(&name, &sum); err != nil {
			return err
		}
		actual[name] = sum
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(actual) != len(names) {
		return fmt.Errorf("%w: migration version", ErrSchemaMismatch)
	}
	for _, name := range names {
		data, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(data)
		if actual[name] != hex.EncodeToString(hash[:]) {
			return fmt.Errorf("%w: migration integrity", ErrSchemaMismatch)
		}
	}
	return nil
}

// Migrate is invoked explicitly with the migration identity, never implicitly by
// API/worker startup. Checksums prevent silently editing historical migrations.
func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(72108003001)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS dashboard_schema_migrations (name text PRIMARY KEY, sha256 text NOT NULL, applied_at timestamptz NOT NULL DEFAULT clock_timestamp())`); err != nil {
		return err
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	slices.Sort(names)
	rows, err := tx.Query(ctx, "SELECT name FROM dashboard_schema_migrations")
	if err != nil {
		return err
	}
	unknown := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		if !slices.Contains(names, name) {
			unknown = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if unknown {
		return fmt.Errorf("database contains a newer or unknown Dashboard migration; use a compatible binary")
	}
	for _, name := range names {
		sql, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(sql)
		sum := hex.EncodeToString(hash[:])
		var old string
		err = tx.QueryRow(ctx, "SELECT sha256 FROM dashboard_schema_migrations WHERE name=$1", name).Scan(&old)
		if err == nil {
			if old != sum {
				return fmt.Errorf("migration checksum mismatch: %s", name)
			}
			continue
		}
		if err != pgx.ErrNoRows {
			return err
		}
		if _, err = tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s failed: %w", name, err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO dashboard_schema_migrations(name,sha256) VALUES($1,$2)", name, sum); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) Create(ctx context.Context, data []byte, expires time.Time) (string, error) {
	identity, err := monitoring.DecodeCursorIdentity(data)
	if err != nil {
		return "", err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(72108003002)"); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM dashboard_browse_sessions WHERE id IN (SELECT id FROM dashboard_browse_sessions WHERE expires_at<=clock_timestamp() LIMIT 500)`); err != nil {
		return "", err
	}
	if identity.Initial {
		if _, err = tx.Exec(ctx, `DELETE FROM dashboard_browse_sessions WHERE id IN (SELECT id FROM dashboard_browse_sessions WHERE account_id=$1 AND query_hash=$2 AND initial_page AND version=1 ORDER BY created_at DESC,id DESC OFFSET 2)`, identity.AccountID, identity.QueryHash); err != nil {
			return "", err
		}
	}
	if err = cursorBudget(ctx, tx, identity.AccountID, "", len(data)); err != nil {
		return "", err
	}
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(raw[:])
	_, err = tx.Exec(ctx, `INSERT INTO dashboard_browse_sessions(id,payload,expires_at,account_id,query_hash,initial_page) VALUES($1,$2,$3,$4,$5,$6)`, id, data, expires, identity.AccountID, identity.QueryHash, identity.Initial)
	if err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}
func (s *Store) Load(ctx context.Context, id string) ([]byte, uint64, error) {
	var data []byte
	var version uint64
	err := s.Pool.QueryRow(ctx, "SELECT payload,version FROM dashboard_browse_sessions WHERE id=$1 AND expires_at>clock_timestamp()", id).Scan(&data, &version)
	if err == pgx.ErrNoRows {
		return nil, 0, monitoring.ErrCursor
	}
	return data, version, err
}
func (s *Store) Advance(ctx context.Context, id string, version uint64, data []byte) error {
	if len(data) == 0 {
		tag, err := s.Pool.Exec(ctx, "DELETE FROM dashboard_browse_sessions WHERE id=$1 AND version=$2 AND expires_at>clock_timestamp()", id, version)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return monitoring.ErrCursor
		}
		return nil
	}
	identity, err := monitoring.DecodeCursorIdentity(data)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(72108003002)"); err != nil {
		return err
	}
	if err = cursorBudget(ctx, tx, identity.AccountID, id, len(data)); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE dashboard_browse_sessions SET payload=$3,version=version+1 WHERE id=$1 AND version=$2 AND account_id=$4 AND query_hash=$5 AND expires_at>clock_timestamp()`, id, version, data, identity.AccountID, identity.QueryHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return monitoring.ErrCursor
	}
	return tx.Commit(ctx)
}
func cursorBudget(ctx context.Context, tx pgx.Tx, account, except string, newBytes int) error {
	var total, own, count, ownCount int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(octet_length(payload)),0),COALESCE(sum(octet_length(payload)) FILTER(WHERE account_id=$1),0),count(*),count(*) FILTER(WHERE account_id=$1) FROM dashboard_browse_sessions WHERE id<>$2`, account, except).Scan(&total, &own, &count, &ownCount)
	if err != nil {
		return err
	}
	if total+int64(newBytes) > 512<<20 || own+int64(newBytes) > 64<<20 || count >= 10000 || ownCount >= 1000 {
		return monitoring.ErrCursorQuota
	}
	return nil
}

func (s *Store) Preferences(ctx context.Context, accountID string) (api.Preferences, error) {
	p := api.DefaultPreferences()
	err := s.Pool.QueryRow(ctx, "SELECT revision::text,timezone,appearance,refresh_seconds FROM dashboard_preferences WHERE account_id=$1", accountID).Scan(&p.Revision, &p.Timezone, &p.Appearance, &p.RefreshSeconds)
	if err == pgx.ErrNoRows {
		return p, nil
	}
	return p, err
}
func (s *Store) UpdatePreferences(ctx context.Context, accountID, revision string, p api.Preferences) (api.Preferences, error) {
	if _, err := time.LoadLocation(p.Timezone); err != nil || p.Timezone == "" || p.Timezone == "Local" || len(p.Timezone) > 128 || !slices.Contains([]string{"system", "light", "dark"}, p.Appearance) || !slices.Contains([]int{0, 5, 10, 30}, p.RefreshSeconds) {
		return api.Preferences{}, &api.Error{Code: "invalid_settings", Message: "Choose a valid timezone, appearance, and refresh interval."}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return p, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "INSERT INTO dashboard_preferences(account_id) VALUES($1) ON CONFLICT DO NOTHING", accountID); err != nil {
		return p, err
	}
	err = tx.QueryRow(ctx, `UPDATE dashboard_preferences SET revision=revision+1,timezone=$3,appearance=$4,refresh_seconds=$5,updated_at=clock_timestamp() WHERE account_id=$1 AND revision::text=$2 RETURNING revision::text`, accountID, revision, p.Timezone, p.Appearance, p.RefreshSeconds).Scan(&p.Revision)
	if err == pgx.ErrNoRows {
		return p, &api.Error{Code: "revision_conflict", Message: "Your settings changed in another browser or device. Reload settings and try again."}
	}
	if err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}

// PruneCursors bounds each cleanup transaction and lets multiple workers safely
// claim disjoint rows. Other retention families have independent policies.
func (s *Store) PruneCursors(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM dashboard_browse_sessions WHERE id IN (SELECT id FROM dashboard_browse_sessions WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 500 FOR UPDATE SKIP LOCKED)`)
	return tag.RowsAffected(), err
}
