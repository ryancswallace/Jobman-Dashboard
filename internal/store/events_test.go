package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/events"
)

func eventCheckpoint() events.Checkpoint {
	return events.Checkpoint{DeploymentID: "20000000-0000-4000-8000-000000000001", ControlInstanceID: "30000000-0000-4000-8000-000000000001", RecoveryEpoch: "1", NamespaceIDs: []string{"40000000-0000-4000-8000-000000000001"}, AsOf: time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC), HeadCursor: "opaque-initial-head", OldestCursor: "opaque-oldest", RetentionSeconds: "2592000", BacklogCount: "0"}
}
func sourceEvent(c events.Checkpoint, n int) events.Event {
	return events.Event{DeploymentID: c.DeploymentID, ControlInstanceID: c.ControlInstanceID, EventID: fmt.Sprintf("50000000-0000-4000-8000-%012d", n), Position: fmt.Sprint(n), NamespaceID: c.NamespaceIDs[0], JobID: "60000000-0000-4000-8000-000000000001", OwnerPrincipalID: "70000000-0000-4000-8000-000000000001", OldPhase: "running", NewPhase: "terminal", Outcome: "future_outcome", JobRevision: "9007199254740993", RecordedAt: c.AsOf.Add(time.Second)}
}
func initializedFeed(t *testing.T, s *Store, c events.Checkpoint) events.Feed {
	t.Helper()
	feed, err := s.ClaimFeed(t.Context(), c.DeploymentID, c.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.InitializeFeed(t.Context(), feed, c); err != nil {
		t.Fatal(err)
	}
	feed, err = s.ClaimFeed(t.Context(), c.DeploymentID, c.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	return feed
}
func eventPage(c events.Checkpoint, cursor string, items ...events.Event) events.Page {
	c.HeadCursor = cursor
	if items == nil {
		items = []events.Event{}
	}
	return events.Page{Checkpoint: c, Items: items, NextCursor: cursor}
}

func TestEventFeedClaimAndLeaseFencing(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	var wg sync.WaitGroup
	claims := make(chan events.Feed, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			feed, err := s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
			if err == nil {
				claims <- feed
			} else if !errors.Is(err, events.ErrLease) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(claims)
	if len(claims) != 1 {
		t.Fatalf("concurrent claims=%d", len(claims))
	}
	old := <-claims
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_event_feeds SET lease_expires_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
	if err != nil || fresh.LeaseToken == old.LeaseToken {
		t.Fatal("lease not reclaimed", err)
	}
	if err = s.InitializeFeed(ctx, old, c); !errors.Is(err, events.ErrLease) {
		t.Fatal("expired worker initialized source", err)
	}
	if err = s.InitializeFeed(ctx, fresh, c); err != nil {
		t.Fatal(err)
	}
	if err = s.ReleaseFeed(ctx, old, "source_unavailable"); err != nil {
		t.Fatal(err)
	}
	var status, code string
	if err = s.Pool.QueryRow(ctx, `SELECT status,last_error FROM dashboard_event_feeds`).Scan(&status, &code); err != nil || status != "active" || code != "" {
		t.Fatal("stale release changed fresh state", err)
	}
}

func TestEventFeedAtomicCommitReplayAndImmutableFacts(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	feed := initializedFeed(t, s, c)
	first := sourceEvent(c, 1)
	// A database failure after inserts but before checkpoint publication must
	// roll back the entire batch, like a crash before commit.
	_, err := s.Pool.Exec(ctx, `CREATE FUNCTION reject_feed_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic commit interruption'; END $$; CREATE TRIGGER reject_feed_commit BEFORE UPDATE OF cursor ON dashboard_event_feeds FOR EACH ROW EXECUTE FUNCTION reject_feed_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AppendFeed(ctx, feed, eventPage(c, "head-1", first)); err == nil {
		t.Fatal("injected commit failure absent")
	}
	var count int
	var cursor string
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM dashboard_source_events),cursor FROM dashboard_event_feeds`).Scan(&count, &cursor); err != nil || count != 0 || cursor != c.HeadCursor {
		t.Fatal("partial event/checkpoint committed", err)
	}
	if _, err = s.Pool.Exec(ctx, `DROP TRIGGER reject_feed_commit ON dashboard_event_feeds`); err != nil {
		t.Fatal(err)
	}
	if err = s.AppendFeed(ctx, feed, eventPage(c, "head-1", first)); err != nil {
		t.Fatal(err)
	}
	if err = s.AppendFeed(ctx, feed, eventPage(c, "head-1", first)); !errors.Is(err, events.ErrLease) {
		t.Fatal("old committed claim reused", err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_source_events SET processed_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	feed, err = s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	first.Position = "2"
	if err = s.AppendFeed(ctx, feed, eventPage(c, "head-2", first)); err != nil {
		t.Fatal("stable replay at new position rejected", err)
	}
	var processed bool
	var retained int64
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM dashboard_source_events),(SELECT bool_and(processed_at IS NOT NULL) FROM dashboard_source_events),retained_count FROM dashboard_event_feeds`).Scan(&count, &processed, &retained); err != nil || count != 1 || retained != 1 || !processed {
		t.Fatal("replay duplicated or reset work", err)
	}
	feed, err = s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	newItem := sourceEvent(c, 3)
	changed := first
	changed.Position = "4"
	changed.Outcome = "failure"
	if err = s.AppendFeed(ctx, feed, eventPage(c, "head-4", newItem, changed)); !errors.Is(err, events.ErrConflict) {
		t.Fatal("reused event identity changed facts", err)
	}
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM dashboard_source_events),cursor FROM dashboard_event_feeds`).Scan(&count, &cursor); err != nil || count != 1 || cursor != "head-2" {
		t.Fatal("conflicting batch partially committed", err)
	}
	if err = s.AppendFeed(ctx, feed, eventPage(c, "head-3", newItem)); err != nil {
		t.Fatal("original cursor could not replay after rollback", err)
	}
}

func TestEventFeedSourceIsolationAndExplicitGaps(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	feed := initializedFeed(t, s, c)
	if err := s.AppendFeed(ctx, feed, eventPage(c, "head-1", sourceEvent(c, 1))); err != nil {
		t.Fatal(err)
	}
	other := c
	other.DeploymentID = "20000000-0000-4000-8000-000000000002"
	otherFeed := initializedFeed(t, s, other)
	if err := s.AppendFeed(ctx, otherFeed, eventPage(other, "head-1", sourceEvent(other, 1))); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_source_events`).Scan(&count); err != nil || count != 2 {
		t.Fatal("same event UUID collapsed across deployments", err)
	}
	feed, err := s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	changed := c
	changed.RecoveryEpoch = "2"
	if err = s.AppendFeed(ctx, feed, eventPage(changed, "new-epoch", sourceEvent(changed, 2))); !errors.Is(err, events.ErrRecoveryRequired) {
		t.Fatal("recovery epoch silently changed", err)
	}
	if err = s.PauseFeed(ctx, feed, string(events.SourceChanged)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs); !errors.Is(err, events.ErrPaused) {
		t.Fatal("paused source automatically resumed", err)
	}
	var reason, prior string
	if err = s.Pool.QueryRow(ctx, `SELECT reason,prior_cursor FROM dashboard_event_gaps WHERE deployment_id=$1::uuid`, c.DeploymentID).Scan(&reason, &prior); err != nil || reason != string(events.SourceChanged) || prior != "head-1" {
		t.Fatal("source gap missing prior checkpoint", err)
	}
	otherFeed, err = s.ClaimFeed(ctx, other.DeploymentID, other.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	newScope := append([]string(nil), other.NamespaceIDs...)
	newScope = append(newScope, "40000000-0000-4000-8000-000000000002")
	if _, err = s.ClaimFeed(ctx, other.DeploymentID, newScope); !errors.Is(err, events.ErrPaused) {
		t.Fatal("namespace-set change did not pause", err)
	}
	if err = s.AppendFeed(ctx, otherFeed, eventPage(other, "head-2", sourceEvent(other, 2))); !errors.Is(err, events.ErrLease) {
		t.Fatal("configuration change did not fence old worker", err)
	}
}

func TestEventFeedBoundsAndRetention(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	feed := initializedFeed(t, s, c)
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_event_feeds SET retained_count=1000000`); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendFeed(ctx, feed, eventPage(c, "head-1", sourceEvent(c, 1))); !errors.Is(err, events.ErrCapacity) {
		t.Fatal("retained capacity exceeded", err)
	}
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_source_events`).Scan(&count); err != nil || count != 0 {
		t.Fatal("capacity failure stored a partial batch", err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_event_feeds SET retained_count=0`); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendFeed(ctx, feed, eventPage(c, "head-1", sourceEvent(c, 1))); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneSourceEvents(ctx, c.DeploymentID); err != nil || n != 0 {
		t.Fatal("unexpired tombstone removed", err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_source_events SET expires_at=clock_timestamp()-interval '1 second',last_seen_at=clock_timestamp()-interval '36 days'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneSourceEvents(ctx, c.DeploymentID); err != nil || n != 0 {
		t.Fatal("pending evaluation silently deleted", err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_source_events SET processed_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneSourceEvents(ctx, c.DeploymentID); err != nil || n != 1 {
		t.Fatal("expired tombstone not removed", err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT retained_count FROM dashboard_event_feeds`).Scan(&count); err != nil || count != 0 {
		t.Fatal("retention counter not decremented", err)
	}
	feed, err := s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	backwards := sourceEvent(c, 1)
	if err = s.AppendFeed(ctx, feed, eventPage(c, "head-backwards", backwards)); !errors.Is(err, events.ErrInvalid) {
		t.Fatal("within-epoch positions moved backward", err)
	}
	if err = s.AppendFeed(ctx, feed, eventPage(c, "head-empty")); err != nil {
		t.Fatal("empty authorized page could not advance", err)
	}
}

func TestEventRetentionIncreaseAndSourceOutageProtectTombstones(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	feed := initializedFeed(t, s, c)
	if err := s.AppendFeed(ctx, feed, eventPage(c, "head-1", sourceEvent(c, 1))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE dashboard_source_events SET expires_at=clock_timestamp()-interval '1 second',last_seen_at=clock_timestamp()-interval '36 days',processed_at=clock_timestamp(); UPDATE dashboard_event_feeds SET last_success_at=clock_timestamp()-interval '2 minutes'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneSourceEvents(ctx, c.DeploymentID); err != nil || n != 0 {
		t.Fatal("outage pruned an unrefreshed replay window", err)
	}
	feed, err := s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	c.RetentionSeconds = "7776000"
	if err = s.AppendFeed(ctx, feed, eventPage(c, "head-90days")); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneSourceEvents(ctx, c.DeploymentID); err != nil || n != 0 {
		t.Fatal("retention increase deleted older tombstone", err)
	}
	feed, err = s.ClaimFeed(ctx, c.DeploymentID, c.NamespaceIDs)
	if err != nil {
		t.Fatal(err)
	}
	c.RetentionSeconds = "2592000"
	if err = s.AppendFeed(ctx, feed, eventPage(c, "head-30days")); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneSourceEvents(ctx, c.DeploymentID); err != nil || n != 0 {
		t.Fatal("retention decrease discarded high-water protection", err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE dashboard_source_events SET last_seen_at=clock_timestamp()-interval '96 days'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneSourceEvents(ctx, c.DeploymentID); err != nil || n != 1 {
		t.Fatal("expired processed tombstone retained", err)
	}
}

type ingestionSource struct {
	checkpoint  events.Checkpoint
	page        events.Page
	err         error
	checkpoints int
}

func (f *ingestionSource) SourceID() string       { return f.checkpoint.DeploymentID }
func (f *ingestionSource) NamespaceIDs() []string { return f.checkpoint.NamespaceIDs }
func (f *ingestionSource) Checkpoint(context.Context) (events.Checkpoint, error) {
	f.checkpoints++
	return f.checkpoint, nil
}
func (f *ingestionSource) Read(context.Context, string, int) (events.Page, error) {
	return f.page, f.err
}

func TestIngestionWorkerDoesNotResetRecoveryOrTransientFailure(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	c := eventCheckpoint()
	source := &ingestionSource{checkpoint: c}
	worker, err := events.NewIngestor([]events.Source{source}, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = worker.Step(ctx, source); err != nil {
		t.Fatal(err)
	}
	source.err = events.ErrAuthority
	if _, err = worker.Step(ctx, source); !errors.Is(err, events.ErrAuthority) {
		t.Fatal(err)
	}
	source.err = nil
	source.page = eventPage(c, "head-1", sourceEvent(c, 1))
	if _, err = worker.Step(ctx, source); err != nil {
		t.Fatal(err)
	}
	source.err = &events.RecoveryError{Reason: events.CursorExpired}
	if _, err = worker.Step(ctx, source); !errors.Is(err, events.ErrRecoveryRequired) {
		t.Fatal(err)
	}
	if _, err = worker.Step(ctx, source); !errors.Is(err, events.ErrPaused) {
		t.Fatal(err)
	}
	if source.checkpoints != 1 {
		t.Fatal("failure silently obtained a new head")
	}
	var cursor, reason string
	if err = s.Pool.QueryRow(ctx, `SELECT cursor,last_error FROM dashboard_event_feeds`).Scan(&cursor, &reason); err != nil || cursor != "head-1" || reason != string(events.CursorExpired) {
		t.Fatal("recovery checkpoint lost", err)
	}
}
