package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

type activationSource struct {
	cp                  events.Checkpoint
	unavailable, denied bool
}

func (s *activationSource) ID() string             { return s.cp.DeploymentID }
func (s *activationSource) SourceID() string       { return s.ID() }
func (s *activationSource) NamespaceIDs() []string { return slices.Clone(s.cp.NamespaceIDs) }
func (s *activationSource) Checkpoint(context.Context) (events.Checkpoint, error) {
	if s.unavailable {
		return events.Checkpoint{}, events.ErrUnavailable
	}
	return s.cp, nil
}
func (s *activationSource) Read(context.Context, string, int) (events.Page, error) {
	return events.Page{}, events.ErrUnavailable
}
func (s *activationSource) Discover(context.Context, monitoring.Actor) (monitoring.Discovery, error) {
	d := monitoring.Discovery{InstanceID: s.cp.ControlInstanceID, RecoveryEpoch: s.cp.RecoveryEpoch, PrincipalID: "70000000-0000-4000-8000-000000000001", Deployment: api.Deployment{ID: s.ID(), Status: "available", Namespaces: []api.Namespace{}}}
	if !s.denied {
		now := time.Now().UTC()
		for _, id := range s.cp.NamespaceIDs {
			d.Deployment.Namespaces = append(d.Deployment.Namespaces, api.Namespace{ID: id, AuthorizationVersion: "1", AuthorizationCheckedAt: now, AuthorizationExpiresAt: now.Add(time.Minute), Capabilities: []string{"namespace.read", "jobs.read"}})
		}
	}
	return d, nil
}
func (s *activationSource) Job(_ context.Context, _ monitoring.Actor, scope api.Scope, id string) (api.Job, error) {
	return api.Job{ID: id, Scope: scope}, nil
}

func pendingSetup(t *testing.T) (*evaluationFixture, *NotificationActivationStore, *notifications.RuleService, *activationSource, monitoring.Actor, notifications.Rule) {
	t.Helper()
	x := evaluationSetup(t)
	a := reportActor(t, x.s, 901)
	source := &activationSource{cp: x.cp, unavailable: true}
	service, err := notifications.NewRuleService(x.s, []notifications.RuleSource{source}, []events.Source{source}, x.s)
	if err != nil {
		t.Fatal(err)
	}
	v, err := service.Create(t.Context(), a, notificationInput(x.cp))
	if err != nil {
		t.Fatal(err)
	}
	r, err := x.s.NotificationRule(t.Context(), a, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewNotificationActivationStore(x.s, a.Issuer)
	if err != nil {
		t.Fatal(err)
	}
	if r.Activation[0].Status != notifications.ActivationPending || x.count(t, `SELECT count(*) FROM dashboard_notification_activation_work`) != 1 {
		t.Fatal("pending intent not durably queued")
	}
	return x, repo, service, source, a, r
}

func TestNotificationPendingActivationRecoversOnlyAfterFreshSourceBoundary(t *testing.T) {
	x, repo, service, source, a, r := pendingSetup(t)
	ctx := t.Context()
	worker, err := notifications.NewActivationWorker(repo, service)
	if err != nil {
		t.Fatal(err)
	}
	if progress, err := worker.Step(ctx); err != nil || !progress {
		t.Fatal(progress, err)
	}
	if _, err = repo.ClaimNotificationActivation(ctx); !errors.Is(err, notifications.ErrEvaluationEmpty) {
		t.Fatal("outage retry ignored delay", err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_rule_versions`); n != 1 {
		t.Fatal("outage consumed history", n)
	}
	source.unavailable = false
	source.cp.AsOf = time.Now().UTC()
	deviceExec(t, x.s, `UPDATE dashboard_notification_activation_work SET next_attempt_at=clock_timestamp()`)
	if progress, err := worker.Step(ctx); err != nil || !progress {
		t.Fatal(progress, err)
	}
	current, err := x.s.NotificationRule(ctx, a, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != 2 || current.Activation[0].Status != notifications.ActivationActive || current.Activation[0].ID == r.Activation[0].ID || !current.Activation[0].Boundary.NotBefore.Equal(source.cp.AsOf) {
		t.Fatal("missing fresh activation", current)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_activation_work`); n != 0 {
		t.Fatal("resolved work leaked")
	}
	e := sourceEvent(x.cp, 1)
	e.Outcome = "failure"
	e.RecordedAt = source.cp.AsOf.Add(-time.Second)
	if m, err := notifications.MatchVersion(current, e); err != nil || m != nil {
		t.Fatal("outage-period event was backfilled", m, err)
	}
}

func TestNotificationPendingActivationNeverResurrectsLostScope(t *testing.T) {
	for _, mode := range []string{"recorded-denial", "scope-removal", "fresh-namespace-denial", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			x, repo, service, source, a, r := pendingSetup(t)
			ctx := t.Context()
			source.unavailable = false
			switch mode {
			case "recorded-denial":
				if err := x.s.DisableNotificationActivations(ctx, a, r.ID, r.Revision, []string{r.Activation[0].ID}); err != nil {
					t.Fatal(err)
				}
			case "scope-removal":
				deviceExec(t, x.s, `INSERT INTO dashboard_notification_scope_revocations(deployment_id,namespace_id,revoked_through) VALUES($1::uuid,$2::uuid,clock_timestamp())`, source.ID(), source.cp.NamespaceIDs[0])
			case "fresh-namespace-denial":
				source.denied = true
			case "stopped":
				if _, err := service.SetEnabled(ctx, a, r.ID, r.Revision, false); err != nil {
					t.Fatal(err)
				}
			}
			worker, err := notifications.NewActivationWorker(repo, service)
			if err != nil {
				t.Fatal(err)
			}
			_, err = worker.Step(ctx)
			if err != nil {
				t.Fatal(err)
			}
			source.denied = false
			deviceExec(t, x.s, `UPDATE dashboard_notification_activation_work SET next_attempt_at=clock_timestamp()`)
			_, err = worker.Step(ctx)
			if err != nil {
				t.Fatal(err)
			}
			current, err := x.s.NotificationRule(ctx, a, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range current.Activation {
				if v.Status == notifications.ActivationActive {
					t.Fatal("revoked intent reactivated")
				}
			}
			if n := x.count(t, `SELECT count(*) FROM dashboard_notification_activation_work`); n != 0 {
				t.Fatal("ineligible pending work looped")
			}
		})
	}
}

func TestNotificationPendingActivationLeaseTakeoverAndUserEditFence(t *testing.T) {
	x, repo, service, source, a, r := pendingSetup(t)
	ctx := t.Context()
	first, err := repo.ClaimNotificationActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ClaimNotificationActivation(ctx); !errors.Is(err, notifications.ErrEvaluationEmpty) {
		t.Fatal("concurrent claim accepted", err)
	}
	deviceExec(t, x.s, `UPDATE dashboard_notification_activation_work SET lease_expires_at=clock_timestamp()-interval '1 second'`)
	second, err := repo.ClaimNotificationActivation(ctx)
	if err != nil || second.LeaseToken == first.LeaseToken {
		t.Fatal("lease not reclaimed", err)
	}
	if err = repo.ReleaseNotificationActivation(ctx, first); err != nil {
		t.Fatal(err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_activation_work WHERE lease_token=$1::uuid`, second.LeaseToken); n != 1 {
		t.Fatal("late cleanup erased successor")
	}
	source.unavailable = false
	if _, err = service.SetEnabled(ctx, a, r.ID, r.Revision, false); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Revalidate(ctx, second.Actor, second.RuleID, second.Revision, false); !errors.Is(err, notifications.ErrConflict) {
		t.Fatal("worker bypassed intervening stop", err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_activation_work`); n != 0 {
		t.Fatal("stop left pending work")
	}
}

func TestNotificationPendingActivationMissingAliasDoesNotLoseIntent(t *testing.T) {
	x, repo, _, _, a, _ := pendingSetup(t)
	ctx := t.Context()
	deviceExec(t, x.s, `DELETE FROM dashboard_identity_aliases WHERE account_id=$1::uuid`, a.Account.ID)
	if _, err := repo.ClaimNotificationActivation(ctx); !errors.Is(err, notifications.ErrEvaluationAdvanced) {
		t.Fatal(err)
	}
	if n := x.count(t, `SELECT count(*) FROM dashboard_notification_activation_work WHERE lease_token IS NULL AND next_attempt_at>clock_timestamp()`); n != 1 {
		t.Fatal("missing alias erased or published intent")
	}
}
