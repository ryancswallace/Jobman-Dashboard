package store

import (
	"context"
	"crypto/hmac"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

// Device persistence owns only local cryptography/SQL. No provider request or
// external authorization lookup occurs while a database transaction is open.
type NotificationDeviceStore struct {
	store  *Store
	policy *notifications.DevicePolicy
	cipher *notifications.DeviceCipher
}

func NewNotificationDeviceStore(s *Store, p *notifications.DevicePolicy, c *notifications.DeviceCipher) (*NotificationDeviceStore, error) {
	if s == nil || s.Pool == nil || p == nil || c == nil {
		return nil, notifications.ErrDeviceInvalid
	}
	return &NotificationDeviceStore{s, p, c}, nil
}

type deviceInstallation struct {
	id, creator, binding string
	secret               []byte
	revision             int64
}
type deviceBinding struct {
	view               notifications.DeviceView
	id, account, keyID string
	ciphertext         []byte
	tokenVersion       int64
	registered         time.Time
	invalidated        *time.Time
}

const deviceBindingColumns = `b.id::text,b.account_id::text,b.installation_id::text,i.revision,b.label,b.topic,b.environment,b.state,b.enabled,b.muted,b.permission,b.token_version,b.token_ciphertext,COALESCE(b.token_key_id,''),b.token_registered_at,b.token_invalidated_at,b.created_at,i.updated_at,b.last_seen_at,` + notificationDeviceReadySQL

func scanDeviceBinding(row pgx.Row) (deviceBinding, error) {
	var b deviceBinding
	err := row.Scan(&b.id, &b.account, &b.view.InstallationID, &b.view.Revision, &b.view.Label, &b.view.Topic, &b.view.Environment, &b.view.State, &b.view.Enabled, &b.view.Muted, &b.view.Permission, &b.tokenVersion, &b.ciphertext, &b.keyID, &b.registered, &b.invalidated, &b.view.CreatedAt, &b.view.UpdatedAt, &b.view.LastSeenAt, &b.view.RevocationReady)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, notifications.ErrDeviceNotFound
	}
	if err != nil {
		return b, err
	}
	b.view.TokenStatus = "current"
	if b.ciphertext == nil {
		b.view.TokenStatus = "absent"
	} else if b.invalidated != nil {
		b.view.TokenStatus = "invalid"
	}
	return b, nil
}
func deviceStorageError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return notifications.ErrDeviceConflict
	}
	return err
}

func (d *NotificationDeviceStore) begin(ctx context.Context, actor monitoring.Actor, write bool) (pgx.Tx, error) {
	tx, err := d.store.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	// Creation and attachment quotas are bounded and global. One short advisory
	// lock orders installation/account mutations consistently across API replicas.
	if write {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72108003010)`); err != nil {
			tx.Rollback(ctx)
			return nil, err
		}
	}
	if err = lockNotificationOwner(ctx, tx, actor, write); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func installation(ctx context.Context, tx pgx.Tx, id string, write bool) (deviceInstallation, error) {
	return readInstallation(ctx, tx, id, write, true)
}

// Delivery has no possession-proof authority and never reads the installation
// secret hash. It shares the same row lock and generation fields with API reads.
func deliveryInstallation(ctx context.Context, tx pgx.Tx, id string, write bool) (deviceInstallation, error) {
	return readInstallation(ctx, tx, id, write, false)
}
func readInstallation(ctx context.Context, tx pgx.Tx, id string, write, proof bool) (deviceInstallation, error) {
	var i deviceInstallation
	if !eventUUID(id) {
		return i, notifications.ErrDeviceNotFound
	}
	lock := " FOR SHARE"
	if write {
		lock = " FOR UPDATE"
	}
	hashColumn := "NULL::bytea"
	if proof {
		hashColumn = "secret_hash"
	}
	err := tx.QueryRow(ctx, `SELECT id::text,creator_account_id::text,`+hashColumn+`,revision,COALESCE(current_binding_id::text,'') FROM dashboard_notification_installations WHERE id=$1::uuid`+lock, id).Scan(&i.id, &i.creator, &i.secret, &i.revision, &i.binding)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, notifications.ErrDeviceNotFound
	}
	return i, err
}
func deviceProof(i deviceInstallation, secret string) error {
	hash, err := notifications.InstallationSecretHash(i.id, secret)
	if err != nil || !hmac.Equal(hash, i.secret) {
		return notifications.ErrDeviceNotFound
	}
	return nil
}
func deviceRevision(i deviceInstallation, expected int64) error {
	if expected <= 0 || i.revision != expected || i.revision == math.MaxInt64 {
		return notifications.ErrDeviceConflict
	}
	return nil
}
func currentDevice(ctx context.Context, tx pgx.Tx, i deviceInstallation) (deviceBinding, error) {
	if i.binding == "" {
		return deviceBinding{}, notifications.ErrDeviceNotFound
	}
	return scanDeviceBinding(tx.QueryRow(ctx, `SELECT `+deviceBindingColumns+` FROM dashboard_notification_device_bindings b JOIN dashboard_notification_installations i ON i.id=b.installation_id WHERE b.id=$1::uuid AND i.id=$2::uuid AND i.current_binding_id=b.id AND b.state='bound'`, i.binding, i.id))
}
func deviceOwner(b deviceBinding, a monitoring.Actor) error {
	if b.account != a.Account.ID {
		return notifications.ErrDeviceNotFound
	}
	return nil
}
func auditDevice(ctx context.Context, tx pgx.Tx, account, action, id string) error {
	_, err := tx.Exec(ctx, `INSERT INTO dashboard_audit(account_id,action,resource_kind,resource_id) VALUES($1::uuid,$2,'notification_device',$3)`, account, action, id)
	return err
}

func (d *NotificationDeviceStore) InspectInstallation(ctx context.Context, actor monitoring.Actor, id, secret string) (notifications.InstallationState, error) {
	var result notifications.InstallationState
	tx, err := d.begin(ctx, actor, false)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	i, err := installation(ctx, tx, id, false)
	if err != nil {
		return result, err
	}
	if err = deviceProof(i, secret); err != nil {
		return result, err
	}
	result = notifications.InstallationState{InstallationID: i.id, Revision: i.revision, HasBinding: i.binding != ""}
	if i.binding != "" {
		b, err := currentDevice(ctx, tx, i)
		if err != nil {
			return notifications.InstallationState{}, err
		}
		result.BoundToCurrentAccount = b.account == actor.Account.ID
	}
	return result, tx.Commit(ctx)
}

func deviceCreationAdmission(ctx context.Context, tx pgx.Tx, account string) error {
	var global, personal, recent int
	err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE creator_account_id=$1::uuid),count(*) FILTER(WHERE creator_account_id=$1::uuid AND created_at>clock_timestamp()-interval '1 minute') FROM dashboard_notification_installations`, account).Scan(&global, &personal, &recent)
	if err != nil {
		return err
	}
	if global >= notifications.MaximumInstallations || personal >= notifications.MaximumCreatedInstallations {
		return notifications.ErrDeviceCapacity
	}
	if recent >= notifications.MaximumInstallationCreatesPerMinute {
		return notifications.ErrDeviceRateLimited
	}
	return nil
}
func deviceBindingAdmission(ctx context.Context, tx pgx.Tx, account, id string) error {
	var bound, history, recent int
	err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM dashboard_notification_device_bindings WHERE account_id=$1::uuid AND state='bound'),count(*),count(*) FILTER(WHERE created_at>clock_timestamp()-interval '1 minute') FROM dashboard_notification_device_bindings WHERE installation_id=$2::uuid`, account, id).Scan(&bound, &history, &recent)
	if err != nil {
		return err
	}
	if bound >= notifications.MaximumBoundDevices || history >= notifications.MaximumInstallationBindings {
		return notifications.ErrDeviceCapacity
	}
	if recent >= notifications.MaximumBindingChangesPerMinute {
		return notifications.ErrDeviceRateLimited
	}
	return nil
}

// Repeated startup/token callbacks and setting writes are limited to 30 per
// fixed 60-second installation window. Real stop/mute/permission-loss changes
// bypass admission only when the same edit does not rename, reenable or unmute.
// No-op settings produce no revision, counter or audit entry.
func deviceMutationAdmission(ctx context.Context, tx pgx.Tx, id string, stopping bool) error {
	if stopping {
		return nil
	}
	tag, err := tx.Exec(ctx, `UPDATE dashboard_notification_installations SET
 mutation_count=CASE WHEN mutation_window_started_at<=statement_timestamp()-interval '1 minute' THEN 1 ELSE mutation_count+1 END,
 mutation_window_started_at=CASE WHEN mutation_window_started_at<=statement_timestamp()-interval '1 minute' THEN statement_timestamp() ELSE mutation_window_started_at END
 WHERE id=$1::uuid AND (mutation_window_started_at<=statement_timestamp()-interval '1 minute' OR mutation_count<$2)`, id, notifications.MaximumDeviceMutationsPerMinute)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return notifications.ErrDeviceRateLimited
	}
	return nil
}

// Bind is an explicit user operation. Normal startup/token registration must
// call Refresh; it must never resurrect a remotely removed installation.
// The client must first acknowledge an authenticated revocation reservation.
// Every bind uses its positive current installation revision and reserved target.
func (d *NotificationDeviceStore) Bind(ctx context.Context, actor monitoring.Actor, expectedRevision int64, input notifications.DeviceRegistration) (notifications.DeviceView, error) {
	return d.bind(ctx, actor, expectedRevision, input, false, false)
}

// SwitchBinding is explicit account-switch intent, independent of expired old
// account credentials. The new actor must authenticate and prove possession of
// the installation secret. It never discloses the previous account or metadata.
func (d *NotificationDeviceStore) SwitchBinding(ctx context.Context, actor monitoring.Actor, expectedRevision int64, input notifications.DeviceRegistration, confirmSwitch bool) (notifications.DeviceView, error) {
	if !confirmSwitch {
		return notifications.DeviceView{}, notifications.ErrDeviceInvalid
	}
	return d.bind(ctx, actor, expectedRevision, input, true, true)
}
func (d *NotificationDeviceStore) bind(ctx context.Context, actor monitoring.Actor, expectedRevision int64, input notifications.DeviceRegistration, switching, confirmed bool) (notifications.DeviceView, error) {
	var empty notifications.DeviceView
	if err := input.Validate(d.policy); err != nil {
		return empty, err
	}
	tx, err := d.begin(ctx, actor, true)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	i, err := installation(ctx, tx, input.InstallationID, true)
	if err != nil {
		return empty, err
	}
	if err = deviceProof(i, input.InstallationSecret); err != nil {
		return empty, err
	}
	if err = deviceRevision(i, expectedRevision); err != nil {
		return empty, err
	}
	r, err := deviceRevocation(ctx, tx, input.RevocationID)
	if err != nil {
		return empty, err
	}
	intent := "bind"
	if switching {
		intent = "switch"
	}
	if !revocationMatches(r, i, actor, input.RevocationCredential) || r.state != "reserved" || r.intent != intent || r.origin != i.binding {
		return empty, notifications.ErrDeviceConflict
	}
	if switching {
		old, err := currentDevice(ctx, tx, i)
		if err != nil {
			return empty, err
		}
		if !confirmed || old.account == actor.Account.ID {
			return empty, notifications.ErrDeviceConflict
		}
		if err = closeDeviceBinding(ctx, tx, i, "detached"); err != nil {
			return empty, err
		}
	} else if i.binding != "" {
		return empty, notifications.ErrDeviceConflict
	}
	if err = deviceBindingAdmission(ctx, tx, actor.Account.ID, i.id); err != nil {
		return empty, err
	}
	if err = deviceMutationAdmission(ctx, tx, i.id, false); err != nil {
		return empty, err
	}
	i.revision++
	id := r.target
	pins := notifications.DeviceTokenBinding{InstallationID: i.id, BindingID: id, AccountID: actor.Account.ID, Topic: input.Topic, Environment: input.Environment, TokenVersion: 1}
	encrypted, err := d.cipher.Seal(pins, input.Token)
	if err != nil {
		return empty, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_device_bindings(id,installation_id,account_id,label,topic,environment,state,enabled,muted,permission,token_version,token_ciphertext,token_key_id,token_digest,token_registered_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,'bound',$7,$8,$9,1,$10,$11,$12,clock_timestamp())`, id, i.id, actor.Account.ID, input.Label, input.Topic, input.Environment, input.Enabled, input.Muted, input.Permission, encrypted.Ciphertext, encrypted.KeyID, notifications.DeviceTokenDigest(input.Topic, input.Environment, input.Token))
	if err != nil {
		return empty, deviceStorageError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_installations SET current_binding_id=$2::uuid,revision=$3,updated_at=clock_timestamp() WHERE id=$1::uuid`, i.id, id, i.revision); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_device_revocations SET state='active',activated_at=clock_timestamp() WHERE id=$1::uuid`, r.id); err != nil {
		return empty, err
	}
	i.binding = id
	if err = auditDevice(ctx, tx, actor.Account.ID, "notification_device.bound", i.id); err != nil {
		return empty, err
	}
	b, err := currentDevice(ctx, tx, i)
	if err != nil {
		return empty, err
	}
	return b.view, tx.Commit(ctx)
}
func closeDeviceBinding(ctx context.Context, tx pgx.Tx, i deviceInstallation, state string) error {
	if _, err := tx.Exec(ctx, `UPDATE dashboard_notification_device_revocations SET state='revoked',secret_hash=NULL,revoked_at=clock_timestamp() WHERE target_binding_id=$1::uuid AND state<>'revoked'`, i.binding); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `UPDATE dashboard_notification_device_bindings SET state=$2,enabled=false,token_ciphertext=NULL,token_key_id=NULL,token_digest=NULL,closed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1::uuid AND state='bound'`, i.binding, state); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE dashboard_notification_installations SET current_binding_id=NULL,updated_at=clock_timestamp() WHERE id=$1::uuid`, i.id)
	return err
}

func (d *NotificationDeviceStore) Refresh(ctx context.Context, actor monitoring.Actor, id string, expectedRevision int64, input notifications.DeviceRefresh) (notifications.DeviceView, error) {
	var empty notifications.DeviceView
	if err := input.Validate(id); err != nil {
		return empty, err
	}
	tx, err := d.begin(ctx, actor, true)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	i, err := installation(ctx, tx, id, true)
	if err != nil {
		return empty, err
	}
	if err = deviceProof(i, input.InstallationSecret); err != nil {
		return empty, err
	}
	b, err := currentDevice(ctx, tx, i)
	if err != nil {
		return empty, err
	}
	if err = deviceOwner(b, actor); err != nil {
		return empty, err
	}
	if err = deviceRevision(i, expectedRevision); err != nil {
		return empty, err
	}
	if !d.policy.Allows(b.view.Topic, b.view.Environment) || b.tokenVersion == math.MaxInt64 {
		return empty, notifications.ErrDeviceConflict
	}
	stopping := notifications.DevicePermissionAllowsDelivery(b.view.Permission) && !notifications.DevicePermissionAllowsDelivery(input.Permission)
	if err = deviceMutationAdmission(ctx, tx, id, stopping); err != nil {
		return empty, err
	}
	version := b.tokenVersion + 1
	encrypted, err := d.cipher.Seal(notifications.DeviceTokenBinding{InstallationID: id, BindingID: b.id, AccountID: b.account, Topic: b.view.Topic, Environment: b.view.Environment, TokenVersion: version}, input.Token)
	if err != nil {
		return empty, err
	}
	_, err = tx.Exec(ctx, `UPDATE dashboard_notification_device_bindings SET token_version=$2,token_ciphertext=$3,token_key_id=$4,token_digest=$5,permission=$6,token_registered_at=clock_timestamp(),token_invalidated_at=NULL,updated_at=clock_timestamp(),last_seen_at=clock_timestamp() WHERE id=$1::uuid`, b.id, version, encrypted.Ciphertext, encrypted.KeyID, notifications.DeviceTokenDigest(b.view.Topic, b.view.Environment, input.Token), input.Permission)
	if err != nil {
		return empty, deviceStorageError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_installations SET revision=revision+1,updated_at=clock_timestamp() WHERE id=$1::uuid`, id); err != nil {
		return empty, err
	}
	if err = auditDevice(ctx, tx, actor.Account.ID, "notification_device.token_refreshed", id); err != nil {
		return empty, err
	}
	b, err = currentDevice(ctx, tx, i)
	if err != nil {
		return empty, err
	}
	return b.view, tx.Commit(ctx)
}

func (d *NotificationDeviceStore) Settings(ctx context.Context, actor monitoring.Actor, id string, expectedRevision int64, input notifications.DeviceSettings) (notifications.DeviceView, error) {
	var empty notifications.DeviceView
	if err := input.Validate(); err != nil {
		return empty, err
	}
	tx, err := d.begin(ctx, actor, true)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	i, err := installation(ctx, tx, id, true)
	if err != nil {
		return empty, err
	}
	b, err := currentDevice(ctx, tx, i)
	if err != nil {
		return empty, err
	}
	if err = deviceOwner(b, actor); err != nil {
		return empty, err
	}
	if err = deviceRevision(i, expectedRevision); err != nil {
		return empty, err
	}
	if input.Label == b.view.Label && input.Enabled == b.view.Enabled && input.Muted == b.view.Muted {
		return b.view, tx.Commit(ctx)
	}
	stopping := (b.view.Enabled && !input.Enabled || !b.view.Muted && input.Muted) &&
		!(!b.view.Enabled && input.Enabled) && !(b.view.Muted && !input.Muted) && input.Label == b.view.Label
	if err = deviceMutationAdmission(ctx, tx, id, stopping); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_device_bindings SET label=$2,enabled=$3,muted=$4,updated_at=clock_timestamp() WHERE id=$1::uuid`, b.id, input.Label, input.Enabled, input.Muted); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_installations SET revision=revision+1,updated_at=clock_timestamp() WHERE id=$1::uuid`, id); err != nil {
		return empty, err
	}
	if err = auditDevice(ctx, tx, actor.Account.ID, "notification_device.settings_changed", id); err != nil {
		return empty, err
	}
	b, err = currentDevice(ctx, tx, i)
	if err != nil {
		return empty, err
	}
	return b.view, tx.Commit(ctx)
}
func (d *NotificationDeviceStore) Remove(ctx context.Context, actor monitoring.Actor, id string, expectedRevision int64) error {
	return d.detach(ctx, actor, id, expectedRevision, "", "removed", false)
}
func (d *NotificationDeviceStore) Detach(ctx context.Context, actor monitoring.Actor, id string, expectedRevision int64, secret string) error {
	return d.detach(ctx, actor, id, expectedRevision, secret, "detached", true)
}
func (d *NotificationDeviceStore) detach(ctx context.Context, actor monitoring.Actor, id string, expectedRevision int64, secret, state string, proof bool) error {
	tx, err := d.begin(ctx, actor, true)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	i, err := installation(ctx, tx, id, true)
	if err != nil {
		return err
	}
	if proof {
		if err = deviceProof(i, secret); err != nil {
			return err
		}
	}
	b, err := currentDevice(ctx, tx, i)
	if err != nil {
		return err
	}
	if err = deviceOwner(b, actor); err != nil {
		return err
	}
	if err = deviceRevision(i, expectedRevision); err != nil {
		return err
	}
	if err = closeDeviceBinding(ctx, tx, i, state); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_installations SET revision=revision+1 WHERE id=$1::uuid`, id); err != nil {
		return err
	}
	if err = auditDevice(ctx, tx, actor.Account.ID, "notification_device."+state, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (d *NotificationDeviceStore) list(ctx context.Context, actor monitoring.Actor) ([]deviceBinding, error) {
	tx, err := d.begin(ctx, actor, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT `+deviceBindingColumns+` FROM dashboard_notification_device_bindings b JOIN dashboard_notification_installations i ON i.id=b.installation_id AND i.current_binding_id=b.id WHERE b.account_id=$1::uuid AND b.state='bound' ORDER BY b.installation_id LIMIT $2`, actor.Account.ID, notifications.MaximumBoundDevices+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []deviceBinding{}
	for rows.Next() {
		b, err := scanDeviceBinding(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, b)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(bindings) > notifications.MaximumBoundDevices {
		return nil, notifications.ErrDeviceCapacity
	}
	return bindings, tx.Commit(ctx)
}
func (d *NotificationDeviceStore) List(ctx context.Context, actor monitoring.Actor) ([]notifications.DeviceView, error) {
	bindings, err := d.list(ctx, actor)
	if err != nil {
		return nil, err
	}
	views := make([]notifications.DeviceView, len(bindings))
	for i, b := range bindings {
		views[i] = b.view
	}
	return views, nil
}
func (d *NotificationDeviceStore) eligible(b deviceBinding) bool {
	return b.view.RevocationReady && b.view.State == "bound" && b.view.Enabled && !b.view.Muted && notifications.DevicePermissionAllowsDelivery(b.view.Permission) && b.ciphertext != nil && b.invalidated == nil && d.policy.Allows(b.view.Topic, b.view.Environment)
}
func (d *NotificationDeviceStore) Candidates(ctx context.Context, actor monitoring.Actor) ([]notifications.DeviceCandidate, error) {
	bindings, err := d.list(ctx, actor)
	if err != nil {
		return nil, err
	}
	result := []notifications.DeviceCandidate{}
	for _, b := range bindings {
		if d.eligible(b) {
			result = append(result, notifications.DeviceCandidate{InstallationID: b.view.InstallationID, BindingID: b.id, AccountID: b.account, TokenVersion: b.tokenVersion})
		}
	}
	return result, nil
}

// Handoff is the final device fence. Caller separately checks current source,
// rule, account and inbox authority immediately before handing off to APNs.
// A switch/remove/refresh makes old candidate IDs or token versions unusable.
func (d *NotificationDeviceStore) Handoff(ctx context.Context, actor monitoring.Actor, candidate notifications.DeviceCandidate) (notifications.DeviceHandoff, error) {
	var empty notifications.DeviceHandoff
	if candidate.AccountID != actor.Account.ID || !eventUUID(candidate.BindingID) || candidate.TokenVersion <= 0 {
		return empty, notifications.ErrDeviceNotFound
	}
	tx, err := d.begin(ctx, actor, false)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	i, err := deliveryInstallation(ctx, tx, candidate.InstallationID, false)
	if err != nil {
		return empty, err
	}
	b, err := currentDevice(ctx, tx, i)
	if err != nil {
		return empty, err
	}
	if b.id != candidate.BindingID || b.tokenVersion != candidate.TokenVersion || deviceOwner(b, actor) != nil || !d.eligible(b) {
		return empty, notifications.ErrDeviceNotFound
	}
	token, err := d.cipher.Open(notifications.DeviceTokenBinding{InstallationID: i.id, BindingID: b.id, AccountID: b.account, Topic: b.view.Topic, Environment: b.view.Environment, TokenVersion: b.tokenVersion}, notifications.EncryptedDeviceToken{KeyID: b.keyID, Ciphertext: b.ciphertext})
	if err != nil {
		return empty, err
	}
	result := notifications.DeviceHandoff{DeviceCandidate: candidate, Topic: b.view.Topic, Environment: b.view.Environment, Token: token, RegisteredAt: b.registered}
	return result, tx.Commit(ctx)
}

// Invalidate records a provider rejection only for the exact current token and
// binding. A 410 timestamp older than its latest registration cannot disable a
// freshly registered identical token; a delayed response cannot affect a switch.
func (d *NotificationDeviceStore) Invalidate(ctx context.Context, candidate notifications.DeviceCandidate, invalidAt *time.Time) (bool, error) {
	if !eventUUID(candidate.InstallationID) || !eventUUID(candidate.BindingID) || !eventUUID(candidate.AccountID) || candidate.TokenVersion <= 0 || invalidAt != nil && (invalidAt.IsZero() || invalidAt.Year() < 1 || invalidAt.Year() > 9999) {
		return false, notifications.ErrDeviceInvalid
	}
	tx, err := d.store.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72108003010)`); err != nil {
		return false, err
	}
	changed, err := d.invalidateTx(ctx, tx, candidate, invalidAt)
	if err != nil {
		return false, err
	}
	return changed, tx.Commit(ctx)
}

// invalidateTx shares provider-result atomicity with the delivery journal. The
// caller must already hold the global device advisory lock in this transaction.
func (d *NotificationDeviceStore) invalidateTx(ctx context.Context, tx pgx.Tx, candidate notifications.DeviceCandidate, invalidAt *time.Time) (bool, error) {
	if !eventUUID(candidate.InstallationID) || !eventUUID(candidate.BindingID) || !eventUUID(candidate.AccountID) || candidate.TokenVersion <= 0 || invalidAt != nil && (invalidAt.IsZero() || invalidAt.Year() < 1 || invalidAt.Year() > 9999) {
		return false, notifications.ErrDeviceInvalid
	}
	i, err := deliveryInstallation(ctx, tx, candidate.InstallationID, true)
	if errors.Is(err, notifications.ErrDeviceNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	b, err := currentDevice(ctx, tx, i)
	if errors.Is(err, notifications.ErrDeviceNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if b.id != candidate.BindingID || b.account != candidate.AccountID || b.tokenVersion != candidate.TokenVersion || b.invalidated != nil || invalidAt != nil && invalidAt.Before(b.registered) {
		return false, nil
	}
	if i.revision == math.MaxInt64 {
		return false, notifications.ErrDeviceConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_device_bindings SET token_invalidated_at=COALESCE($2,clock_timestamp()),updated_at=clock_timestamp() WHERE id=$1::uuid`, b.id, invalidAt); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_installations SET revision=revision+1,updated_at=clock_timestamp() WHERE id=$1::uuid`, i.id); err != nil {
		return false, err
	}
	if err = auditDevice(ctx, tx, b.account, "notification_device.token_invalidated", i.id); err != nil {
		return false, err
	}
	return true, nil
}
