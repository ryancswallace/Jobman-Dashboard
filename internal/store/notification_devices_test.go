package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

func deviceStore(t *testing.T, s *Store) *NotificationDeviceStore {
	t.Helper()
	p, err := notifications.NewDevicePolicy([]notifications.DeviceTopic{{Topic: "test.jobman.dashboard", Environment: "sandbox"}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := notifications.NewDeviceCipher("test-v1", map[string][]byte{"test-v1": bytes.Repeat([]byte{43}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewNotificationDeviceStore(s, p, c)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func deviceRegistration(n int) notifications.DeviceRegistration {
	return notifications.DeviceRegistration{RevocationID: fmt.Sprintf("88000000-0000-4000-8000-%012d", n), RevocationCredential: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(n + 100)}, 32)), InstallationID: fmt.Sprintf("80000000-0000-4000-8000-%012d", n), InstallationSecret: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(n)}, 32)), Label: "Synthetic phone", Topic: "test.jobman.dashboard", Environment: "sandbox", Token: fmt.Sprintf("ab%062x", n), Permission: "authorized", Enabled: true}
}
func deviceBind(t *testing.T, d *NotificationDeviceStore, a monitoring.Actor, n int) (notifications.DeviceRegistration, notifications.DeviceView) {
	t.Helper()
	in := deviceRegistration(n)
	receipt, err := d.ReserveRevocation(t.Context(), a, in.InstallationID, 0, revocationInput(in))
	if err != nil {
		t.Fatal("reserve", err)
	}
	v, err := d.Bind(t.Context(), a, receipt.Revision, in)
	if err != nil {
		t.Fatal("bind", err)
	}
	return in, v
}
func revocationInput(in notifications.DeviceRegistration) notifications.DeviceRevocationInput {
	return notifications.DeviceRevocationInput{InstallationSecret: in.InstallationSecret, RevocationID: in.RevocationID, RevocationCredential: in.RevocationCredential}
}

// Existing lifecycle tests use this explicit two-phase operation. Dedicated
// protocol tests below exercise stale CAS, reservation loss and cancellation.
func attemptDeviceBind(ctx context.Context, d *NotificationDeviceStore, a monitoring.Actor, revision int64, in notifications.DeviceRegistration, switching bool) (notifications.DeviceView, error) {
	in.RevocationID, _ = newID()
	if revision == 0 {
		if state, err := d.InspectInstallation(ctx, a, in.InstallationID, in.InstallationSecret); err == nil {
			revision = state.Revision
		}
	}
	receipt, err := d.ReserveRevocation(ctx, a, in.InstallationID, revision, revocationInput(in))
	if err != nil {
		return notifications.DeviceView{}, err
	}
	if switching {
		return d.SwitchBinding(ctx, a, receipt.Revision, in, true)
	}
	return d.Bind(ctx, a, receipt.Revision, in)
}
func deviceCandidate(t *testing.T, d *NotificationDeviceStore, a monitoring.Actor) notifications.DeviceCandidate {
	t.Helper()
	list, err := d.Candidates(t.Context(), a)
	if err != nil || len(list) != 1 {
		t.Fatal("expected one delivery candidate", err, len(list))
	}
	return list[0]
}
func noDeviceHandoff(t *testing.T, d *NotificationDeviceStore, a monitoring.Actor, c notifications.DeviceCandidate) {
	t.Helper()
	if _, err := d.Handoff(t.Context(), a, c); !errors.Is(err, notifications.ErrDeviceNotFound) {
		t.Fatal("ineligible/stale device handed off", err)
	}
}
func deviceExec(t *testing.T, s *Store, q string, args ...any) {
	t.Helper()
	if _, err := s.Pool.Exec(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationDevicesTokenLifecycleAndRemotePreferences(t *testing.T) {
	s := testDB(t)
	d := deviceStore(t, s)
	ctx := t.Context()
	alice := reportActor(t, s, 201)
	in, v := deviceBind(t, d, alice, 1)
	candidate := deviceCandidate(t, d, alice)
	handoff, err := d.Handoff(ctx, alice, candidate)
	if err != nil || handoff.Token != in.Token || v.Revision != 2 || v.TokenStatus != "current" {
		t.Fatal("initial handoff", err)
	}
	var encrypted, hash []byte
	var key string
	if err = s.Pool.QueryRow(ctx, `SELECT b.token_ciphertext,b.token_key_id,i.secret_hash FROM dashboard_notification_device_bindings b JOIN dashboard_notification_installations i ON i.current_binding_id=b.id WHERE i.id=$1::uuid`, in.InstallationID).Scan(&encrypted, &key, &hash); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(in.Token)) || bytes.Equal(hash, []byte(in.InstallationSecret)) || key != "test-v1" {
		t.Fatal("plaintext token/proof or wrong encryption key")
	}
	encoded, _ := json.Marshal(v)
	for _, private := range []string{in.Token, in.InstallationSecret, candidate.BindingID, "tokenVersion", "AccountID"} {
		if bytes.Contains(encoded, []byte(private)) {
			t.Fatal("public view leaked private device state")
		}
	}
	v, err = d.Settings(ctx, alice, in.InstallationID, v.Revision, notifications.DeviceSettings{Label: "Muted by browser", Enabled: false, Muted: true})
	if err != nil {
		t.Fatal(err)
	}
	noDeviceHandoff(t, d, alice, candidate)
	v, err = d.Refresh(ctx, alice, in.InstallationID, v.Revision, notifications.DeviceRefresh{InstallationSecret: in.InstallationSecret, Token: in.Token, Permission: "provisional"})
	if err != nil || v.Enabled || !v.Muted || v.Label != "Muted by browser" {
		t.Fatal("startup refreshed away remote settings", err)
	}
	v, err = d.Settings(ctx, alice, in.InstallationID, v.Revision, notifications.DeviceSettings{Label: v.Label, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	current := deviceCandidate(t, d, alice)
	if current.TokenVersion != candidate.TokenVersion+1 || current.BindingID != candidate.BindingID {
		t.Fatal("same-token refresh did not fence old attempt")
	}
	noDeviceHandoff(t, d, alice, candidate)
	if changed, err := d.Invalidate(ctx, candidate, nil); err != nil || changed {
		t.Fatal("old attempt invalidated new token", err)
	}
	oldTime := handoff.RegisteredAt.Add(-time.Second)
	if changed, err := d.Invalidate(ctx, current, &oldTime); err != nil || changed {
		t.Fatal("old APNs410 timestamp invalidated latest registration", err)
	}
	if changed, err := d.Invalidate(ctx, current, nil); err != nil || !changed {
		t.Fatal("current rejection not persisted", err)
	}
	if changed, err := d.Invalidate(ctx, current, nil); err != nil || changed {
		t.Fatal("duplicate rejection changed revision/audit", err)
	}
	noDeviceHandoff(t, d, alice, current)
	list, err := d.List(ctx, alice)
	if err != nil || len(list) != 1 || list[0].TokenStatus != "invalid" {
		t.Fatal("invalid token not represented", err)
	}
	v = list[0]
	in.Token = strings.Repeat("ae", 400)
	v, err = d.Refresh(ctx, alice, in.InstallationID, v.Revision, notifications.DeviceRefresh{InstallationSecret: in.InstallationSecret, Token: in.Token, Permission: "denied"})
	if err != nil {
		t.Fatal(err)
	}
	if list, err := d.Candidates(ctx, alice); err != nil || len(list) != 0 {
		t.Fatal("denied OS permission delivered", err)
	}
	v, err = d.Refresh(ctx, alice, in.InstallationID, v.Revision, notifications.DeviceRefresh{InstallationSecret: in.InstallationSecret, Token: in.Token, Permission: "ephemeral"})
	if err != nil {
		t.Fatal(err)
	}
	current = deviceCandidate(t, d, alice)
	if got, err := d.Handoff(ctx, alice, current); err != nil || got.Token != in.Token {
		t.Fatal("variable-length new token failed", err)
	}
	// Last seen is informational: an old installation remains usable with current authority.
	deviceExec(t, s, `UPDATE dashboard_notification_device_bindings SET last_seen_at=clock_timestamp()-interval '90 days'`)
	if _, err := d.Handoff(ctx, alice, current); err != nil {
		t.Fatal("invented inactivity expiry", err)
	}
	if err = d.Remove(ctx, alice, in.InstallationID, v.Revision); err != nil {
		t.Fatal(err)
	}
	noDeviceHandoff(t, d, alice, current)
	proof, err := d.InspectInstallation(ctx, alice, in.InstallationID, in.InstallationSecret)
	if err != nil || proof.HasBinding || proof.BoundToCurrentAccount {
		t.Fatal("removed binding remains attached", err)
	}
	if _, err = d.Refresh(ctx, alice, in.InstallationID, proof.Revision, notifications.DeviceRefresh{InstallationSecret: in.InstallationSecret, Token: in.Token, Permission: "authorized"}); !errors.Is(err, notifications.ErrDeviceNotFound) {
		t.Fatal("normal startup resurrected remote removal", err)
	}
	rebound, err := attemptDeviceBind(ctx, d, alice, proof.Revision, in, false)
	if err != nil || rebound.Revision != proof.Revision+2 {
		t.Fatal("explicit reactivation with proof failed", err)
	}
	next := deviceCandidate(t, d, alice)
	if next.BindingID == current.BindingID || next.TokenVersion != 1 {
		t.Fatal("reactivation reused old binding")
	}
	if changed, err := d.Invalidate(ctx, current, nil); err != nil || changed {
		t.Fatal("old binding response affected reactivation", err)
	}
	var retained int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_notification_device_bindings WHERE state<>'bound' AND token_ciphertext IS NOT NULL`).Scan(&retained); err != nil || retained != 0 {
		t.Fatal("closed binding retained token", err)
	}
}

func TestNotificationDevicesOwnershipAliasAndExplicitSwitch(t *testing.T) {
	s := testDB(t)
	d := deviceStore(t, s)
	ctx := t.Context()
	alice, bob := reportActor(t, s, 201), reportActor(t, s, 202)
	in, v := deviceBind(t, d, alice, 1)
	old := deviceCandidate(t, d, alice)
	wrong := deviceRegistration(2).InstallationSecret
	for _, id := range []string{in.InstallationID, deviceRegistration(9).InstallationID} {
		if _, err := d.InspectInstallation(ctx, bob, id, wrong); !errors.Is(err, notifications.ErrDeviceNotFound) {
			t.Fatal("unknown and wrong proof distinguishable", err)
		}
	}
	for _, rev := range []int64{1, 900} {
		if _, err := d.Settings(ctx, bob, in.InstallationID, rev, notifications.DeviceSettings{Label: "Intruder", Enabled: true}); !errors.Is(err, notifications.ErrDeviceNotFound) {
			t.Fatal("foreign settings leaked ownership/revision", err)
		}
		if err := d.Remove(ctx, bob, in.InstallationID, rev); !errors.Is(err, notifications.ErrDeviceNotFound) {
			t.Fatal("foreign removal leaked revision", err)
		}
	}
	inspect, err := d.InspectInstallation(ctx, bob, in.InstallationID, in.InstallationSecret)
	if err != nil || !inspect.HasBinding || inspect.BoundToCurrentAccount || inspect.Revision != v.Revision {
		t.Fatal("safe account-switch inspection", err)
	}
	raw, _ := json.Marshal(inspect)
	for _, private := range []string{alice.Account.ID, alice.DirectoryID, alice.Issuer, old.BindingID, in.Label} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("inspect disclosed previous owner")
		}
	}
	if _, err = d.SwitchBinding(ctx, bob, v.Revision, in, false); !errors.Is(err, notifications.ErrDeviceInvalid) {
		t.Fatal("unconfirmed account switch", err)
	}
	if _, err = d.Bind(ctx, bob, v.Revision, in); !errors.Is(err, notifications.ErrDeviceConflict) {
		t.Fatal("ordinary bind implicitly switched account", err)
	}
	bad := in
	bad.InstallationSecret = wrong
	if _, err = d.SwitchBinding(ctx, bob, v.Revision, bad, true); !errors.Is(err, notifications.ErrDeviceNotFound) {
		t.Fatal("UUID alone claimed installation", err)
	}
	// Old credentials may already be disabled; only the new verified actor and Keychain proof authorize the explicit switch.
	deviceExec(t, s, `UPDATE dashboard_accounts SET disabled_at=clock_timestamp() WHERE id=$1::uuid`, alice.Account.ID)
	switched, err := attemptDeviceBind(ctx, d, bob, v.Revision, in, true)
	if err != nil || switched.Revision != v.Revision+2 {
		t.Fatal("explicit switch required expired old credentials", err)
	}
	if _, err = d.Handoff(ctx, alice, old); !errors.Is(err, monitoring.ErrForbidden) {
		t.Fatal("disabled old actor retained handoff", err)
	}
	current := deviceCandidate(t, d, bob)
	if current.BindingID == old.BindingID || current.AccountID != bob.Account.ID {
		t.Fatal("switch did not replace private binding")
	}
	if changed, err := d.Invalidate(ctx, old, nil); err != nil || changed {
		t.Fatal("old-account APNs response crossed switch", err)
	}
	alias, err := s.ResolveIdentity(ctx, auth.Identity{Issuer: "https://second-synthetic.example", Subject: "device-current-alias", DirectoryID: bob.DirectoryID, DisplayName: "Synthetic"})
	if err != nil || alias.Account.ID != bob.Account.ID {
		t.Fatal(err)
	}
	deviceExec(t, s, `DELETE FROM dashboard_identity_aliases WHERE issuer=$1 AND subject=$2`, bob.Issuer, bob.Subject)
	if _, err = d.List(ctx, bob); !errors.Is(err, monitoring.ErrForbidden) {
		t.Fatal("old alias retained devices", err)
	}
	if _, err = d.Handoff(ctx, alias, current); err != nil {
		t.Fatal("new verified alias lost immutable account binding", err)
	}
	if err = d.Detach(ctx, alias, in.InstallationID, switched.Revision, wrong); !errors.Is(err, notifications.ErrDeviceNotFound) {
		t.Fatal("detach accepted wrong proof", err)
	}
	if err = d.Detach(ctx, alias, in.InstallationID, switched.Revision, in.InstallationSecret); err != nil {
		t.Fatal(err)
	}
	if items, err := d.List(ctx, alias); err != nil || len(items) != 0 {
		t.Fatal("detached device listed", err)
	}
}

func TestNotificationDevicesAtomicConflictAndConcurrentCAS(t *testing.T) {
	s := testDB(t)
	d := deviceStore(t, s)
	ctx := t.Context()
	alice, bob := reportActor(t, s, 201), reportActor(t, s, 202)
	in, v := deviceBind(t, d, alice, 1)
	other, _ := deviceBind(t, d, bob, 2)
	old := deviceCandidate(t, d, alice)
	receipt, err := d.ReserveRevocation(ctx, bob, in.InstallationID, v.Revision, notifications.DeviceRevocationInput{InstallationSecret: in.InstallationSecret, RevocationID: deviceRegistration(3).RevocationID, RevocationCredential: in.RevocationCredential})
	if err != nil {
		t.Fatal(err)
	}
	in.RevocationID = receipt.RevocationID
	v.Revision = receipt.Revision
	conflict := in
	conflict.Token = other.Token
	if _, err := d.SwitchBinding(ctx, bob, v.Revision, conflict, true); !errors.Is(err, notifications.ErrDeviceConflict) {
		t.Fatal("duplicate token accepted across bindings", err)
	}
	if got, err := d.Handoff(ctx, alice, old); err != nil || got.Token != in.Token {
		t.Fatal("failed switch partially detached old account", err)
	}
	deviceExec(t, s, `CREATE FUNCTION reject_device_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='notification_device.bound' THEN RAISE EXCEPTION 'synthetic device audit failure'; END IF; RETURN NEW; END$$; CREATE TRIGGER reject_device_audit BEFORE INSERT ON dashboard_audit FOR EACH ROW EXECUTE FUNCTION reject_device_audit()`)
	if _, err := d.SwitchBinding(ctx, bob, v.Revision, in, true); err == nil {
		t.Fatal("injected audit failure ignored")
	}
	if _, err := d.Handoff(ctx, alice, old); err != nil {
		t.Fatal("late transaction failure partially transferred binding", err)
	}
	deviceExec(t, s, `DROP TRIGGER reject_device_audit ON dashboard_audit`)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := d.Settings(ctx, alice, in.InstallationID, v.Revision, notifications.DeviceSettings{Label: "Concurrent rename", Enabled: true})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	won := 0
	for err := range results {
		if err == nil {
			won++
		} else if !errors.Is(err, notifications.ErrDeviceConflict) {
			t.Fatal("unexpected CAS result", err)
		}
	}
	if won != 1 {
		t.Fatal("CAS winner count", won)
	}
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_audit WHERE action='notification_device.settings_changed'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed CAS audited or repeated mutation", err)
	}
}

func TestNotificationDevicesKeyRotationAndOperatorAllowlist(t *testing.T) {
	s := testDB(t)
	d := deviceStore(t, s)
	ctx := t.Context()
	alice := reportActor(t, s, 201)
	in, v := deviceBind(t, d, alice, 1)
	candidate := deviceCandidate(t, d, alice)
	cipher, err := notifications.NewDeviceCipher("test-v2", map[string][]byte{"test-v1": bytes.Repeat([]byte{43}, 32), "test-v2": bytes.Repeat([]byte{79}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := NewNotificationDeviceStore(s, d.policy, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rotated.Handoff(ctx, alice, candidate); err != nil {
		t.Fatal("previous read key failed", err)
	}
	v, err = rotated.Refresh(ctx, alice, in.InstallationID, v.Revision, notifications.DeviceRefresh{InstallationSecret: in.InstallationSecret, Token: in.Token, Permission: "authorized"})
	if err != nil {
		t.Fatal(err)
	}
	candidate = deviceCandidate(t, rotated, alice)
	if _, err = d.Handoff(ctx, alice, candidate); !errors.Is(err, notifications.ErrDeviceUnavailable) {
		t.Fatal("unknown token key did not fail closed", err)
	}
	restricted, _ := notifications.NewDevicePolicy([]notifications.DeviceTopic{{Topic: "other.jobman.dashboard", Environment: "sandbox"}})
	other, err := NewNotificationDeviceStore(s, restricted, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if candidates, err := other.Candidates(ctx, alice); err != nil || len(candidates) != 0 {
		t.Fatal("removed topic still eligible", err)
	}
	noDeviceHandoff(t, other, alice, candidate)
	if _, err = other.Refresh(ctx, alice, in.InstallationID, v.Revision, notifications.DeviceRefresh{InstallationSecret: in.InstallationSecret, Token: in.Token, Permission: "authorized"}); !errors.Is(err, notifications.ErrDeviceConflict) {
		t.Fatal("disallowed-topic refresh accepted", err)
	}
	if err = other.Remove(ctx, alice, in.InstallationID, v.Revision); err != nil {
		t.Fatal("operator allowlist removal blocked safe removal", err)
	}
}

func TestNotificationDevicesAdmissionAndStops(t *testing.T) {
	t.Run("creation rate and retained creator", func(t *testing.T) {
		s := testDB(t)
		d := deviceStore(t, s)
		a := reportActor(t, s, 201)
		ctx := t.Context()
		var in notifications.DeviceRegistration
		var v notifications.DeviceView
		for n := 1; n <= notifications.MaximumInstallationCreatesPerMinute; n++ {
			in, v = deviceBind(t, d, a, n)
		}
		if _, err := attemptDeviceBind(ctx, d, a, 0, deviceRegistration(6), false); !errors.Is(err, notifications.ErrDeviceRateLimited) {
			t.Fatal("new installation rate unbounded", err)
		}
		if err := d.Remove(ctx, a, in.InstallationID, v.Revision); err != nil {
			t.Fatal("creation rate blocked removal", err)
		}
		deviceExec(t, s, `UPDATE dashboard_notification_installations SET created_at=clock_timestamp()-interval '2 minutes'`)
		deviceExec(t, s, `INSERT INTO dashboard_notification_installations(id,creator_account_id,secret_hash,created_at) SELECT ('81000000-0000-4000-8000-'||lpad(n::text,12,'0'))::uuid,$1::uuid,decode(repeat('ab',32),'hex'),clock_timestamp()-interval '2 minutes' FROM generate_series(1,$2::integer) n`, a.Account.ID, notifications.MaximumCreatedInstallations-5)
		if _, err := attemptDeviceBind(ctx, d, a, 0, deviceRegistration(6), false); !errors.Is(err, notifications.ErrDeviceCapacity) {
			t.Fatal("retained original creator quota not applied", err)
		}
		// A former installation creator's quota still counts after ownership transfer.
		bob := reportActor(t, s, 202)
		remaining := deviceRegistration(1)
		if _, err := attemptDeviceBind(ctx, d, bob, 2, remaining, true); err != nil {
			t.Fatal(err)
		}
		if _, err := attemptDeviceBind(ctx, d, a, 0, deviceRegistration(7), false); !errors.Is(err, notifications.ErrDeviceCapacity) {
			t.Fatal("transfer freed original creator quota", err)
		}
	})
	t.Run("account bound cap and safe removal", func(t *testing.T) {
		s := testDB(t)
		d := deviceStore(t, s)
		a := reportActor(t, s, 201)
		ctx := t.Context()
		var in notifications.DeviceRegistration
		var v notifications.DeviceView
		for n := 1; n <= notifications.MaximumBoundDevices; n++ {
			if n%5 == 1 {
				deviceExec(t, s, `UPDATE dashboard_notification_installations SET created_at=clock_timestamp()-interval '2 minutes'`)
			}
			in, v = deviceBind(t, d, a, n)
		}
		deviceExec(t, s, `UPDATE dashboard_notification_installations SET created_at=clock_timestamp()-interval '2 minutes'`)
		if _, err := attemptDeviceBind(ctx, d, a, 0, deviceRegistration(51), false); !errors.Is(err, notifications.ErrDeviceCapacity) {
			t.Fatal("bound cap not applied", err)
		}
		list, err := d.List(ctx, a)
		if err != nil || len(list) != 50 {
			t.Fatal("bounded complete device list", err)
		}
		if err = d.Remove(ctx, a, in.InstallationID, v.Revision); err != nil {
			t.Fatal("capacity blocked removal", err)
		}
		if _, err = attemptDeviceBind(ctx, d, a, 0, deviceRegistration(51), false); err != nil {
			t.Fatal("removal did not free bound slot", err)
		}
	})
	t.Run("binding rate and explicit stops", func(t *testing.T) {
		s := testDB(t)
		d := deviceStore(t, s)
		a, b := reportActor(t, s, 201), reportActor(t, s, 202)
		ctx := t.Context()
		in, v := deviceBind(t, d, a, 1)
		owner := a
		for n := 1; n < notifications.MaximumBindingChangesPerMinute; n++ {
			next := b
			if owner.Account.ID == b.Account.ID {
				next = a
			}
			var err error
			v, err = attemptDeviceBind(ctx, d, next, v.Revision, in, true)
			if err != nil {
				t.Fatal(err)
			}
			owner = next
		}
		next := a
		if owner.Account.ID == a.Account.ID {
			next = b
		}
		if _, err := attemptDeviceBind(ctx, d, next, v.Revision, in, true); !errors.Is(err, notifications.ErrDeviceRateLimited) {
			t.Fatal("switch rate not applied", err)
		}
		stateBeforeStop, inspectErr := d.InspectInstallation(ctx, owner, in.InstallationID, in.InstallationSecret)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		if err := d.Detach(ctx, owner, in.InstallationID, stateBeforeStop.Revision, in.InstallationSecret); err != nil {
			t.Fatal("rate blocked detach", err)
		}
		state, err := d.InspectInstallation(ctx, owner, in.InstallationID, in.InstallationSecret)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = attemptDeviceBind(ctx, d, owner, state.Revision, in, false); !errors.Is(err, notifications.ErrDeviceRateLimited) {
			t.Fatal("explicit rebind bypassed binding rate", err)
		}
	})
	t.Run("retained binding history and safe disable", func(t *testing.T) {
		s := testDB(t)
		d := deviceStore(t, s)
		a := reportActor(t, s, 201)
		ctx := t.Context()
		in, v := deviceBind(t, d, a, 1)
		deviceExec(t, s, `INSERT INTO dashboard_notification_device_bindings(id,installation_id,account_id,label,topic,environment,state,enabled,muted,permission,token_version,token_registered_at,closed_at,created_at) SELECT ('82000000-0000-4000-8000-'||lpad(n::text,12,'0'))::uuid,$1::uuid,$2::uuid,'Synthetic old phone','test.jobman.dashboard','sandbox','detached',false,false,'denied',1,clock_timestamp()-interval '2 minutes',clock_timestamp()-interval '2 minutes',clock_timestamp()-interval '2 minutes' FROM generate_series(1,$3::integer) n`, in.InstallationID, a.Account.ID, notifications.MaximumInstallationBindings-1)
		var err error
		v, err = d.Settings(ctx, a, in.InstallationID, v.Revision, notifications.DeviceSettings{Label: in.Label, Enabled: false})
		if err != nil {
			t.Fatal("retained quota blocked disable", err)
		}
		if err = d.Remove(ctx, a, in.InstallationID, v.Revision); err != nil {
			t.Fatal("retained quota blocked removal", err)
		}
		state, err := d.InspectInstallation(ctx, a, in.InstallationID, in.InstallationSecret)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = attemptDeviceBind(ctx, d, a, state.Revision, in, false); !errors.Is(err, notifications.ErrDeviceCapacity) {
			t.Fatal("binding history was silently deleted or exceeded", err)
		}
		var count int
		if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_notification_device_bindings WHERE installation_id=$1::uuid`, in.InstallationID).Scan(&count); err != nil || count != 100 {
			t.Fatal("history bound changed", err)
		}
	})
	t.Run("global installation cap", func(t *testing.T) {
		s := testDB(t)
		d := deviceStore(t, s)
		a, b := reportActor(t, s, 201), reportActor(t, s, 202)
		ctx := t.Context()
		deviceExec(t, s, `INSERT INTO dashboard_notification_installations(id,creator_account_id,secret_hash,created_at) SELECT ('83000000-0000-4000-8000-'||lpad(n::text,12,'0'))::uuid,$1::uuid,decode(repeat('ab',32),'hex'),clock_timestamp()-interval '2 minutes' FROM generate_series(1,$2::integer) n`, a.Account.ID, notifications.MaximumInstallations)
		if _, err := attemptDeviceBind(ctx, d, b, 0, deviceRegistration(1), false); !errors.Is(err, notifications.ErrDeviceCapacity) {
			t.Fatal("global retained metadata cap not applied", err)
		}
	})
}

func TestNotificationDevicesMutationRateCannotDelayStops(t *testing.T) {
	s := testDB(t)
	d := deviceStore(t, s)
	a := reportActor(t, s, 201)
	ctx := t.Context()
	in, v := deviceBind(t, d, a, 1)
	var auditBefore int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_audit`).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	same, err := d.Settings(ctx, a, in.InstallationID, v.Revision, notifications.DeviceSettings{Label: in.Label, Enabled: true})
	if err != nil || same.Revision != v.Revision {
		t.Fatal("no-op settings changed revision", err)
	}
	var auditAfter int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_audit`).Scan(&auditAfter); err != nil || auditBefore != auditAfter {
		t.Fatal("no-op settings added audit", err)
	}
	for range notifications.MaximumDeviceMutationsPerMinute - 2 {
		v, err = d.Refresh(ctx, a, in.InstallationID, v.Revision, notifications.DeviceRefresh{InstallationSecret: in.InstallationSecret, Token: in.Token, Permission: "authorized"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = d.Refresh(ctx, a, in.InstallationID, v.Revision, notifications.DeviceRefresh{InstallationSecret: in.InstallationSecret, Token: in.Token, Permission: "authorized"}); !errors.Is(err, notifications.ErrDeviceRateLimited) {
		t.Fatal("refresh rate unbounded", err)
	}
	if _, err = d.Settings(ctx, a, in.InstallationID, v.Revision, notifications.DeviceSettings{Label: "Repeated rename", Enabled: true}); !errors.Is(err, notifications.ErrDeviceRateLimited) {
		t.Fatal("rename bypassed shared mutation bound", err)
	}
	v, err = d.Settings(ctx, a, in.InstallationID, v.Revision, notifications.DeviceSettings{Label: in.Label, Enabled: false, Muted: true})
	if err != nil {
		t.Fatal("rate blocked real stop/mute", err)
	}
	if _, err = d.Settings(ctx, a, in.InstallationID, v.Revision, notifications.DeviceSettings{Label: in.Label, Enabled: true}); !errors.Is(err, notifications.ErrDeviceRateLimited) {
		t.Fatal("reenable bypassed rate", err)
	}
	v, err = d.Refresh(ctx, a, in.InstallationID, v.Revision, notifications.DeviceRefresh{InstallationSecret: in.InstallationSecret, Token: in.Token, Permission: "denied"})
	if err != nil {
		t.Fatal("rate delayed OS-permission loss", err)
	}
	deviceExec(t, s, `UPDATE dashboard_notification_installations SET mutation_window_started_at=clock_timestamp()-interval '2 minutes'`)
	v, err = d.Settings(ctx, a, in.InstallationID, v.Revision, notifications.DeviceSettings{Label: in.Label, Enabled: true})
	if err != nil {
		t.Fatal("new admission window did not open", err)
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT mutation_count FROM dashboard_notification_installations WHERE id=$1::uuid`, in.InstallationID).Scan(&count); err != nil || count != 1 {
		t.Fatal("fixed counter reset", err)
	}
	deviceExec(t, s, `UPDATE dashboard_notification_installations SET mutation_count=30`)
	if err = d.Remove(ctx, a, in.InstallationID, v.Revision); err != nil {
		t.Fatal("rate blocked remote removal", err)
	}
}

func TestNotificationDevicesMixedStopsCannotBypassMutationAdmission(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after notifications.DeviceSettings
	}{
		{"disable plus unmute", notifications.DeviceSettings{Label: "Synthetic phone", Enabled: true, Muted: true}, notifications.DeviceSettings{Label: "Synthetic phone", Enabled: false, Muted: false}},
		{"mute plus reenable", notifications.DeviceSettings{Label: "Synthetic phone", Enabled: false, Muted: false}, notifications.DeviceSettings{Label: "Synthetic phone", Enabled: true, Muted: true}},
		{"stop plus rename", notifications.DeviceSettings{Label: "Synthetic phone", Enabled: true, Muted: false}, notifications.DeviceSettings{Label: "Renamed", Enabled: false, Muted: false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testDB(t)
			d := deviceStore(t, s)
			a := reportActor(t, s, 201)
			ctx := t.Context()
			in := deviceRegistration(1)
			in.Enabled = tc.before.Enabled
			in.Muted = tc.before.Muted
			v, err := attemptDeviceBind(ctx, d, a, 0, in, false)
			if err != nil {
				t.Fatal(err)
			}
			deviceExec(t, s, `UPDATE dashboard_notification_installations SET mutation_count=30`)
			var auditBefore int
			if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_audit`).Scan(&auditBefore); err != nil {
				t.Fatal(err)
			}
			if _, err = d.Settings(ctx, a, in.InstallationID, v.Revision, tc.after); !errors.Is(err, notifications.ErrDeviceRateLimited) {
				t.Fatal("mixed stop/reopening bypassed mutation bound", err)
			}
			list, err := d.List(ctx, a)
			if err != nil || len(list) != 1 || list[0].Revision != v.Revision || list[0].Enabled != v.Enabled || list[0].Muted != v.Muted {
				t.Fatal("rejected mixed change was partially applied", err)
			}
			var auditAfter int
			if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_audit`).Scan(&auditAfter); err != nil || auditAfter != auditBefore {
				t.Fatal("rejected mixed change appended audit", err)
			}
			pure := tc.before
			pure.Enabled = false
			pure.Muted = true
			if _, err = d.Settings(ctx, a, in.InstallationID, v.Revision, pure); err != nil {
				t.Fatal("pure monotonic stop blocked", err)
			}
		})
	}
}
