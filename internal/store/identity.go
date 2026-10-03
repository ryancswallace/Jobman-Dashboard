package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}
func (s *Store) ResolveIdentity(ctx context.Context, identity auth.Identity) (monitoring.Actor, error) {
	if identity.Issuer == "" || len(identity.Issuer) > 512 || identity.Subject == "" || len(identity.Subject) > 512 || len(identity.DirectoryID) != 36 || len(identity.DisplayName) > 256 {
		return monitoring.Actor{}, auth.ErrUnauthenticated
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return monitoring.Actor{}, err
	}
	defer tx.Rollback(ctx)
	// Serialize only the short identity mapping write, never a network request.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(72108003003)"); err != nil {
		return monitoring.Actor{}, err
	}
	var accountID, existingDirectoryID string
	err = tx.QueryRow(ctx, `SELECT a.id::text,a.directory_id::text FROM dashboard_identity_aliases i JOIN dashboard_accounts a ON a.id=i.account_id WHERE i.issuer=$1 AND i.subject=$2`, identity.Issuer, identity.Subject).Scan(&accountID, &existingDirectoryID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return monitoring.Actor{}, err
	}
	if err == nil && existingDirectoryID != identity.DirectoryID {
		return monitoring.Actor{}, auth.ErrIdentityConflict
	}
	var actor monitoring.Actor
	var disabled *time.Time
	err = tx.QueryRow(ctx, `SELECT id::text,display_name,disabled_at FROM dashboard_accounts WHERE directory_id=$1::uuid`, identity.DirectoryID).Scan(&actor.Account.ID, &actor.Account.DisplayName, &disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		id, err := newID()
		if err != nil {
			return actor, err
		}
		actor.Account.ID = id
		actor.Account.DisplayName = identity.DisplayName
		if _, err := tx.Exec(ctx, `INSERT INTO dashboard_accounts(id,directory_id,display_name) VALUES($1::uuid,$2::uuid,$3)`, id, identity.DirectoryID, identity.DisplayName); err != nil {
			return actor, err
		}
	} else if err != nil {
		return actor, err
	} else if disabled != nil {
		return actor, auth.ErrUnauthenticated
	}
	if accountID == "" {
		if _, err := tx.Exec(ctx, `INSERT INTO dashboard_identity_aliases(issuer,subject,account_id) VALUES($1,$2,$3::uuid)`, identity.Issuer, identity.Subject, actor.Account.ID); err != nil {
			return actor, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO dashboard_audit(account_id,action,resource_kind) VALUES($1::uuid,'identity.alias.verified','account')`, actor.Account.ID); err != nil {
			return actor, err
		}
	}
	if actor.Account.DisplayName != identity.DisplayName {
		if _, err := tx.Exec(ctx, `UPDATE dashboard_accounts SET display_name=$2 WHERE id=$1::uuid`, actor.Account.ID, identity.DisplayName); err != nil {
			return actor, err
		}
		actor.Account.DisplayName = identity.DisplayName
	}
	actor.Issuer = identity.Issuer
	actor.Subject = identity.Subject
	actor.DirectoryID = identity.DirectoryID
	if err := tx.Commit(ctx); err != nil {
		return monitoring.Actor{}, err
	}
	return actor, nil
}

func (s *Store) PutLogin(ctx context.Context, hash, data []byte, expires time.Time) error {
	if len(hash) != 32 || len(data) < 1 || len(data) > 4096 {
		return errors.New("invalid login state")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(72108003004)"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM dashboard_login_attempts WHERE state_hash IN (SELECT state_hash FROM dashboard_login_attempts WHERE expires_at<=clock_timestamp() LIMIT 500)`); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM dashboard_login_attempts`).Scan(&count); err != nil {
		return err
	}
	if count >= 1000 {
		return errors.New("login capacity temporarily unavailable")
	}
	tag, err := tx.Exec(ctx, `INSERT INTO dashboard_login_attempts(state_hash,encrypted_payload,expires_at) SELECT $1,$2,$3 WHERE $3>clock_timestamp() AND $3<=clock_timestamp()+interval '5 minutes 10 seconds'`, hash, data, expires)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("login expiry is outside the allowed lifetime")
	}
	return tx.Commit(ctx)
}
func (s *Store) ConsumeLogin(ctx context.Context, hash []byte) ([]byte, error) {
	var data []byte
	err := s.Pool.QueryRow(ctx, `DELETE FROM dashboard_login_attempts WHERE state_hash=$1 RETURNING CASE WHEN expires_at>clock_timestamp() THEN encrypted_payload ELSE NULL END`, hash).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && data == nil) {
		return nil, auth.ErrUnauthenticated
	}
	return data, err
}
func (s *Store) CreateSession(ctx context.Context, session auth.Session) error {
	if len(session.TokenHash) != 32 || len(session.CSRFHash) != 32 || session.Actor.Issuer == "" || session.Actor.Subject == "" || !session.ExpiresAt.After(session.CreatedAt) || session.ExpiresAt.Sub(session.CreatedAt) > 8*time.Hour {
		return errors.New("invalid session")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(72108003005)"); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM dashboard_sessions WHERE revoked_at IS NULL AND expires_at>clock_timestamp() AND touched_at>clock_timestamp()-interval '30 minutes'`).Scan(&count); err != nil {
		return err
	}
	if count >= 10000 {
		return errors.New("web session capacity temporarily unavailable")
	}
	tag, err := tx.Exec(ctx, `WITH created AS (INSERT INTO dashboard_sessions(token_hash,account_id,csrf_hash,created_at,touched_at,expires_at,issuer,subject)
 SELECT $1,a.id,$3,clock_timestamp(),clock_timestamp(),$4,$5,$6 FROM dashboard_accounts a
 JOIN dashboard_identity_aliases i ON i.account_id=a.id AND i.issuer=$5 AND i.subject=$6
 WHERE a.id=$2::uuid AND a.disabled_at IS NULL AND $4>clock_timestamp() AND $4<=clock_timestamp()+interval '8 hours 10 seconds' RETURNING account_id)
 INSERT INTO dashboard_audit(account_id,action,resource_kind) SELECT account_id,'session.created','session' FROM created`, session.TokenHash, session.Actor.Account.ID, session.CSRFHash, session.ExpiresAt, session.Actor.Issuer, session.Actor.Subject)
	if err == nil && tag.RowsAffected() != 1 {
		return auth.ErrUnauthenticated
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `WITH evicted AS (UPDATE dashboard_sessions SET revoked_at=clock_timestamp() WHERE token_hash IN
 (SELECT token_hash FROM dashboard_sessions WHERE account_id=$1::uuid AND revoked_at IS NULL ORDER BY created_at DESC,token_hash LIMIT 10000 OFFSET 20) RETURNING account_id)
 INSERT INTO dashboard_audit(account_id,action,resource_kind) SELECT account_id,'session.capacity_revoked','session' FROM evicted`, session.Actor.Account.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Session(ctx context.Context, hash []byte) (auth.Session, error) {
	var result auth.Session
	err := s.Pool.QueryRow(ctx, `UPDATE dashboard_sessions session SET touched_at=clock_timestamp() FROM dashboard_accounts account
 WHERE session.token_hash=$1 AND session.account_id=account.id AND account.disabled_at IS NULL AND session.revoked_at IS NULL
 AND session.expires_at>clock_timestamp() AND session.touched_at>clock_timestamp()-interval '30 minutes' AND session.issuer<>'' AND session.subject<>''
 RETURNING session.token_hash,session.csrf_hash,session.created_at,session.expires_at,session.issuer,session.subject,account.id::text,account.directory_id::text,account.display_name`, hash).Scan(&result.TokenHash, &result.CSRFHash, &result.CreatedAt, &result.ExpiresAt, &result.Actor.Issuer, &result.Actor.Subject, &result.Actor.Account.ID, &result.Actor.DirectoryID, &result.Actor.Account.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, auth.ErrUnauthenticated
	}
	return result, err
}
func (s *Store) RevokeSession(ctx context.Context, hash []byte) error {
	_, err := s.Pool.Exec(ctx, `WITH revoked AS (UPDATE dashboard_sessions SET revoked_at=clock_timestamp() WHERE token_hash=$1 AND revoked_at IS NULL RETURNING account_id)
 INSERT INTO dashboard_audit(account_id,action,resource_kind) SELECT account_id,'session.revoked','session' FROM revoked`, hash)
	return err
}

// PruneAuthentication bounds each transaction and cooperates across API
// processes. Audit retention is separate from short-lived credential records.
func (s *Store) PruneAuthentication(ctx context.Context) error {
	for _, query := range []string{
		`DELETE FROM dashboard_login_attempts WHERE state_hash IN (SELECT state_hash FROM dashboard_login_attempts WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 500 FOR UPDATE SKIP LOCKED)`,
		`DELETE FROM dashboard_sessions WHERE token_hash IN (SELECT token_hash FROM dashboard_sessions WHERE revoked_at IS NOT NULL OR expires_at<=clock_timestamp() OR touched_at<=clock_timestamp()-interval '30 minutes' ORDER BY expires_at LIMIT 500 FOR UPDATE SKIP LOCKED)`,
		`DELETE FROM dashboard_audit WHERE id IN (SELECT id FROM dashboard_audit WHERE recorded_at<clock_timestamp()-interval '90 days' ORDER BY recorded_at LIMIT 500 FOR UPDATE SKIP LOCKED)`,
	} {
		if _, err := s.Pool.Exec(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

// VerifySourceIdentity prevents a deployment UUID from being reassigned to a
// replacement Control. Recovery epochs may advance without changing identity.
func (s *Store) VerifySourceIdentity(ctx context.Context, deployment, instance, epoch string, revision int64) error {
	number, parseErr := strconv.ParseInt(epoch, 10, 64)
	if parseErr != nil || number < 1 || strconv.FormatInt(number, 10) != epoch || revision < 1 {
		return fmt.Errorf("invalid source identity registration")
	}
	tag, err := s.Pool.Exec(ctx, `INSERT INTO dashboard_source_identities(deployment_id,control_instance_id,recovery_epoch,configuration_revision,verified_at)
 VALUES($1::uuid,$2::uuid,$3,$4,clock_timestamp()) ON CONFLICT(deployment_id) DO UPDATE SET recovery_epoch=excluded.recovery_epoch,configuration_revision=excluded.configuration_revision,verified_at=excluded.verified_at
	 WHERE dashboard_source_identities.control_instance_id=excluded.control_instance_id AND dashboard_source_identities.configuration_revision<=excluded.configuration_revision
	 AND dashboard_source_identities.recovery_epoch::bigint<=excluded.recovery_epoch::bigint`, deployment, instance, epoch, revision)
	if err == nil && tag.RowsAffected() != 1 {
		return errors.New("deployment identity cannot be reassigned or configuration/recovery epoch rolled backward")
	}
	return err
}
