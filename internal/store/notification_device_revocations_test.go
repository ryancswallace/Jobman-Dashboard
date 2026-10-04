package store

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

func reserveDeviceTest(t *testing.T, d *NotificationDeviceStore, a monitoring.Actor, revision int64, in *notifications.DeviceRegistration) notifications.DeviceRevocationReceipt {
	t.Helper()
	in.RevocationID, _ = newID()
	r, err := d.ReserveRevocation(t.Context(), a, in.InstallationID, revision, revocationInput(*in))
	if err != nil {
		t.Fatal("reservation", err)
	}
	return r
}
func deviceAuditCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM dashboard_audit`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestNotificationDeviceRevocationReservationCancellationAndLostReceipts(t *testing.T) {
	s := testDB(t)
	d := deviceStore(t, s)
	a := reportActor(t, s, 201)
	ctx := t.Context()
	in := deviceRegistration(1)
	// A revoke that arrives before an uncertain first reservation cannot allocate
	// authority. A late reservation alone is unbound and push-ineligible.
	if err := d.RevokeDevice(ctx, in.RevocationID, in.RevocationCredential); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_notification_device_revocations`).Scan(&n); err != nil || n != 0 {
		t.Fatal("unknown revoke allocated state", err)
	}
	r, err := d.ReserveRevocation(ctx, a, in.InstallationID, 0, revocationInput(in))
	if err != nil || r.Intent != "bind" || r.Revision != 1 {
		t.Fatal("initial reserve", err)
	}
	if list, err := d.Candidates(ctx, a); err != nil || len(list) != 0 {
		t.Fatal("unacknowledged reservation delivered", err)
	}
	before := deviceAuditCount(t, s)
	retry, err := d.ReserveRevocation(ctx, a, in.InstallationID, 0, revocationInput(in))
	if err != nil || retry != r || deviceAuditCount(t, s) != before {
		t.Fatal("lost receipt retry mutated", err)
	}
	if err = d.RevokeDevice(ctx, in.RevocationID, in.RevocationCredential); err != nil {
		t.Fatal(err)
	}
	state, err := d.InspectInstallation(ctx, a, in.InstallationID, in.InstallationSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Bind(ctx, a, state.Revision, in); !errors.Is(err, notifications.ErrDeviceConflict) {
		t.Fatal("cancelled reservation attached", err)
	}
	if _, err = d.ReserveRevocation(ctx, a, in.InstallationID, state.Revision, revocationInput(in)); !errors.Is(err, notifications.ErrDeviceConflict) {
		t.Fatal("revoked ID reassigned", err)
	}
	before = deviceAuditCount(t, s)
	if err = d.RevokeDevice(ctx, in.RevocationID, in.RevocationCredential); err != nil || deviceAuditCount(t, s) != before {
		t.Fatal("duplicate revoke changed audit", err)
	}
	r = reserveDeviceTest(t, d, a, state.Revision, &in)
	v, err := d.Bind(ctx, a, r.Revision, in)
	if err != nil || !v.RevocationReady {
		t.Fatal("acknowledged bind", err)
	}
	// The binding response can be lost: replaying the acknowledged reservation
	// verifies the same current generation and returns the current revision.
	retry, err = d.ReserveRevocation(ctx, a, in.InstallationID, r.Revision, revocationInput(in))
	if err != nil || retry.Revision != v.Revision {
		t.Fatal("lost bind response recovery", err)
	}
	candidate := deviceCandidate(t, d, a)
	if err = d.RevokeDevice(ctx, in.RevocationID, in.RevocationCredential); err != nil {
		t.Fatal(err)
	}
	noDeviceHandoff(t, d, a, candidate)
	var hash []byte
	var stateName string
	if err = s.Pool.QueryRow(ctx, `SELECT state,secret_hash FROM dashboard_notification_device_revocations WHERE id=$1::uuid`, in.RevocationID).Scan(&stateName, &hash); err != nil || stateName != "revoked" || hash != nil {
		t.Fatal("tombstone retained secret or missing", err)
	}
}
func TestNotificationDeviceRevocationPinsBindingAndSwitch(t *testing.T) {
	s := testDB(t)
	d := deviceStore(t, s)
	a, b := reportActor(t, s, 201), reportActor(t, s, 202)
	ctx := t.Context()
	in, v := deviceBind(t, d, a, 1)
	old := in
	oldCandidate := deviceCandidate(t, d, a)
	r := reserveDeviceTest(t, d, b, v.Revision, &in)
	if r.Intent != "switch" {
		t.Fatal(r.Intent)
	}
	if err := d.RevokeDevice(ctx, in.RevocationID, in.RevocationCredential); err != nil {
		t.Fatal(err)
	}
	state, err := d.InspectInstallation(ctx, b, in.InstallationID, in.InstallationSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.SwitchBinding(ctx, b, state.Revision, in, true); !errors.Is(err, notifications.ErrDeviceConflict) {
		t.Fatal("cancelled switch admitted", err)
	}
	if _, err = d.Handoff(ctx, a, oldCandidate); err != nil {
		t.Fatal("cancelling future target detached origin", err)
	}
	r = reserveDeviceTest(t, d, b, state.Revision, &in)
	// Reusing random secret under a fresh ID is safe; old ID remains generation-bound.
	v, err = d.SwitchBinding(ctx, b, r.Revision, in, true)
	if err != nil {
		t.Fatal(err)
	}
	current := deviceCandidate(t, d, b)
	if err = d.RevokeDevice(ctx, old.RevocationID, old.RevocationCredential); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Handoff(ctx, b, current); err != nil {
		t.Fatal("old queued credential detached new owner", err)
	}
	if _, err = d.ReserveRevocation(ctx, b, in.InstallationID, v.Revision, revocationInput(old)); !errors.Is(err, notifications.ErrDeviceConflict) {
		t.Fatal("old ID reused across account", err)
	}
	bad := in
	bad.RevocationCredential = deviceRegistration(7).RevocationCredential
	if err = d.RevokeDevice(ctx, bad.RevocationID, bad.RevocationCredential); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Handoff(ctx, b, current); err != nil {
		t.Fatal("wrong secret detached", err)
	}
}
func TestNotificationDeviceRevocationUpgradePauseAndActivation(t *testing.T) {
	s := testDB(t)
	d := deviceStore(t, s)
	a := reportActor(t, s, 201)
	ctx := t.Context()
	in, v := deviceBind(t, d, a, 1)
	candidate := deviceCandidate(t, d, a)
	// Simulate a pre-migration binding: no active narrow credential exists.
	deviceExec(t, s, `DELETE FROM dashboard_notification_device_revocations`)
	noDeviceHandoff(t, d, a, candidate)
	r := reserveDeviceTest(t, d, a, v.Revision, &in)
	if r.Intent != "existing" {
		t.Fatal(r.Intent)
	}
	noDeviceHandoff(t, d, a, candidate)
	receipt, err := d.ActivateRevocation(ctx, a, in.InstallationID, r.Revision, revocationInput(in))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Handoff(ctx, a, candidate); err != nil {
		t.Fatal("upgrade not active", err)
	}
	before := deviceAuditCount(t, s)
	retry, err := d.ActivateRevocation(ctx, a, in.InstallationID, r.Revision, revocationInput(in))
	if err != nil || retry != receipt || deviceAuditCount(t, s) != before {
		t.Fatal("lost activation response not idempotent", err)
	}
	// Recovering a lost local capability pauses old ready state. Even if an
	// unknown revoke preceded this late reserve, it cannot deliver until ack.
	next := in
	r = reserveDeviceTest(t, d, a, receipt.Revision, &next)
	noDeviceHandoff(t, d, a, candidate)
	if err = d.RevokeDevice(ctx, next.RevocationID, next.RevocationCredential); err != nil {
		t.Fatal(err)
	}
	state, err := d.InspectInstallation(ctx, a, in.InstallationID, in.InstallationSecret)
	if err != nil || state.HasBinding {
		t.Fatal("pending existing reservation did not detach exact target", err)
	}
	if _, err = d.ActivateRevocation(ctx, a, in.InstallationID, state.Revision, revocationInput(next)); !errors.Is(err, notifications.ErrDeviceNotFound) {
		t.Fatal("cancelled upgrade activated", err)
	}
}
func TestNotificationDeviceRevocationAuditRollbackAndConcurrentAdmission(t *testing.T) {
	s := testDB(t)
	d := deviceStore(t, s)
	a := reportActor(t, s, 201)
	ctx := t.Context()
	in := deviceRegistration(1)
	r := reserveDeviceTest(t, d, a, 0, &in)
	deviceExec(t, s, `CREATE FUNCTION reject_revocation_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='notification_device.offline_revoked' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END$$; CREATE TRIGGER reject_revocation_audit BEFORE INSERT ON dashboard_audit FOR EACH ROW EXECUTE FUNCTION reject_revocation_audit()`)
	if err := d.RevokeDevice(ctx, in.RevocationID, in.RevocationCredential); err == nil {
		t.Fatal("audit failure ignored")
	}
	if _, err := d.Bind(ctx, a, r.Revision, in); err != nil {
		t.Fatal("failed revoke partially cancelled admission", err)
	}
	candidate := deviceCandidate(t, d, a)
	if err := d.RevokeDevice(ctx, in.RevocationID, in.RevocationCredential); err == nil {
		t.Fatal("audit failure ignored for attached target")
	}
	if _, err := d.Handoff(ctx, a, candidate); err != nil {
		t.Fatal("failed revoke partially detached", err)
	}
	deviceExec(t, s, `DROP TRIGGER reject_revocation_audit ON dashboard_audit`)
	if err := d.RevokeDevice(ctx, in.RevocationID, in.RevocationCredential); err != nil {
		t.Fatal(err)
	}
	state, err := d.InspectInstallation(ctx, a, in.InstallationID, in.InstallationSecret)
	if err != nil {
		t.Fatal(err)
	}
	r = reserveDeviceTest(t, d, a, state.Revision, &in)
	// Both serialization orders are safe: revoke first cancels admission; bind
	// first creates exactly the generation revoked by the second transaction.
	var wg sync.WaitGroup
	var bindErr, revokeErr error
	wg.Go(func() { _, bindErr = d.Bind(ctx, a, r.Revision, in) })
	wg.Go(func() { revokeErr = d.RevokeDevice(ctx, in.RevocationID, in.RevocationCredential) })
	wg.Wait()
	if revokeErr != nil || bindErr != nil && !errors.Is(bindErr, notifications.ErrDeviceConflict) {
		t.Fatal(bindErr, revokeErr)
	}
	state, err = d.InspectInstallation(ctx, a, in.InstallationID, in.InstallationSecret)
	if err != nil || state.HasBinding {
		t.Fatal("race left live binding", err)
	}
}
func TestNotificationDeviceRevocationBoundsAndCurrentOwner(t *testing.T) {
	s := testDB(t)
	d := deviceStore(t, s)
	a, b := reportActor(t, s, 201), reportActor(t, s, 202)
	ctx := t.Context()
	in, v := deviceBind(t, d, a, 1)
	before := deviceAuditCount(t, s)
	bad := in
	bad.InstallationSecret = deviceRegistration(3).InstallationSecret
	if _, err := d.ReserveRevocation(ctx, b, in.InstallationID, v.Revision, revocationInput(bad)); !errors.Is(err, notifications.ErrDeviceNotFound) || deviceAuditCount(t, s) != before {
		t.Fatal("wrong installation proof admitted", err)
	}
	for n := 0; n < 4; n++ {
		r := reserveDeviceTest(t, d, a, v.Revision, &in)
		v.Revision = r.Revision
	}
	in.RevocationID, _ = newID()
	if _, err := d.ReserveRevocation(ctx, a, in.InstallationID, v.Revision, revocationInput(in)); !errors.Is(err, notifications.ErrDeviceCapacity) {
		t.Fatal("five credential target bound", err)
	}
	// Secret hashes are purpose/binding bound and never plaintext in SQL.
	var stored []byte
	if err := s.Pool.QueryRow(ctx, `SELECT secret_hash FROM dashboard_notification_device_revocations WHERE state='active' LIMIT 1`).Scan(&stored); err != nil || len(stored) != 32 || bytes.Contains(stored, []byte(in.RevocationCredential)) {
		t.Fatal("credential hash", err)
	}
	if err := d.Remove(ctx, a, in.InstallationID, v.Revision); err != nil {
		t.Fatal("quota blocked removal", err)
	}
	state, err := d.InspectInstallation(ctx, a, in.InstallationID, in.InstallationSecret)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 5; n++ {
		r := reserveDeviceTest(t, d, a, state.Revision, &in)
		state.Revision = r.Revision
	}
	in.RevocationID, _ = newID()
	if _, err = d.ReserveRevocation(ctx, a, in.InstallationID, state.Revision, revocationInput(in)); !errors.Is(err, notifications.ErrDeviceCapacity) {
		t.Fatal("pending reservations unbounded", err)
	}
	// Synthetic retained tombstones exercise global per-installation bound without
	// 500 network mutations. Removing remains independent of this admission.
	deviceExec(t, s, `UPDATE dashboard_notification_device_revocations SET state='revoked',secret_hash=NULL,revoked_at=clock_timestamp()`)
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_notification_device_revocations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	deviceExec(t, s, `INSERT INTO dashboard_notification_device_revocations(id,installation_id,account_id,target_binding_id,intent,state,revoked_at) SELECT ('89000000-0000-4000-8000-'||lpad(n::text,12,'0'))::uuid,$1::uuid,$2::uuid,$3::uuid,'bind','revoked',clock_timestamp() FROM generate_series(1,$4::integer)n`, in.InstallationID, a.Account.ID, in.RevocationID, 500-count)
	if _, err = d.ReserveRevocation(ctx, a, in.InstallationID, state.Revision, revocationInput(in)); !errors.Is(err, notifications.ErrDeviceCapacity) {
		t.Fatal("retained bound", err)
	}
}

func TestNotificationDeviceRevocationCancelledRequestDoesNotAcknowledge(t *testing.T) {
	s := testDB(t)
	d := deviceStore(t, s)
	a := reportActor(t, s, 201)
	in := deviceRegistration(1)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := d.ReserveRevocation(cancelled, a, in.InstallationID, 0, revocationInput(in)); err == nil {
		t.Fatal("cancelled reservation acknowledged")
	}
	if _, err := d.InspectInstallation(t.Context(), a, in.InstallationID, in.InstallationSecret); !errors.Is(err, notifications.ErrDeviceNotFound) {
		t.Fatal("cancelled reservation leaked creation", err)
	}
	in, _ = deviceBind(t, d, a, 1)
	candidate := deviceCandidate(t, d, a)
	if err := d.RevokeDevice(cancelled, in.RevocationID, in.RevocationCredential); err == nil {
		t.Fatal("cancelled revocation acknowledged")
	}
	if _, err := d.Handoff(t.Context(), a, candidate); err != nil {
		t.Fatal("cancelled revocation partially applied", err)
	}
	if err := d.RevokeDevice(t.Context(), in.RevocationID, in.RevocationCredential); err != nil {
		t.Fatal("retry failed", err)
	}
	noDeviceHandoff(t, d, a, candidate)
}
