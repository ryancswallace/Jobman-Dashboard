package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/operations"
)

func TestOperationalStatusSourceIsolationQueuesAndHold(t *testing.T) {
	x := deliverySetup(t)
	ctx := t.Context()
	x.event(t, 2)
	x.fanout(t)
	x.claim(t)    // One leased account evaluation for event two.
	x.event(t, 3) // No fanout yet for event three.
	x.claimDelivery(t)
	deviceExec(t, x.s, `UPDATE dashboard_source_events SET first_seen_at=clock_timestamp()-interval '2 minutes' WHERE processed_at IS NULL`)
	deviceExec(t, x.s, `INSERT INTO dashboard_notification_provider_health(topic,environment,retry_not_before,last_failure_at,last_reason,failures) VALUES('private-topic-must-not-escape','sandbox',clock_timestamp()+interval '1 minute',clock_timestamp()-interval '1 second','private-reason-must-not-escape',9007199254740993)`)
	c := x.cp
	oldest := c.AsOf.Add(-time.Minute)
	c.BacklogCount, c.OldestUnpublishedRecordedAt = "1", &oldest
	raw, _ := json.Marshal(c)
	deviceExec(t, x.s, `UPDATE dashboard_event_feeds SET checkpoint=$2 WHERE deployment_id=$1::uuid`, c.DeploymentID, raw)
	hidden := c
	hidden.DeploymentID = "20000000-0000-4000-8000-000000000002"
	initializedFeed(t, x.s, hidden)
	cutoff := x.now.Add(-time.Hour)
	if _, err := x.s.HoldNotifications(ctx, 1, &cutoff); err != nil {
		t.Fatal(err)
	}
	missing := "20000000-0000-4000-8000-000000000003"
	s, err := x.s.OperationalStatus(ctx, []string{missing, c.DeploymentID})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Sources) != 2 || s.Sources[0].DeploymentID != c.DeploymentID || s.Sources[1].State != "uninitialized" || s.Sources[1].Feed != nil {
		t.Fatal("configured source isolation failed", s.Sources)
	}
	source := s.Sources[0]
	if source.State != "paused" || source.Feed.LastError != "invalid_cursor" || source.Feed.RetainedEvents != "3" || source.Events.Pending != "2" || source.Fanout.Pending != "1" || source.Events.OldestAt == nil || source.Fanout.OldestAt == nil {
		t.Fatal("feed/fanout counts differ", source)
	}
	if source.Evaluation.Pending != "1" || source.Evaluation.Leased != "1" || source.Evaluation.Due != "0" || source.Delivery.Pending != "1" || source.Delivery.Leased != "1" || source.Delivery.Due != "0" {
		t.Fatal("lease/due counts differ", source.Evaluation, source.Delivery)
	}
	if !s.Hold.Held || s.Hold.Generation != "2" || s.Hold.RestoreRecordedThrough == nil || !s.Hold.RestoreRecordedThrough.Equal(cutoff) || source.Feed.SuppressRecordedThrough == nil {
		t.Fatal("hold/cutoff absent", s.Hold)
	}
	if source.Feed.Checkpoint.UnpublishedEvents != "1" || !source.Feed.Checkpoint.AsOf.Equal(c.AsOf) || s.Provider.RecordedFailures != "9007199254740993" || s.Provider.BackingOff != "1" || s.Provider.RetryNotBefore == nil {
		t.Fatal("stored observation lost exactness", s)
	}
	var rendered bytes.Buffer
	if err = operations.WriteStatus(ctx, x.s, []operations.Deployment{{ID: c.DeploymentID}}, "json", &rendered); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-topic", "private-reason", c.HeadCursor, c.OldestCursor, c.ControlInstanceID, c.NamespaceIDs[0], x.actor.Account.ID, x.registration.Token, hidden.DeploymentID} {
		if strings.Contains(rendered.String(), private) {
			t.Fatalf("private value escaped operator projection: field length %d", len(private))
		}
	}
	deviceExec(t, x.s, `UPDATE dashboard_notification_evaluations SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE state='pending'`)
	deviceExec(t, x.s, `UPDATE dashboard_notification_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE state='pending'`)
	s, err = x.s.OperationalStatus(ctx, []string{c.DeploymentID})
	if err != nil || s.Sources[0].Evaluation.Due != "1" || s.Sources[0].Evaluation.Leased != "0" || s.Sources[0].Delivery.Due != "1" {
		t.Fatal("expired leases do not become due", s, err)
	}
}

func TestOperationalStatusActivationAgeAndEmptySelection(t *testing.T) {
	x, repo, _, _, _, rule := pendingSetup(t)
	s, err := x.s.OperationalStatus(t.Context(), nil)
	if err != nil || len(s.Sources) != 0 || s.Activation.Pending != "1" || s.Activation.Due != "1" || s.Activation.Leased != "0" || s.Activation.OldestAt == nil || !s.Activation.OldestAt.Equal(rule.UpdatedAt) {
		t.Fatal("global activation snapshot or explicit empty source selection wrong", s, err)
	}
	if _, err = repo.ClaimNotificationActivation(t.Context()); err != nil {
		t.Fatal(err)
	}
	s, err = x.s.OperationalStatus(t.Context(), []string{x.cp.DeploymentID})
	if err != nil || s.Activation.Leased != "1" || s.Activation.Due != "0" {
		t.Fatal("activation lease absent", s, err)
	}
}

func TestOperationalStatusInvalidCheckpointAndBoundedFailure(t *testing.T) {
	s := testDB(t)
	cp := eventCheckpoint()
	initializedFeed(t, s, cp)
	for _, raw := range [][]byte{[]byte(`{"unknown":"private-value"}`), []byte(`not-json`)} {
		deviceExec(t, s, `UPDATE dashboard_event_feeds SET checkpoint=$1`, raw)
		result, err := s.OperationalStatus(t.Context(), []string{cp.DeploymentID})
		if err != operations.ErrStatusUnavailable || !result.ObservedAt.IsZero() {
			t.Fatal("malformed checkpoint published", err)
		}
	}
	raw, _ := json.Marshal(cp)
	deviceExec(t, s, `UPDATE dashboard_event_feeds SET checkpoint=$1`, raw)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.OperationalStatus(ctx, []string{cp.DeploymentID}); err != operations.ErrStatusUnavailable {
		t.Fatal("cancellation leaked/ignored", err)
	}
	if _, err := s.OperationalStatus(t.Context(), []string{cp.DeploymentID, cp.DeploymentID}); err != operations.ErrStatusInvalid {
		t.Fatal(err)
	}
	// An operator scrape must not wait on DDL/table locks indefinitely. The
	// transaction-local lock timeout aborts and returns no partial snapshot.
	tx, err := s.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), `LOCK TABLE dashboard_notification_provider_health IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, err := s.OperationalStatus(t.Context(), []string{cp.DeploymentID})
	if !errors.Is(err, operations.ErrStatusUnavailable) || !result.ObservedAt.IsZero() || time.Since(started) > 3*time.Second {
		t.Fatal("lock timeout failed", err, time.Since(started))
	}
}
