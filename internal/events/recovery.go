package events

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"
)

var (
	ErrRecoveryStale       = errors.New("event recovery plan changed or is no longer applicable")
	ErrRecoveryQuarantined = errors.New("conflicting original event facts require repair before recovery")
	ErrReconciliation      = errors.New("an explicit bounded reconciliation receipt is required")
)

const (
	RecoveryRetained      = "retained"
	RecoveryContinue      = "continue"
	RecoveryReplaying     = "replaying"
	RecoveryReady         = "ready"
	RecoveryApplied       = "applied"
	RecoveryQuarantined   = "quarantined"
	MaximumRecoveryPages  = 10000
	MaximumRecoveryEvents = MaximumRecoveryPages * MaximumPageSize
	MaximumReceiptBytes   = 128 << 10
	MaximumReconciledJobs = 1000000
)

// RecoveryUncertainty is a conservative notification suppression policy, never
// permission to synthesize a transition. A known Dashboard restore requires a
// cutoff through the period whose delivery history may have been lost. The
// operator must obtain that cutoff outside the restored database. Pruned event
// identities additionally raise SuppressRecordedThrough in durable storage.
type RecoveryUncertainty struct {
	DashboardRestored       bool       `json:"dashboardRestored"`
	SuppressAllRecovered    bool       `json:"suppressAllRecovered"`
	SuppressRecordedThrough *time.Time `json:"suppressRecordedThrough,omitempty"`
}

func (u RecoveryUncertainty) Validate() error {
	if u.SuppressRecordedThrough != nil && !validTime(*u.SuppressRecordedThrough) || u.DashboardRestored && u.SuppressRecordedThrough == nil {
		return ErrInvalid
	}
	return nil
}

func (u RecoveryUncertainty) Suppresses(e Event) bool {
	return u.SuppressAllRecovered || u.SuppressRecordedThrough != nil && !e.RecordedAt.After(*u.SuppressRecordedThrough)
}

// RecoveryPlan is operator-only, immutable provenance. It contains private
// opaque cursors, not authorization grants, and must not be projected publicly.
// ConfigurationRevision pins the verified registry configuration. A replacement
// instance can never be accepted under an existing deployment identity.
type RecoveryPlan struct {
	ID                    string              `json:"id"`
	GapID                 string              `json:"gapId"`
	DeploymentID          string              `json:"deploymentId"`
	FeedGeneration        int64               `json:"feedGeneration,string"`
	ConfigurationRevision int64               `json:"configurationRevision,string"`
	Reason                string              `json:"reason"`
	EffectiveReason       string              `json:"effectiveReason"`
	Mode                  string              `json:"mode"`
	PriorCheckpoint       *Checkpoint         `json:"priorCheckpoint,omitempty"`
	PriorNamespaceIDs     []string            `json:"priorNamespaceIds"`
	PriorCursor           string              `json:"priorCursor"`
	PriorLastPosition     int64               `json:"priorLastPosition,string"`
	Checkpoint            Checkpoint          `json:"checkpoint"`
	RemovedNamespaceIDs   []string            `json:"removedNamespaceIds"`
	AddedNamespaceIDs     []string            `json:"addedNamespaceIds"`
	Uncertainty           RecoveryUncertainty `json:"uncertainty"`
	CreatedAt             time.Time           `json:"createdAt"`
	Digest                string              `json:"digest"`
}

// NamespaceChanges keeps existing intervals eligible only in the unchanged
// namespace intersection. Removed intervals need a transactional revocation
// hook before apply. Added namespaces confer no subscription automatically.
func NamespaceChanges(before, after []string) (removed, added []string) {
	removed, added = []string{}, []string{}
	for _, id := range before {
		if !slices.Contains(after, id) {
			removed = append(removed, id)
		}
	}
	for _, id := range after {
		if !slices.Contains(before, id) {
			added = append(added, id)
		}
	}
	return
}

func (p RecoveryPlan) Fingerprint() string {
	p.Digest = ""
	encoded, _ := json.Marshal(p)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (p RecoveryPlan) Validate() error {
	if !uuid(p.ID) || !uuid(p.GapID) || p.DeploymentID != p.Checkpoint.DeploymentID || p.Checkpoint.Validate() != nil || p.FeedGeneration < 1 || p.ConfigurationRevision < 1 || p.PriorLastPosition < 0 || !validTime(p.CreatedAt) || p.Uncertainty.Validate() != nil || !validRecoveryNamespaces(p.PriorNamespaceIDs) || p.Digest != p.Fingerprint() {
		return ErrInvalid
	}
	if p.PriorCheckpoint != nil {
		old := p.PriorCheckpoint
		priorEpoch, _ := strconv.ParseInt(old.RecoveryEpoch, 10, 64)
		epoch, _ := strconv.ParseInt(p.Checkpoint.RecoveryEpoch, 10, 64)
		if old.Validate() != nil || old.DeploymentID != p.DeploymentID || old.ControlInstanceID != p.Checkpoint.ControlInstanceID || epoch < priorEpoch || !slices.Equal(old.NamespaceIDs, p.PriorNamespaceIDs) || !ValidCursor(p.PriorCursor) {
			return ErrInvalid
		}
	} else if p.PriorCursor != "" || p.PriorLastPosition != 0 {
		return ErrInvalid
	}
	removed, added := NamespaceChanges(p.PriorNamespaceIDs, p.Checkpoint.NamespaceIDs)
	if !slices.Equal(removed, p.RemovedNamespaceIDs) || !slices.Equal(added, p.AddedNamespaceIDs) {
		return ErrInvalid
	}
	if p.Reason == "event_conflict" {
		return ErrRecoveryQuarantined
	}
	effective := p.Reason
	if p.Reason == "capacity" && p.PriorCheckpoint != nil {
		if p.PriorCheckpoint.RecoveryEpoch != p.Checkpoint.RecoveryEpoch {
			effective = string(SourceChanged)
		} else if len(removed)+len(added) != 0 {
			effective = string(ScopeChanged)
		}
	}
	if p.EffectiveReason != effective {
		return ErrInvalid
	}
	if effective == "capacity" {
		if p.Mode != RecoveryContinue || p.PriorCheckpoint == nil || p.PriorCheckpoint.RecoveryEpoch != p.Checkpoint.RecoveryEpoch || len(removed)+len(added) != 0 {
			return ErrInvalid
		}
	} else if p.Mode != RecoveryRetained || !slices.Contains([]string{string(CursorExpired), string(SourceChanged), string(ScopeChanged), string(CursorInvalid)}, effective) {
		return ErrInvalid
	}
	return nil
}

func validRecoveryNamespaces(ids []string) bool {
	if len(ids) < 1 || len(ids) > 320 {
		return false
	}
	for i, id := range ids {
		if !uuid(id) || i > 0 && ids[i-1] >= id {
			return false
		}
	}
	return true
}

type RecoveryState struct {
	Plan         RecoveryPlan
	Status       string
	Revision     int64
	Cursor       string
	LastPosition int64
	Checkpoint   Checkpoint
	Pages        int64
	Scanned      int64
	Added        int64
	LeaseToken   string
}

// NamespaceReconciliation describes a bounded current-state scan, not a frozen
// terminal snapshot and not proof of complete event delivery. No job content,
// event UUID or synthetic transition belongs in this receipt.
type NamespaceReconciliation struct {
	NamespaceID string     `json:"namespaceId"`
	Status      string     `json:"status"` // complete, partial, unavailable, inaccessible
	JobsRead    int64      `json:"jobsRead,string"`
	AsOf        *time.Time `json:"asOf,omitempty"`
}

type ReconciliationReceipt struct {
	PlanDigest      string                    `json:"planDigest"`
	AcknowledgedGap bool                      `json:"acknowledgedGap"`
	Namespaces      []NamespaceReconciliation `json:"namespaces"`
	CompletedAt     time.Time                 `json:"completedAt"`
}

func (r ReconciliationReceipt) Validate(p RecoveryPlan) error {
	if p.Validate() != nil || r.PlanDigest != p.Digest || !r.AcknowledgedGap || !validTime(r.CompletedAt) || r.CompletedAt.Before(p.CreatedAt) || len(r.Namespaces) != len(p.Checkpoint.NamespaceIDs) {
		return ErrReconciliation
	}
	var total int64
	for i, n := range r.Namespaces {
		if n.NamespaceID != p.Checkpoint.NamespaceIDs[i] || n.JobsRead < 0 || n.JobsRead > MaximumReconciledJobs || n.AsOf != nil && !validTime(*n.AsOf) {
			return ErrReconciliation
		}
		switch n.Status {
		case "complete", "partial":
			if n.AsOf == nil {
				return ErrReconciliation
			}
		case "unavailable", "inaccessible":
			if n.JobsRead != 0 || n.AsOf != nil {
				return ErrReconciliation
			}
		default:
			return ErrReconciliation
		}
		total += n.JobsRead
	}
	encoded, err := json.Marshal(r)
	if total > MaximumReconciledJobs || err != nil || len(encoded) > MaximumReceiptBytes {
		return ErrReconciliation
	}
	return nil
}

// TerminalReconciler is implemented by the represented-user bridge. It may read
// only currently authorized, explicitly monitored subjects, must bound scans,
// and records unavailable coverage honestly. It must never produce Events.
type TerminalReconciler interface {
	Reconcile(context.Context, RecoveryPlan) (ReconciliationReceipt, error)
}

// RecoveryJournal is deliberately separate from normal ingestion. The normal
// feed stays paused until an operator explicitly applies a ready plan and its
// reconciliation receipt. Each append must recheck registry pins and its lease.
type RecoveryJournal interface {
	ClaimEventRecovery(context.Context, string) (RecoveryState, error)
	AppendEventRecovery(context.Context, RecoveryState, Page) error
	ReleaseEventRecovery(context.Context, RecoveryState) error
}

type Recoverer struct {
	source  Source
	journal RecoveryJournal
}

func NewRecoverer(source Source, journal RecoveryJournal) (*Recoverer, error) {
	if source == nil || journal == nil || !uuid(source.SourceID()) || !validRecoveryNamespaces(source.NamespaceIDs()) {
		return nil, ErrInvalid
	}
	return &Recoverer{source, journal}, nil
}

// Step performs one source page outside any database transaction. A timeout or
// failed commit leaves the previous private cursor intact. Ready is not applied.
func (r *Recoverer) Step(ctx context.Context, id string) error {
	if !uuid(id) {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, IngestionTimeout)
	defer cancel()
	state, err := r.journal.ClaimEventRecovery(ctx, id)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer done()
		_ = r.journal.ReleaseEventRecovery(cleanup, state)
	}()
	if state.Plan.Validate() != nil || state.Status != RecoveryReplaying || state.Plan.DeploymentID != r.source.SourceID() || !slices.Equal(state.Plan.Checkpoint.NamespaceIDs, r.source.NamespaceIDs()) || !ValidCursor(state.Cursor) || state.Pages >= MaximumRecoveryPages {
		return ErrRecoveryStale
	}
	page, err := r.source.Read(ctx, state.Cursor, MaximumPageSize)
	if err != nil {
		return err
	}
	return r.journal.AppendEventRecovery(ctx, state, page)
}
