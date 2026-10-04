package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type inboxMemory struct {
	records              []InboxRecord
	now                  time.Time
	finalErr             error
	updates, validations int
}

func (m *inboxMemory) ListNotificationInbox(_ context.Context, a monitoring.Actor, q InboxSelection) (InboxRecords, error) {
	out := InboxRecords{Items: []InboxRecord{}, SnapshotAt: q.SnapshotAt, FetchedAt: m.now}
	if out.SnapshotAt.IsZero() {
		out.SnapshotAt = m.now
	}
	unread := 0
	for _, r := range m.records {
		allowed := false
		for _, p := range q.Authorities {
			if p.AccountID == a.Account.ID && p.Namespace == (NamespaceRef{r.Event.DeploymentID, r.Event.NamespaceID}) && p.ControlInstanceID == r.Event.ControlInstanceID {
				allowed = true
			}
		}
		if !allowed || r.AccountID != a.Account.ID || !r.ExpiresAt.After(m.now) {
			continue
		}
		if r.ReadAt == nil {
			unread++
		}
		if q.Unread && r.ReadAt != nil || r.CreatedAt.After(out.SnapshotAt) {
			continue
		}
		if q.After != nil && (r.CreatedAt.After(q.After.CreatedAt) || r.CreatedAt.Equal(q.After.CreatedAt) && r.ID >= q.After.ID) {
			continue
		}
		out.Items = append(out.Items, r)
	}
	slices.SortFunc(out.Items, func(a, b InboxRecord) int {
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		if a.CreatedAt.Before(b.CreatedAt) {
			return 1
		}
		if a.ID > b.ID {
			return -1
		}
		return 1
	})
	if len(out.Items) > q.Limit {
		out.More = true
		out.Items = out.Items[:q.Limit]
	}
	out.UnreadCount = strconv.Itoa(unread)
	return out, nil
}
func (m *inboxMemory) NotificationInbox(_ context.Context, a monitoring.Actor, id string) (InboxRecord, error) {
	for _, r := range m.records {
		if r.ID == id && r.AccountID == a.Account.ID && r.ExpiresAt.After(m.now) {
			return r, nil
		}
	}
	return InboxRecord{}, monitoring.ErrNotFound
}
func (m *inboxMemory) SetNotificationInboxRead(ctx context.Context, a monitoring.Actor, id string, read bool, p AuthorizationProof) (InboxRecord, error) {
	r, err := m.NotificationInbox(ctx, a, id)
	if err != nil {
		return r, err
	}
	m.updates++
	for i := range m.records {
		if m.records[i].ID == id {
			m.records[i].ReadAt = nil
			if read {
				now := m.now
				m.records[i].ReadAt = &now
			}
			return m.records[i], nil
		}
	}
	return r, monitoring.ErrNotFound
}
func (m *inboxMemory) ValidateNotificationInboxOwner(_ context.Context, a monitoring.Actor, p []AuthorizationProof) error {
	m.validations++
	if m.finalErr != nil {
		return m.finalErr
	}
	for _, proof := range p {
		if proof.AccountID != a.Account.ID || proof.Validate(m.now) != nil {
			return monitoring.ErrAuthority
		}
	}
	return nil
}
func inboxServiceFixture(t *testing.T) (*InboxService, *inboxMemory, *ruleTestSource, monitoring.Actor) {
	t.Helper()
	_, memory, source := workerFixture(t)
	a := memory.claims[0].Actor
	r := InboxRecord{ID: fixtureID(51, 1), AccountID: a.Account.ID, Event: fixtureEvent(), CreatedAt: fixtureTime().Add(-time.Hour), ExpiresAt: fixtureTime().Add(time.Hour), Matches: []InboxMatch{{RuleID: fixtureID(52, 1), Revision: "1", Name: "Original rule name", Scope: ScopeNamespaceJobs, OutcomeMode: OutcomeSelected, Outcomes: []string{"failure"}}}, Delivery: InboxDelivery{Total: "0", ByState: map[string]string{}}}
	repo := &inboxMemory{records: []InboxRecord{r}, now: fixtureTime()}
	cursors := monitoring.NewMemoryCursors()
	cursors.Now = fixtureTime
	s, err := NewInboxService(repo, []RuleSource{source}, cursors)
	if err != nil {
		t.Fatal(err)
	}
	s.now = fixtureTime
	return s, repo, source, a
}
func TestInboxListCurrentAuthorityCountsAndMissingHistory(t *testing.T) {
	s, m, source, a := inboxServiceFixture(t)
	source.jobError = monitoring.ErrNotFound
	page, err := s.List(t.Context(), a, InboxQuery{Limit: 20})
	if err != nil || len(page.Items) != 1 || page.UnreadCount != "1" || page.Completeness != "complete" || page.Items[0].JobAvailability != "missing" || page.Items[0].JobName != "" || page.Items[0].MatchedRules[0].Name != "Original rule name" {
		t.Fatal(page, err)
	}
	if source.discoverReads != 2 || source.jobReads != 1 || m.validations != 1 {
		t.Fatal("missing read fences")
	}
	page, err = s.List(t.Context(), a, InboxQuery{Scopes: []NamespaceRef{}, Limit: 20})
	if err != nil || len(page.Items) != 0 || page.UnreadCount != "0" || source.discoverReads != 2 {
		t.Fatal("explicit empty scope broadened", page, err)
	}
	item, err := s.Get(t.Context(), a, m.records[0].ID)
	if err != nil || item.JobAvailability != "missing" {
		t.Fatal("historical missing job unavailable", item, err)
	}
	_, err = s.SetRead(t.Context(), a, item.ID, true)
	if err != nil || m.updates != 1 || m.records[0].ReadAt == nil {
		t.Fatal(err)
	}
	other := a
	other.Account.ID = fixtureID(9, 2)
	if _, err = s.Get(t.Context(), other, item.ID); !errors.Is(err, monitoring.ErrNotFound) {
		t.Fatal("cross-account opaque ID leaked", err)
	}
}
func TestInboxOutageIsPartialAndFinalChangesPublishNothing(t *testing.T) {
	for _, mode := range []string{"discovery-not-found", "stale-proof", "job-outage", "late-revocation", "late-version", "local-owner-revoked"} {
		t.Run(mode, func(t *testing.T) {
			s, m, source, a := inboxServiceFixture(t)
			switch mode {
			case "discovery-not-found":
				source.discoverError = monitoring.ErrNotFound
			case "stale-proof":
				source.discovery.Deployment.Namespaces[0].AuthorizationExpiresAt = fixtureTime()
			case "job-outage":
				source.jobError = monitoring.ErrSource
			case "late-revocation", "late-version":
				source.discoverHook = func() {
					if source.discoverReads == 2 {
						if mode == "late-revocation" {
							source.discovery.Deployment.Namespaces = nil
						} else {
							source.discovery.Deployment.Namespaces[0].AuthorizationVersion = "next"
						}
					}
				}
			case "local-owner-revoked":
				m.finalErr = monitoring.ErrForbidden
			}
			page, err := s.List(t.Context(), a, InboxQuery{Limit: 20})
			if mode == "late-revocation" || mode == "late-version" || mode == "local-owner-revoked" {
				if err == nil || len(page.Items) != 0 || page.UnreadCount != "" {
					t.Fatal("final fence published data", page, err)
				}
				return
			}
			if mode == "job-outage" {
				if err != nil || page.Completeness != "partial" || len(page.Items) != 1 || page.Items[0].JobAvailability != "unavailable" || page.UnreadCount != "1" {
					t.Fatal(page, err)
				}
			} else {
				if !errors.Is(err, monitoring.ErrAuthority) || len(page.Items) != 0 || page.UnreadCount != "" {
					t.Fatal("total outage fabricated an empty inbox/count", page, err)
				}
				if _, err = s.Get(t.Context(), a, m.records[0].ID); !errors.Is(err, monitoring.ErrAuthority) {
					t.Fatal("detail outage mislabeled absent", err)
				}
			}
		})
	}
}

func TestInboxVerifiedEmptyGrantIsComplete(t *testing.T) {
	s, _, source, a := inboxServiceFixture(t)
	source.discovery.Deployment.Namespaces = []api.Namespace{}
	page, err := s.List(t.Context(), a, InboxQuery{Limit: 20})
	if err != nil || page.Completeness != "complete" || len(page.Items) != 0 || page.UnreadCount != "0" {
		t.Fatal("verified empty grants were treated as an outage", page, err)
	}
}
func TestInboxCursorAccountQueryAuthorityAndReplay(t *testing.T) {
	s, m, source, a := inboxServiceFixture(t)
	second := m.records[0]
	second.ID = fixtureID(51, 2)
	second.CreatedAt = second.CreatedAt.Add(-time.Minute)
	m.records = append(m.records, second)
	page, err := s.List(t.Context(), a, InboxQuery{Limit: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	query := InboxQuery{Limit: 1, Cursor: page.NextCursor}
	older, err := s.List(t.Context(), a, query)
	if err != nil || len(older.Items) != 1 || older.Items[0].ID != second.ID || older.NextCursor != "" {
		t.Fatal(older, err)
	}
	replay, err := s.List(t.Context(), a, query)
	if err != nil || replay.Items[0].ID != second.ID {
		t.Fatal("cursor replay", replay, err)
	}
	changed := query
	changed.Unread = true
	if _, err = s.List(t.Context(), a, changed); !errors.Is(err, monitoring.ErrCursor) {
		t.Fatal("query changed", err)
	}
	other := a
	other.Account.ID = fixtureID(9, 2)
	if _, err = s.List(t.Context(), other, query); !errors.Is(err, monitoring.ErrCursor) {
		t.Fatal("account changed", err)
	}
	source.discovery.Deployment.Namespaces[0].AuthorizationVersion = "4"
	if _, err = s.List(t.Context(), a, query); !errors.Is(err, monitoring.ErrCursor) {
		t.Fatal("authority changed", err)
	}
	source.discovery.Deployment.Namespaces[0].AuthorizationVersion = "3"
	source.discovery.InstanceID = fixtureID(3, 2)
	if _, err = s.List(t.Context(), a, query); !errors.Is(err, monitoring.ErrCursor) {
		t.Fatal("source instance changed", err)
	}
}
func TestInboxPartialSourcesNeverRevealUnavailableReferences(t *testing.T) {
	s, m, source, a := inboxServiceFixture(t)
	_, _, second, _ := inboxServiceFixture(t)
	second.checkpoint.DeploymentID = fixtureID(2, 2)
	second.discovery.Deployment.ID = second.ID()
	second.discovery.InstanceID = fixtureID(3, 2)
	second.discoverError = monitoring.ErrAuthority
	s.sources[second.ID()] = second
	s.ids = append(s.ids, second.ID())
	hidden := m.records[0]
	hidden.ID = fixtureID(51, 2)
	hidden.Event.DeploymentID = second.ID()
	hidden.Event.ControlInstanceID = second.discovery.InstanceID
	m.records = append(m.records, hidden)
	page, err := s.List(t.Context(), a, InboxQuery{Limit: 20})
	if err != nil || len(page.Items) != 1 || page.UnreadCount != "1" || page.UnavailableSources != 1 || page.Completeness != "partial" {
		t.Fatal(page, err)
	}
	data, _ := json.Marshal(page)
	var decoded map[string]any
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if page.Items[0].Job.DeploymentID != source.ID() {
		t.Fatal("wrong visible source")
	}
	for _, item := range page.Items {
		if item.ID == hidden.ID || item.ControlInstanceID == hidden.Event.ControlInstanceID {
			t.Fatal("hidden reference leaked")
		}
	}
}
func TestInboxReadDeniedBeforeMutation(t *testing.T) {
	s, m, source, a := inboxServiceFixture(t)
	source.discoverHook = func() {
		if source.discoverReads == 2 {
			source.discovery.Deployment.Namespaces = []api.Namespace{}
		}
	}
	if _, err := s.SetRead(t.Context(), a, m.records[0].ID, true); !errors.Is(err, monitoring.ErrNotFound) || m.updates != 0 {
		t.Fatal("read state changed after access loss", err, m.updates)
	}
}
