// Package events defines the independently authorized source feed. Feed records
// carry no user read authority; delivery requires separate current user checks.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

const MaximumPageSize = 200

var (
	ErrInvalid          = errors.New("invalid monitoring event data")
	ErrUnavailable      = errors.New("monitoring event source unavailable")
	ErrAuthority        = errors.New("monitoring event service authority unavailable")
	ErrRecoveryRequired = errors.New("monitoring event recovery required")
)

type RecoveryReason string

const (
	CursorExpired RecoveryReason = "event_cursor_expired"
	SourceChanged RecoveryReason = "source_recovery_changed"
	ScopeChanged  RecoveryReason = "event_cursor_scope_changed"
	CursorInvalid RecoveryReason = "invalid_cursor"
)

// RecoveryError never contains upstream error messages or a reusable cursor.
// Callers must hold ingestion and explicitly reconcile; no automatic reset is safe.
type RecoveryError struct{ Reason RecoveryReason }

func (e *RecoveryError) Error() string { return string(e.Reason) }
func (e *RecoveryError) Unwrap() error { return ErrRecoveryRequired }

type Source interface {
	SourceID() string
	NamespaceIDs() []string
	Checkpoint(context.Context) (Checkpoint, error)
	Read(context.Context, string, int) (Page, error)
}

// Checkpoint combines the source response with immutable operator configuration.
// DeploymentID and NamespaceIDs are local pins, not invented upstream fields.
type Checkpoint struct {
	DeploymentID                string     `json:"deploymentId"`
	ControlInstanceID           string     `json:"controlInstanceId"`
	RecoveryEpoch               string     `json:"recoveryEpoch"`
	NamespaceIDs                []string   `json:"namespaceIds"`
	AsOf                        time.Time  `json:"asOf"`
	HeadCursor                  string     `json:"headCursor"`
	OldestCursor                string     `json:"oldestCursor"`
	RetentionSeconds            string     `json:"retentionSeconds"`
	BacklogCount                string     `json:"backlogCount"`
	OldestUnpublishedRecordedAt *time.Time `json:"oldestUnpublishedRecordedAt,omitempty"`
}

// Event preserves the original event UUID. RecoveryEpoch is deliberately absent
// from identity: restoring a source must not turn an existing event into a new one.
type Event struct {
	DeploymentID        string     `json:"deploymentId"`
	ControlInstanceID   string     `json:"controlInstanceId"`
	EventID             string     `json:"eventId"`
	Position            string     `json:"position"`
	NamespaceID         string     `json:"namespaceId"`
	JobID               string     `json:"jobId"`
	RunID               string     `json:"runId,omitempty"`
	RunNumber           string     `json:"runNumber,omitempty"`
	OwnerPrincipalID    string     `json:"ownerPrincipalId"`
	OldPhase            string     `json:"oldPhase"`
	NewPhase            string     `json:"newPhase"`
	Outcome             string     `json:"outcome"`
	JobRevision         string     `json:"jobRevision"`
	ObservedCompletedAt *time.Time `json:"observedCompletedAt,omitempty"`
	RecordedAt          time.Time  `json:"recordedAt"`
	Imported            bool       `json:"imported"`
	Reconciliation      bool       `json:"reconciliation"`
}

type Page struct {
	Checkpoint
	Items      []Event `json:"items"`
	NextCursor string  `json:"nextCursor"`
	HasMore    bool    `json:"hasMore"`
}

func (c Checkpoint) Validate() error {
	if !uuid(c.DeploymentID) || !uuid(c.ControlInstanceID) || !decimal(c.RecoveryEpoch, true) || !decimal(c.RetentionSeconds, true) || !decimal(c.BacklogCount, false) || !validTime(c.AsOf) || !ValidCursor(c.HeadCursor) || !ValidCursor(c.OldestCursor) || len(c.NamespaceIDs) == 0 || len(c.NamespaceIDs) > 320 {
		return ErrInvalid
	}
	for i, id := range c.NamespaceIDs {
		if !uuid(id) || i > 0 && c.NamespaceIDs[i-1] >= id {
			return ErrInvalid
		}
	}
	retention, _ := strconv.ParseInt(c.RetentionSeconds, 10, 64)
	if retention < 86400 || retention > 31536000 || (c.BacklogCount == "0") != (c.OldestUnpublishedRecordedAt == nil) || c.OldestUnpublishedRecordedAt != nil && !validTime(*c.OldestUnpublishedRecordedAt) {
		return ErrInvalid
	}
	return nil
}

func (e Event) Validate() error {
	for _, id := range []string{e.DeploymentID, e.ControlInstanceID, e.EventID, e.NamespaceID, e.JobID, e.OwnerPrincipalID} {
		if !uuid(id) {
			return ErrInvalid
		}
	}
	if !decimal(e.Position, true) || !decimal(e.JobRevision, true) || !token(e.OldPhase) || e.OldPhase == "terminal" || e.NewPhase != "terminal" || !token(e.Outcome) || !validTime(e.RecordedAt) || e.ObservedCompletedAt != nil && !validTime(*e.ObservedCompletedAt) {
		return ErrInvalid
	}
	if (e.RunID == "") != (e.RunNumber == "") || e.RunID != "" && (!uuid(e.RunID) || !decimal(e.RunNumber, true)) {
		return ErrInvalid
	}
	encoded, err := json.Marshal(e)
	if err != nil || len(encoded) > 4096 {
		return ErrInvalid
	}
	return nil
}

func (p Page) Validate() error {
	if p.Checkpoint.Validate() != nil || p.Items == nil || len(p.Items) > MaximumPageSize || !ValidCursor(p.NextCursor) || p.HasMore && (len(p.Items) == 0 || p.NextCursor == p.HeadCursor) || !p.HasMore && p.NextCursor != p.HeadCursor {
		return ErrInvalid
	}
	seen := map[string]bool{}
	var previous int64
	for _, e := range p.Items {
		if e.Validate() != nil || e.DeploymentID != p.DeploymentID || e.ControlInstanceID != p.ControlInstanceID || !slices.Contains(p.NamespaceIDs, e.NamespaceID) || seen[e.EventID] {
			return ErrInvalid
		}
		position, _ := strconv.ParseInt(e.Position, 10, 64)
		if position <= previous {
			return ErrInvalid
		}
		seen[e.EventID], previous = true, position
	}
	return nil
}

// ValidCursor treats the token as opaque and bounds storage and URL work without
// decoding, editing, or deriving a new continuation from its internal payload.
func ValidCursor(value string) bool {
	if len(value) == 0 || len(value) > 1024 {
		return false
	}
	for _, c := range value {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func uuid(value string) bool {
	if len(value) != 36 || value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
func decimal(value string, positive bool) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n >= 0 && (!positive || n > 0) && strconv.FormatInt(n, 10) == value
}
func validTime(value time.Time) bool {
	return !value.IsZero() && value.Year() > 0 && value.Year() <= 9999
}
func token(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-", c) {
			return false
		}
	}
	return true
}
