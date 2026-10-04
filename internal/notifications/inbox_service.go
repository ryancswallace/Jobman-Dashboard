package notifications

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type InboxService struct {
	repository InboxRepository
	sources    map[string]RuleSource
	ids        []string
	cursors    monitoring.CursorStore
	slots      chan struct{}
	now        func() time.Time
}

func NewInboxService(repository InboxRepository, sources []RuleSource, cursors monitoring.CursorStore) (*InboxService, error) {
	if repository == nil || cursors == nil || len(sources) < 1 || len(sources) > MaximumSources {
		return nil, ErrInvalid
	}
	s := &InboxService{repository: repository, sources: map[string]RuleSource{}, cursors: cursors, slots: make(chan struct{}, 4), now: time.Now}
	for _, source := range sources {
		if source == nil || !uuid(source.ID()) || s.sources[source.ID()] != nil {
			return nil, ErrInvalid
		}
		s.sources[source.ID()] = source
		s.ids = append(s.ids, source.ID())
	}
	slices.Sort(s.ids)
	return s, nil
}
func (s *InboxService) network(ctx context.Context, fn func() error) error {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
		return fn()
	case <-ctx.Done():
		return ctx.Err()
	}
}

type inboxAccess struct {
	Proofs       []AuthorizationProof
	Unavailable  []string
	Inaccessible int
}

func (s *InboxService) access(ctx context.Context, a monitoring.Actor, selection []NamespaceRef) inboxAccess {
	ids := slices.Clone(s.ids)
	if selection != nil {
		ids = []string{}
		for _, r := range selection {
			if !slices.Contains(ids, r.DeploymentID) {
				ids = append(ids, r.DeploymentID)
			}
		}
		slices.Sort(ids)
	}
	results := make([]inboxAccess, len(ids))
	parallel(len(ids), func(i int) {
		id := ids[i]
		source := s.sources[id]
		if source == nil {
			for _, r := range selection {
				if r.DeploymentID == id {
					results[i].Inaccessible++
				}
			}
			return
		}
		var d monitoring.Discovery
		err := s.network(ctx, func() error { var err error; d, err = source.Discover(ctx, a); return err })
		if err != nil || d.Deployment.ID != id || d.Deployment.Status != "available" || !uuid(d.InstanceID) || !positiveDecimal(d.RecoveryEpoch) || len(d.Deployment.Namespaces) > MaximumNamespaces {
			results[i].Unavailable = []string{id}
			return
		}
		found := map[string]bool{}
		proofs := []AuthorizationProof{}
		for _, ns := range d.Deployment.Namespaces {
			if !uuid(ns.ID) || found[ns.ID] {
				results[i].Unavailable = []string{id}
				return
			}
			found[ns.ID] = true
			ref := NamespaceRef{id, ns.ID}
			if selection != nil && !slices.Contains(selection, ref) {
				continue
			}
			p := AuthorizationProof{AccountID: a.Account.ID, Namespace: ref, ControlInstanceID: d.InstanceID, RecoveryEpoch: d.RecoveryEpoch, PrincipalID: d.PrincipalID, Version: ns.AuthorizationVersion, CheckedAt: ns.AuthorizationCheckedAt, ExpiresAt: ns.AuthorizationExpiresAt}
			if p.Validate(s.now()) != nil {
				results[i].Unavailable = []string{id}
				return
			}
			if !slices.Contains(ns.Capabilities, "namespace.read") || !slices.Contains(ns.Capabilities, "jobs.read") {
				if selection != nil {
					results[i].Inaccessible++
				}
				continue
			}
			proofs = append(proofs, p)
		}
		for _, r := range selection {
			if r.DeploymentID == id && !found[r.NamespaceID] {
				results[i].Inaccessible++
			}
		}
		results[i].Proofs = proofs
	})
	result := inboxAccess{Proofs: []AuthorizationProof{}, Unavailable: []string{}}
	for _, r := range results {
		result.Proofs = append(result.Proofs, r.Proofs...)
		result.Unavailable = append(result.Unavailable, r.Unavailable...)
		result.Inaccessible += r.Inaccessible
	}
	slices.SortFunc(result.Proofs, func(a, b AuthorizationProof) int {
		if a.Namespace.DeploymentID != b.Namespace.DeploymentID {
			return strings.Compare(a.Namespace.DeploymentID, b.Namespace.DeploymentID)
		}
		return strings.Compare(a.Namespace.NamespaceID, b.Namespace.NamespaceID)
	})
	return result
}
func inboxHash(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (a inboxAccess) fingerprint() string {
	type grant struct {
		Namespace                           NamespaceRef
		Instance, Epoch, Principal, Version string
	}
	grants := make([]grant, 0, len(a.Proofs))
	for _, p := range a.Proofs {
		grants = append(grants, grant{p.Namespace, p.ControlInstanceID, p.RecoveryEpoch, p.PrincipalID, p.Version})
	}
	return inboxHash(struct {
		Grants       []grant
		Unavailable  []string
		Inaccessible int
	}{grants, a.Unavailable, a.Inaccessible})
}
func (a inboxAccess) proof(e InboxRecord) (AuthorizationProof, bool) {
	for _, p := range a.Proofs {
		if p.Namespace == (NamespaceRef{e.Event.DeploymentID, e.Event.NamespaceID}) && p.ControlInstanceID == e.Event.ControlInstanceID {
			return p, true
		}
	}
	return AuthorizationProof{}, false
}
func (s *InboxService) project(ctx context.Context, a monitoring.Actor, r InboxRecord, access inboxAccess) (InboxItem, error) {
	var out InboxItem
	if !uuid(r.ID) || r.AccountID != a.Account.ID || r.Event.Validate() != nil || !r.ExpiresAt.After(s.now()) || !r.ExpiresAt.After(r.CreatedAt) || len(r.Matches) < 1 || len(r.Matches) > MaximumRulesPerAccount {
		return out, monitoring.ErrSource
	}
	if _, ok := access.proof(r); !ok {
		return out, monitoring.ErrNotFound
	}
	for _, m := range r.Matches {
		if m.Validate() != nil {
			return out, monitoring.ErrSource
		}
	}
	if _, err := strconv.ParseUint(r.Delivery.Total, 10, 64); err != nil {
		return out, monitoring.ErrSource
	}
	out = InboxItem{ID: r.ID, Job: JobRef{r.Event.DeploymentID, r.Event.NamespaceID, r.Event.JobID}, ControlInstanceID: r.Event.ControlInstanceID, EventID: r.Event.EventID, Outcome: r.Event.Outcome, EventAt: r.Event.RecordedAt, ObservedCompletedAt: r.Event.ObservedCompletedAt, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, Read: r.ReadAt != nil, ReadAt: r.ReadAt, MatchedRules: r.Matches, JobAvailability: "unavailable", Delivery: r.Delivery}
	var job api.Job
	err := s.network(ctx, func() error {
		var err error
		job, err = s.sources[r.Event.DeploymentID].Job(ctx, a, api.Scope{DeploymentID: r.Event.DeploymentID, NamespaceID: r.Event.NamespaceID}, r.Event.JobID)
		return err
	})
	if err != nil {
		if denied(err) {
			out.JobAvailability = "missing"
		}
		return out, nil
	}
	if job.ID != r.Event.JobID || job.DeploymentID != r.Event.DeploymentID || job.NamespaceID != r.Event.NamespaceID {
		return InboxItem{}, monitoring.ErrSource
	}
	out.JobAvailability = "available"
	out.JobName = job.Name
	return out, nil
}

type inboxCursor struct {
	monitoring.CursorIdentity
	Query         InboxQuery     `json:"query"`
	AuthorityHash string         `json:"authorityHash"`
	SnapshotAt    time.Time      `json:"snapshotAt"`
	After         *InboxPosition `json:"after,omitempty"`
	ExpiresAt     time.Time      `json:"expiresAt"`
	Next          string         `json:"next,omitempty"`
	NextAfter     *InboxPosition `json:"nextAfter,omitempty"`
}

func (s *InboxService) List(parent context.Context, a monitoring.Actor, input InboxQuery) (InboxPage, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	empty := InboxPage{}
	q, err := input.Canonical()
	if err != nil {
		return empty, err
	}
	if !uuid(a.Account.ID) {
		return empty, monitoring.ErrForbidden
	}
	access := s.access(ctx, a, q.Scopes)
	if len(access.Proofs) == 0 && len(access.Unavailable) > 0 {
		return empty, monitoring.ErrAuthority
	}
	hash := access.fingerprint()
	queryHash := "inbox:" + inboxHash(q)
	state := inboxCursor{CursorIdentity: monitoring.CursorIdentity{AccountID: a.Account.ID, QueryHash: queryHash}, Query: q, AuthorityHash: hash, ExpiresAt: s.now().Add(InboxCursorLifetime)}
	var version uint64
	if q.Cursor != "" {
		data, v, loadErr := s.cursors.Load(ctx, q.Cursor)
		version = v
		if loadErr != nil || len(data) > 256<<10 || json.Unmarshal(data, &state) != nil || state.AccountID != a.Account.ID || state.QueryHash != queryHash || state.AuthorityHash != hash || !state.ExpiresAt.After(s.now()) || state.SnapshotAt.IsZero() || state.After == nil {
			return empty, monitoring.ErrCursor
		}
	}
	records, err := s.repository.ListNotificationInbox(ctx, a, InboxSelection{Authorities: access.Proofs, SnapshotAt: state.SnapshotAt, After: state.After, Unread: q.Unread, Limit: q.Limit})
	if err != nil {
		return empty, err
	}
	if len(records.Items) > q.Limit || records.More && len(records.Items) != q.Limit || records.SnapshotAt.IsZero() || records.FetchedAt.IsZero() {
		return empty, monitoring.ErrSource
	}
	out := InboxPage{Items: make([]InboxItem, len(records.Items)), UnreadCount: records.UnreadCount, Completeness: "complete", UnavailableSources: len(access.Unavailable), InaccessibleScopes: access.Inaccessible, FetchedAt: records.FetchedAt}
	problems := make([]error, len(records.Items))
	parallel(len(records.Items), func(i int) { out.Items[i], problems[i] = s.project(ctx, a, records.Items[i], access) })
	for _, err := range problems {
		if err != nil {
			return empty, err
		}
	}
	final := s.access(ctx, a, q.Scopes)
	if final.fingerprint() != hash {
		if q.Cursor != "" {
			return empty, monitoring.ErrCursor
		}
		return empty, monitoring.ErrAuthority
	}
	for _, item := range out.Items {
		if !item.ExpiresAt.After(s.now()) {
			return empty, monitoring.ErrAuthority
		}
		if item.JobAvailability == "unavailable" {
			out.Completeness = "partial"
		}
	}
	if out.UnavailableSources > 0 || out.InaccessibleScopes > 0 {
		out.Completeness = "partial"
	}
	if records.More {
		last := records.Items[len(records.Items)-1]
		position := &InboxPosition{last.CreatedAt, last.ID}
		if state.Next != "" && state.NextAfter != nil && *state.NextAfter == *position {
			out.NextCursor = state.Next
		} else {
			next := inboxCursor{CursorIdentity: monitoring.CursorIdentity{AccountID: a.Account.ID, QueryHash: queryHash, Initial: q.Cursor == ""}, Query: q, AuthorityHash: hash, SnapshotAt: records.SnapshotAt, After: position, ExpiresAt: state.ExpiresAt}
			data, _ := json.Marshal(next)
			out.NextCursor, err = s.cursors.Create(ctx, data, next.ExpiresAt)
			if err != nil {
				return empty, err
			}
			state.Next = out.NextCursor
			state.NextAfter = position
		}
	}
	if q.Cursor != "" {
		state.Initial = false
		data, _ := json.Marshal(state)
		if err = s.cursors.Advance(ctx, q.Cursor, version, data); err != nil {
			return empty, err
		}
	}
	if err = s.repository.ValidateNotificationInboxOwner(ctx, a, final.Proofs); err != nil {
		return empty, err
	}
	for _, item := range out.Items {
		if !item.ExpiresAt.After(s.now()) {
			return empty, monitoring.ErrAuthority
		}
	}
	if out.Validate() != nil {
		return empty, monitoring.ErrSource
	}
	return out, nil
}
func (s *InboxService) Get(ctx context.Context, a monitoring.Actor, id string) (InboxItem, error) {
	return s.detail(ctx, a, id, nil)
}
func (s *InboxService) SetRead(ctx context.Context, a monitoring.Actor, id string, read bool) (InboxItem, error) {
	return s.detail(ctx, a, id, &read)
}
func (s *InboxService) detail(parent context.Context, a monitoring.Actor, id string, read *bool) (InboxItem, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	var empty InboxItem
	if !uuid(id) {
		return empty, ErrInvalid
	}
	r, err := s.repository.NotificationInbox(ctx, a, id)
	if err != nil {
		return empty, err
	}
	refs := []NamespaceRef{{r.Event.DeploymentID, r.Event.NamespaceID}}
	access := s.access(ctx, a, refs)
	if len(access.Unavailable) > 0 {
		return empty, monitoring.ErrAuthority
	}
	if _, ok := access.proof(r); !ok {
		return empty, monitoring.ErrNotFound
	}
	out, err := s.project(ctx, a, r, access)
	if err != nil {
		return empty, err
	}
	final := s.access(ctx, a, refs)
	if len(final.Unavailable) > 0 {
		return empty, monitoring.ErrAuthority
	}
	proof, ok := final.proof(r)
	if !ok {
		return empty, monitoring.ErrNotFound
	}
	if access.fingerprint() != final.fingerprint() {
		return empty, monitoring.ErrAuthority
	}
	if read != nil {
		updated, err := s.repository.SetNotificationInboxRead(ctx, a, id, *read, proof)
		if err != nil {
			return empty, err
		}
		out.Read = updated.ReadAt != nil
		out.ReadAt = updated.ReadAt
		after := s.access(ctx, a, refs)
		if len(after.Unavailable) > 0 {
			return empty, monitoring.ErrAuthority
		}
		if _, ok := after.proof(r); !ok {
			return empty, monitoring.ErrNotFound
		}
		if after.fingerprint() != final.fingerprint() {
			return empty, monitoring.ErrAuthority
		}
		final = after
	}
	if err = s.repository.ValidateNotificationInboxOwner(ctx, a, final.Proofs); err != nil {
		return empty, err
	}
	if !out.ExpiresAt.After(s.now()) {
		return empty, monitoring.ErrNotFound
	}
	if out.Validate() != nil {
		return empty, monitoring.ErrSource
	}
	return out, nil
}
