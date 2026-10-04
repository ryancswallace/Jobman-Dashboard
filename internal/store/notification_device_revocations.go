package store

import (
	"context"
	"crypto/hmac"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

// Shared by candidate reads, final handoff and transactional delivery enqueue.
// Binding alias b is deliberate; migration-era bindings fail closed until an
// acknowledged reservation is activated for that exact generation.
const notificationDeviceReadySQL = `EXISTS(SELECT 1 FROM dashboard_notification_device_revocations r WHERE r.target_binding_id=b.id AND r.state='active') AND NOT EXISTS(SELECT 1 FROM dashboard_notification_device_revocations pending WHERE pending.target_binding_id=b.id AND pending.intent='existing' AND pending.state='reserved')`

type deviceRevocationRow struct {
	id, installation, account, origin, target, intent, state string
	hash                                                     []byte
}

func deviceRevocation(ctx context.Context, tx pgx.Tx, id string) (deviceRevocationRow, error) {
	var r deviceRevocationRow
	err := tx.QueryRow(ctx, `SELECT id::text,installation_id::text,account_id::text,COALESCE(origin_binding_id::text,''),target_binding_id::text,intent,state,secret_hash FROM dashboard_notification_device_revocations WHERE id=$1::uuid`, id).Scan(&r.id, &r.installation, &r.account, &r.origin, &r.target, &r.intent, &r.state, &r.hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, notifications.ErrDeviceConflict
	}
	return r, err
}
func revocationMatches(r deviceRevocationRow, i deviceInstallation, a monitoring.Actor, secret string) bool {
	hash, err := notifications.RevocationSecretHash(r.id, i.id, r.target, secret)
	return err == nil && r.installation == i.id && r.account == a.Account.ID && r.state != "revoked" && hmac.Equal(hash, r.hash)
}
func revocationReceipt(r deviceRevocationRow, i deviceInstallation) notifications.DeviceRevocationReceipt {
	return notifications.DeviceRevocationReceipt{InstallationID: i.id, Revision: i.revision, RevocationID: r.id, Intent: r.intent}
}
func (d *NotificationDeviceStore) ReserveRevocation(ctx context.Context, a monitoring.Actor, id string, expected int64, in notifications.DeviceRevocationInput) (notifications.DeviceRevocationReceipt, error) {
	var empty notifications.DeviceRevocationReceipt
	if err := in.Validate(id); err != nil {
		return empty, err
	}
	tx, err := d.begin(ctx, a, true)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	i, err := installation(ctx, tx, id, true)
	created := errors.Is(err, notifications.ErrDeviceNotFound)
	if created {
		if expected != 0 {
			return empty, notifications.ErrDeviceNotFound
		}
		if err = deviceCreationAdmission(ctx, tx, a.Account.ID); err != nil {
			return empty, err
		}
		hash, _ := notifications.InstallationSecretHash(id, in.InstallationSecret)
		if _, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_installations(id,creator_account_id,secret_hash) VALUES($1::uuid,$2::uuid,$3)`, id, a.Account.ID, hash); err != nil {
			return empty, deviceStorageError(err)
		}
		i = deviceInstallation{id: id, creator: a.Account.ID, secret: hash, revision: 1}
	} else if err != nil {
		return empty, err
	}
	if err = deviceProof(i, in.InstallationSecret); err != nil {
		return empty, err
	}
	prior, priorErr := deviceRevocation(ctx, tx, in.RevocationID)
	if priorErr == nil {
		current := prior.origin == i.binding && prior.state == "reserved" || prior.target == i.binding && prior.state == "active"
		if !revocationMatches(prior, i, a, in.RevocationCredential) || !current {
			return empty, notifications.ErrDeviceConflict
		}
		return revocationReceipt(prior, i), tx.Commit(ctx)
	}
	if !errors.Is(priorErr, notifications.ErrDeviceConflict) {
		return empty, priorErr
	}
	if !created {
		if err = deviceRevision(i, expected); err != nil {
			return empty, err
		}
	}
	r := deviceRevocationRow{id: in.RevocationID, installation: id, account: a.Account.ID, origin: i.binding, intent: "bind", state: "reserved"}
	if i.binding != "" {
		b, err := currentDevice(ctx, tx, i)
		if err != nil {
			return empty, err
		}
		r.intent = "switch"
		if b.account == a.Account.ID {
			r.intent = "existing"
			r.target = i.binding
		}
	}
	if r.target == "" {
		r.target, err = newID()
		if err != nil {
			return empty, err
		}
	}
	var all, pending, target int
	if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE state='reserved'),count(*) FILTER(WHERE target_binding_id=$2::uuid) FROM dashboard_notification_device_revocations WHERE installation_id=$1::uuid`, id, r.target).Scan(&all, &pending, &target); err != nil {
		return empty, err
	}
	if all >= notifications.MaximumInstallationRevocations || pending >= notifications.MaximumPendingDeviceRevocations || target >= notifications.MaximumBindingRevocations {
		return empty, notifications.ErrDeviceCapacity
	}
	if err = deviceMutationAdmission(ctx, tx, id, false); err != nil {
		return empty, err
	}
	r.hash, _ = notifications.RevocationSecretHash(r.id, id, r.target, in.RevocationCredential)
	if _, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_device_revocations(id,installation_id,account_id,origin_binding_id,target_binding_id,intent,state,secret_hash) VALUES($1::uuid,$2::uuid,$3::uuid,NULLIF($4,'')::uuid,$5::uuid,$6,'reserved',$7)`, r.id, id, a.Account.ID, r.origin, r.target, r.intent, r.hash); err != nil {
		return empty, deviceStorageError(err)
	}
	if !created {
		i.revision++
		if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_installations SET revision=revision+1,updated_at=clock_timestamp() WHERE id=$1::uuid`, id); err != nil {
			return empty, err
		}
	}
	if err = auditDevice(ctx, tx, a.Account.ID, "notification_device.revocation_reserved", id); err != nil {
		return empty, err
	}
	return revocationReceipt(r, i), tx.Commit(ctx)
}

// ActivateRevocation upgrades an existing binding without touching its token,
// enable/mute preferences or ownership. Native must acknowledge Reserve first.
func (d *NotificationDeviceStore) ActivateRevocation(ctx context.Context, a monitoring.Actor, id string, expected int64, in notifications.DeviceRevocationInput) (notifications.DeviceRevocationReceipt, error) {
	var empty notifications.DeviceRevocationReceipt
	if err := in.Validate(id); err != nil {
		return empty, err
	}
	tx, err := d.begin(ctx, a, true)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	i, err := installation(ctx, tx, id, true)
	if err != nil {
		return empty, err
	}
	if err = deviceProof(i, in.InstallationSecret); err != nil {
		return empty, err
	}
	b, err := currentDevice(ctx, tx, i)
	if err != nil {
		return empty, err
	}
	if err = deviceOwner(b, a); err != nil {
		return empty, err
	}
	r, err := deviceRevocation(ctx, tx, in.RevocationID)
	if err != nil {
		return empty, err
	}
	if !revocationMatches(r, i, a, in.RevocationCredential) || r.intent != "existing" || r.target != i.binding {
		return empty, notifications.ErrDeviceConflict
	}
	if r.state == "active" {
		return revocationReceipt(r, i), tx.Commit(ctx)
	}
	if err = deviceRevision(i, expected); err != nil {
		return empty, err
	}
	if err = deviceMutationAdmission(ctx, tx, id, false); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_device_revocations SET state='active',activated_at=clock_timestamp() WHERE id=$1::uuid`, r.id); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_installations SET revision=revision+1,updated_at=clock_timestamp() WHERE id=$1::uuid`, id); err != nil {
		return empty, err
	}
	i.revision++
	if err = auditDevice(ctx, tx, a.Account.ID, "notification_device.revocation_activated", id); err != nil {
		return empty, err
	}
	return revocationReceipt(r, i), tx.Commit(ctx)
}

// RevokeDevice has only one authority: cancel this immutable reservation and
// detach its exact target if current. Unknown IDs allocate no storage. It never
// checks a login session because native sign-out has already discarded it.
func (d *NotificationDeviceStore) RevokeDevice(ctx context.Context, id, secret string) error {
	if err := notifications.ValidateDeviceRevocation(id, secret); err != nil {
		return err
	}
	tx, err := d.store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72108003010)`); err != nil {
		return err
	}
	r, err := deviceRevocation(ctx, tx, id)
	if errors.Is(err, notifications.ErrDeviceConflict) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	hash, _ := notifications.RevocationSecretHash(r.id, r.installation, r.target, secret)
	if r.state == "revoked" || !hmac.Equal(hash, r.hash) {
		return tx.Commit(ctx)
	}
	i, err := installation(ctx, tx, r.installation, true)
	if err != nil {
		return err
	}
	if i.revision == math.MaxInt64 {
		return notifications.ErrDeviceConflict
	}
	if i.binding == r.target {
		if err = closeDeviceBinding(ctx, tx, i, "detached"); err != nil {
			return err
		}
	} else {
		if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_device_revocations SET state='revoked',secret_hash=NULL,revoked_at=clock_timestamp() WHERE id=$1::uuid`, id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_notification_installations SET revision=revision+1,updated_at=clock_timestamp() WHERE id=$1::uuid`, i.id); err != nil {
		return err
	}
	if err = auditDevice(ctx, tx, r.account, "notification_device.offline_revoked", i.id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
