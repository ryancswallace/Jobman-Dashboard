package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

func inboxSeed(t *testing.T, x *evaluationFixture, a monitoring.Actor, n int) (string, notifications.AuthorizationProof) {
	t.Helper()
	x.event(t, n)
	x.fanout(t)
	c := x.claim(t)
	if c.Actor.Account.ID != a.Account.ID {
		t.Fatal("unexpected synthetic owner")
	}
	proof := x.authorized(t, c)
	result, err := x.repo.CommitNotificationEvaluation(t.Context(), c, x.fence(t), proof)
	if err != nil || result.State != "complete" {
		t.Fatal(result, err)
	}
	return result.InboxID, proof.Proof
}
func TestNotificationInboxScopeCountsHistoryAndReadState(t *testing.T) {
	x := evaluationSetup(t)
	ctx := t.Context()
	a := reportActor(t, x.s, 720)
	rule := x.rule(t, a)
	first, proof := inboxSeed(t, x, a, 1)
	second, _ := inboxSeed(t, x, a, 2)
	q := notifications.InboxSelection{Authorities: []notifications.AuthorizationProof{proof}, Limit: 1}
	page, err := x.s.ListNotificationInbox(ctx, a, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != second || page.UnreadCount != "2" || !page.More {
		t.Fatal(page, err)
	}
	q.SnapshotAt = page.SnapshotAt
	q.After = &notifications.InboxPosition{CreatedAt: page.Items[0].CreatedAt, ID: second}
	page2, err := x.s.ListNotificationInbox(ctx, a, q)
	if err != nil || len(page2.Items) != 1 || page2.Items[0].ID != first || page2.More {
		t.Fatal(page2, err)
	}
	empty, err := x.s.ListNotificationInbox(ctx, a, notifications.InboxSelection{Authorities: []notifications.AuthorizationProof{}, Limit: 50})
	if err != nil || len(empty.Items) != 0 || empty.UnreadCount != "0" {
		t.Fatal("empty scope broadened", empty, err)
	}
	r, err := x.s.SetNotificationInboxRead(ctx, a, first, true, proof)
	if err != nil || r.ReadAt == nil {
		t.Fatal(r, err)
	}
	again, err := x.s.SetNotificationInboxRead(ctx, a, first, true, proof)
	if err != nil || !again.ReadAt.Equal(*r.ReadAt) {
		t.Fatal("read replay changed time", again, err)
	}
	unread, err := x.s.ListNotificationInbox(ctx, a, notifications.InboxSelection{Authorities: []notifications.AuthorizationProof{proof}, Unread: true, Limit: 50})
	if err != nil || len(unread.Items) != 1 || unread.Items[0].ID != second || unread.UnreadCount != "1" {
		t.Fatal(unread, err)
	}
	r, err = x.s.SetNotificationInboxRead(ctx, a, first, false, proof)
	if err != nil || r.ReadAt != nil {
		t.Fatal(r, err)
	}
	if _, err = x.s.DeleteNotificationRule(ctx, a, rule.ID, rule.Revision); err != nil {
		t.Fatal(err)
	}
	r, err = x.s.NotificationInbox(ctx, a, first)
	if err != nil || len(r.Matches) != 1 || r.Matches[0].RuleID != rule.ID || r.Matches[0].Revision != "1" || r.Matches[0].Name != rule.Name {
		t.Fatal("rule deletion changed historical match", r, err)
	}
	deviceExec(t, x.s, `UPDATE dashboard_notification_inbox SET expires_at=created_at+interval '1 microsecond' WHERE id=$1::uuid`, first)
	if _, err = x.s.NotificationInbox(ctx, a, first); !errors.Is(err, monitoring.ErrNotFound) {
		t.Fatal("expired detail returned", err)
	}
	page, err = x.s.ListNotificationInbox(ctx, a, notifications.InboxSelection{Authorities: []notifications.AuthorizationProof{proof}, Limit: 50})
	if err != nil || len(page.Items) != 1 || page.UnreadCount != "1" {
		t.Fatal("expired count retained", page, err)
	}
}
func TestNotificationInboxOwnerSourceProofAnd320Scopes(t *testing.T) {
	x := evaluationSetup(t)
	ctx := t.Context()
	a := reportActor(t, x.s, 721)
	other := reportActor(t, x.s, 722)
	x.rule(t, a)
	id, p := inboxSeed(t, x, a, 1)
	if _, err := x.s.NotificationInbox(ctx, other, id); !errors.Is(err, monitoring.ErrNotFound) {
		t.Fatal("other owner read", err)
	}
	bad := p
	bad.Namespace.NamespaceID = "40000000-0000-4000-8000-000000000099"
	if _, err := x.s.SetNotificationInboxRead(ctx, a, id, true, bad); !errors.Is(err, monitoring.ErrNotFound) {
		t.Fatal("other namespace mutation", err)
	}
	bad = p
	bad.ControlInstanceID = "30000000-0000-4000-8000-000000000099"
	if err := x.s.ValidateNotificationInboxOwner(ctx, a, []notifications.AuthorizationProof{bad}); !errors.Is(err, monitoring.ErrAuthority) {
		t.Fatal("replacement instance accepted", err)
	}
	bad = p
	bad.RecoveryEpoch = "2"
	if err := x.s.ValidateNotificationInboxOwner(ctx, a, []notifications.AuthorizationProof{bad}); !errors.Is(err, monitoring.ErrAuthority) {
		t.Fatal("mixed epoch accepted", err)
	}
	scopes := []notifications.AuthorizationProof{p}
	for n := 1; n < 320; n++ {
		v := p
		v.Namespace.NamespaceID = fmt.Sprintf("44000000-0000-4000-8000-%012d", n)
		scopes = append(scopes, v)
	}
	page, err := x.s.ListNotificationInbox(ctx, a, notifications.InboxSelection{Authorities: scopes, Limit: 50})
	if err != nil || len(page.Items) != 1 || page.UnreadCount != "1" {
		t.Fatal("320 authorized scopes", page, err)
	}
	bad = p
	bad.ExpiresAt = time.Now().Add(-time.Second)
	if _, err = x.s.ListNotificationInbox(ctx, a, notifications.InboxSelection{Authorities: []notifications.AuthorizationProof{bad}, Limit: 50}); !errors.Is(err, monitoring.ErrAuthority) {
		t.Fatal("expired proof read", err)
	}
	deviceExec(t, x.s, `DELETE FROM dashboard_identity_aliases WHERE account_id=$1::uuid`, a.Account.ID)
	if err = x.s.ValidateNotificationInboxOwner(ctx, a, []notifications.AuthorizationProof{p}); !errors.Is(err, monitoring.ErrForbidden) {
		t.Fatal("removed alias accepted", err)
	}
}
func TestNotificationInboxProofExpiryAfterMutationRollsBack(t *testing.T) {
	x := evaluationSetup(t)
	ctx := t.Context()
	a := reportActor(t, x.s, 723)
	x.rule(t, a)
	id, p := inboxSeed(t, x, a, 1)
	deviceExec(t, x.s, `CREATE SEQUENCE inbox_expiry_probe; CREATE FUNCTION delay_test_read_state() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM nextval('inbox_expiry_probe'); PERFORM pg_sleep(1.1); RETURN NEW; END $$; CREATE TRIGGER delay_test_read_state BEFORE UPDATE OF read_at ON dashboard_notification_inbox FOR EACH ROW EXECUTE FUNCTION delay_test_read_state()`)
	if err := x.s.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&p.CheckedAt); err != nil {
		t.Fatal(err)
	}
	p.ExpiresAt = p.CheckedAt.Add(time.Second)
	if _, err := x.s.SetNotificationInboxRead(ctx, a, id, true, p); !errors.Is(err, monitoring.ErrAuthority) {
		t.Fatal("expired proof committed read mutation", err)
	}
	var reached bool
	if err := x.s.Pool.QueryRow(ctx, `SELECT is_called FROM inbox_expiry_probe`).Scan(&reached); err != nil || !reached {
		t.Fatal("expiry fence was not reached", err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_inbox WHERE id=$1::uuid AND read_at IS NULL`, id); n != 1 {
		t.Fatal("read mutation survived expired proof", n)
	}
}

func TestNotificationInboxSourceQualifiedTies(t *testing.T) {
	x := evaluationSetup(t)
	ctx := t.Context()
	a := reportActor(t, x.s, 724)
	x.rule(t, a)
	first, p1 := inboxSeed(t, x, a, 1)
	cp := x.cp
	cp.DeploymentID = "20000000-0000-4000-8000-000000000002"
	cp.ControlInstanceID = "30000000-0000-4000-8000-000000000002"
	feed := initializedFeed(t, x.s, cp)
	if err := x.s.VerifySourceIdentity(ctx, cp.DeploymentID, cp.ControlInstanceID, cp.RecoveryEpoch, 1); err != nil {
		t.Fatal(err)
	}
	y := &evaluationFixture{x.s, x.repo, cp, feed, x.now}
	y.rule(t, a)
	second, p2 := inboxSeed(t, y, a, 1)
	deviceExec(t, x.s, `UPDATE dashboard_notification_inbox SET created_at=$1::timestamptz,expires_at=$1::timestamptz+interval '30 days'`, x.now)
	page, err := x.s.ListNotificationInbox(ctx, a, notifications.InboxSelection{Authorities: []notifications.AuthorizationProof{p1, p2}, Limit: 1})
	if err != nil || len(page.Items) != 1 || !page.More || page.UnreadCount != "2" {
		t.Fatal(page, err)
	}
	high, low := first, second
	if second > first {
		high, low = second, first
	}
	if page.Items[0].ID != high {
		t.Fatal("equal-time UUID order", page.Items[0].ID)
	}
	after := &notifications.InboxPosition{CreatedAt: page.Items[0].CreatedAt, ID: high}
	next, err := x.s.ListNotificationInbox(ctx, a, notifications.InboxSelection{Authorities: []notifications.AuthorizationProof{p1, p2}, SnapshotAt: page.SnapshotAt, After: after, Limit: 1})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != low || next.More {
		t.Fatal("tie continuation duplicated/lost row", next, err)
	}
	for _, proof := range []notifications.AuthorizationProof{p1, p2} {
		only, err := x.s.ListNotificationInbox(ctx, a, notifications.InboxSelection{Authorities: []notifications.AuthorizationProof{proof}, Limit: 50})
		if err != nil || len(only.Items) != 1 || only.UnreadCount != "1" || only.Items[0].Event.DeploymentID != proof.Namespace.DeploymentID {
			t.Fatal("cross-source namespace/UUID collision", only, err)
		}
	}
}
