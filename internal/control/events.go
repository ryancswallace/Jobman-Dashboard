package control

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
)

// EventSource owns a separate transport and two bounded background slots. It
// cannot borrow an interactive actor or consume the interactive client's pool.
type EventSource struct {
	client *Client
	slots  chan struct{}
}

var _ events.Source = (*EventSource)(nil)

func NewEventSource(config Config) (*EventSource, error) {
	config.NamespaceIDs = slices.Clone(config.NamespaceIDs)
	slices.Sort(config.NamespaceIDs)
	for i, id := range config.NamespaceIDs {
		if i > 0 && config.NamespaceIDs[i-1] == id {
			return nil, events.ErrInvalid
		}
	}
	client, err := New(config)
	if err != nil {
		return nil, err
	}
	return &EventSource{client: client, slots: make(chan struct{}, 2)}, nil
}

func (s *EventSource) SourceID() string       { return s.client.config.DeploymentID }
func (s *EventSource) NamespaceIDs() []string { return slices.Clone(s.client.config.NamespaceIDs) }
func (s *EventSource) Close()                 { s.client.Close() }

func (s *EventSource) acquire(parent context.Context) (context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	select {
	case s.slots <- struct{}{}:
		return ctx, func() { <-s.slots; cancel() }, nil
	case <-ctx.Done():
		cancel()
		return nil, nil, ctx.Err()
	}
}

func (s *EventSource) Checkpoint(parent context.Context) (events.Checkpoint, error) {
	ctx, done, err := s.acquire(parent)
	if err != nil {
		return events.Checkpoint{}, err
	}
	defer done()
	epoch, err := s.capabilities(ctx)
	if err != nil {
		return events.Checkpoint{}, err
	}
	var wire eventResponse
	if err = s.get(ctx, "/v1/monitoring-events/checkpoint", nil, &wire, true); err != nil {
		return events.Checkpoint{}, err
	}
	if wire.Kind != "MonitoringCheckpoint" || wire.Items != nil || wire.NextCursor != "" || wire.HasMore != nil {
		return events.Checkpoint{}, events.ErrInvalid
	}
	return s.checkpoint(wire, epoch)
}

func (s *EventSource) Read(parent context.Context, cursor string, limit int) (events.Page, error) {
	if !events.ValidCursor(cursor) {
		return events.Page{}, &events.RecoveryError{Reason: events.CursorInvalid}
	}
	if limit < 1 || limit > events.MaximumPageSize {
		return events.Page{}, events.ErrInvalid
	}
	ctx, done, err := s.acquire(parent)
	if err != nil {
		return events.Page{}, err
	}
	defer done()
	epoch, err := s.capabilities(ctx)
	if err != nil {
		return events.Page{}, err
	}
	var wire eventResponse
	if err = s.get(ctx, "/v1/monitoring-events", url.Values{"cursor": {cursor}, "limit": {strconv.Itoa(limit)}}, &wire, true); err != nil {
		return events.Page{}, err
	}
	checkpoint, err := s.checkpoint(wire, epoch)
	if err != nil {
		return events.Page{}, err
	}
	if wire.Kind != "MonitoringEventList" || wire.Items == nil || wire.HasMore == nil || len(wire.Items) > limit || *wire.HasMore && len(wire.Items) != limit || len(wire.Items) > 0 && wire.NextCursor == cursor {
		return events.Page{}, events.ErrInvalid
	}
	page := events.Page{Checkpoint: checkpoint, Items: make([]events.Event, 0, len(wire.Items)), NextCursor: wire.NextCursor, HasMore: *wire.HasMore}
	for _, item := range wire.Items {
		if item.Imported == nil || item.Reconciliation == nil {
			return events.Page{}, events.ErrInvalid
		}
		page.Items = append(page.Items, events.Event{DeploymentID: s.SourceID(), ControlInstanceID: checkpoint.ControlInstanceID, EventID: item.EventID, Position: item.Position, NamespaceID: item.NamespaceID, JobID: item.JobID, RunID: item.RunID, RunNumber: item.RunNumber, OwnerPrincipalID: item.OwnerPrincipalID, OldPhase: item.OldPhase, NewPhase: item.NewPhase, Outcome: item.Outcome, JobRevision: item.JobRevision, ObservedCompletedAt: item.ObservedCompletedAt, RecordedAt: item.RecordedAt, Imported: *item.Imported, Reconciliation: *item.Reconciliation})
	}
	if err = page.Validate(); err != nil {
		return events.Page{}, err
	}
	return page, nil
}

func (s *EventSource) checkpoint(wire eventResponse, epoch string) (events.Checkpoint, error) {
	if wire.APIVersion != contract || !uuid(wire.ControlInstanceID) || !decimal(wire.RecoveryEpoch) || wire.RecoveryEpoch == "0" {
		return events.Checkpoint{}, events.ErrInvalid
	}
	if wire.ControlInstanceID != s.client.config.InstanceID || wire.RecoveryEpoch != epoch {
		return events.Checkpoint{}, &events.RecoveryError{Reason: events.SourceChanged}
	}
	result := events.Checkpoint{DeploymentID: s.SourceID(), ControlInstanceID: wire.ControlInstanceID, RecoveryEpoch: wire.RecoveryEpoch, NamespaceIDs: s.NamespaceIDs(), AsOf: wire.AsOf, HeadCursor: wire.HeadCursor, OldestCursor: wire.OldestCursor, RetentionSeconds: wire.RetentionSeconds, BacklogCount: wire.BacklogCount, OldestUnpublishedRecordedAt: wire.OldestUnpublishedRecordedAt}
	if err := result.Validate(); err != nil {
		return events.Checkpoint{}, err
	}
	if result.AsOf.After(s.client.now().Add(5 * time.Second)) {
		return events.Checkpoint{}, events.ErrInvalid
	}
	return result, nil
}

func (s *EventSource) capabilities(ctx context.Context) (string, error) {
	var caps struct {
		APIVersion   string `json:"apiVersion"`
		Kind         string `json:"kind"`
		Capabilities struct {
			InstanceID       string    `json:"instanceId"`
			RecoveryEpoch    string    `json:"recoveryEpoch"`
			ServiceTime      time.Time `json:"serviceTime"`
			ContractVersions []string  `json:"contractVersions"`
			Features         []string  `json:"features"`
			MaximumPageSize  int       `json:"maximumPageSize"`
		} `json:"capabilities"`
	}
	if err := s.get(ctx, "/v1/capabilities", nil, &caps, false); err != nil {
		return "", err
	}
	c := caps.Capabilities
	if caps.APIVersion != contract || !slices.Contains(c.ContractVersions, contract) || !slices.Contains(c.Features, "durable-monitoring-events") {
		s.client.config.Observer.Mismatch("source_contract")
	}
	if caps.APIVersion != contract || caps.Kind != "ControlCapabilities" || !uuid(c.InstanceID) || !decimal(c.RecoveryEpoch) || c.RecoveryEpoch == "0" || c.ServiceTime.IsZero() || c.ServiceTime.After(s.client.now().Add(5*time.Second)) || len(c.ContractVersions) > 32 || len(c.Features) > 128 || !slices.Contains(c.ContractVersions, contract) || !slices.Contains(c.Features, "durable-monitoring-events") || c.MaximumPageSize < 200 {
		return "", events.ErrUnavailable
	}
	if c.InstanceID != s.client.config.InstanceID {
		s.client.config.Observer.Mismatch("source_identity")
		return "", &events.RecoveryError{Reason: events.SourceChanged}
	}
	if verify := s.client.config.VerifyIdentity; verify != nil {
		if err := verify(ctx, c.InstanceID, c.RecoveryEpoch); err != nil {
			var recovery *events.RecoveryError
			if errors.As(err, &recovery) {
				return "", recovery
			}
			var known *api.Error
			if errors.As(err, &known) && known.Code == "source_recovery_changed" {
				return "", &events.RecoveryError{Reason: events.SourceChanged}
			}
			return "", events.ErrUnavailable
		}
	}
	return c.RecoveryEpoch, nil
}
