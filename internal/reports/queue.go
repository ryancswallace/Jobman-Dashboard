// Package reports owns bounded deterministic diagnosis work and immutable pairs.
// Source access is checked by the service; neither a queue row nor an object ID
// grants access to the job, report, or its citations.
package reports

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

const (
	MaximumPending        = 512
	MaximumAccountPending = 8
	MaximumRequesters     = 32
	MaximumAttempts       = 3
	TaskTimeout           = 60 * time.Second
	LeaseDuration         = 90 * time.Second
	Retention             = 30 * 24 * time.Hour
)

var (
	ErrConflict   = errors.New("diagnosis request conflict")
	ErrLimit      = errors.New("diagnosis queue limit reached")
	ErrLease      = errors.New("diagnosis lease unavailable")
	ErrInvalid    = errors.New("invalid diagnosis state")
	ErrObject     = errors.New("stored diagnosis pair unavailable or invalid")
	uuidPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Subject pins the source snapshot chosen before queue admission. The
// fingerprint covers factual snapshot provenance (including selected manifests)
// but excludes capture time. A changed snapshot is a distinct analysis request.
type Subject struct {
	api.Scope
	ControlInstanceID   string `json:"controlInstanceId"`
	RecoveryEpoch       string `json:"recoveryEpoch"`
	JobID               string `json:"jobId"`
	Revision            string `json:"revision"`
	RunID               string `json:"runId,omitempty"`
	Profile             string `json:"profile"`
	SnapshotFingerprint string `json:"snapshotFingerprint"`
	EngineVersion       string `json:"engineVersion"`
	CollectorVersion    string `json:"collectorVersion"`
	CompanionVersion    string `json:"companionVersion"`
	JobmanVersion       string `json:"jobmanVersion"`
}

func (s Subject) Validate() error {
	for _, id := range []string{s.DeploymentID, s.NamespaceID, s.ControlInstanceID, s.JobID} {
		if !uuidPattern.MatchString(id) {
			return ErrInvalid
		}
	}
	if s.RunID != "" && !uuidPattern.MatchString(s.RunID) {
		return ErrInvalid
	}
	for _, number := range []string{s.Revision, s.RecoveryEpoch} {
		n, err := strconv.ParseInt(number, 10, 64)
		if err != nil || n < 1 || strconv.FormatInt(n, 10) != number {
			return ErrInvalid
		}
	}
	if s.Profile != "metadata" && s.Profile != "include_log_tail" {
		return ErrInvalid
	}
	if !digestPattern.MatchString(s.SnapshotFingerprint) {
		return ErrInvalid
	}
	for _, version := range []string{s.EngineVersion, s.CollectorVersion, s.CompanionVersion, s.JobmanVersion} {
		if len(version) == 0 || len(version) > 128 {
			return ErrInvalid
		}
	}
	return nil
}

func (s Subject) Fingerprint() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return nil, ErrInvalid
	}
	sum := sha256.Sum256(data)
	return sum[:], nil
}

// RequestFingerprint binds the caller's request, independently of a source
// snapshot that can advance while an HTTP retry is in flight. Replaying a key
// must return the original task, even after the job advances to a new revision.
func (s Subject) RequestFingerprint() ([]byte, error) {
	if s.Validate() != nil {
		return nil, ErrInvalid
	}
	data, err := json.Marshal(struct {
		Scope                 api.Scope
		JobID, RunID, Profile string
	}{s.Scope, s.JobID, s.RunID, s.Profile})
	if err != nil {
		return nil, ErrInvalid
	}
	sum := sha256.Sum256(data)
	return sum[:], nil
}

// Object describes one private file containing both the exact sealed analysis
// evidence and report. Original semantic IDs are never relabeled as task IDs.
type Object struct {
	ID                 string `json:"id"`
	SHA256             string `json:"sha256"`
	ReportID           string `json:"reportId"`
	EvidenceID         string `json:"evidenceId"`
	AnalysisEvidenceID string `json:"analysisEvidenceId"`
}

func (o Object) Validate() error {
	if !uuidPattern.MatchString(o.ID) || !digestPattern.MatchString(o.SHA256) {
		return ErrInvalid
	}
	for _, id := range []string{o.ReportID, o.EvidenceID, o.AnalysisEvidenceID} {
		if len(id) != 71 || id[:7] != "sha256:" || !digestPattern.MatchString(id[7:]) {
			return ErrInvalid
		}
	}
	return nil
}

type Task struct {
	ID             string
	Subject        Subject
	State          string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	Attempts       int
	LeaseToken     string
	LeaseExpiresAt *time.Time
	FailureCode    string
	Object         *Object
}

type Claim struct {
	Task       Task
	Requesters []monitoring.Actor
}

func IdempotencyHash(key string) ([]byte, error) {
	if len(key) < 16 || len(key) > 128 {
		return nil, ErrInvalid
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return nil, ErrInvalid
		}
	}
	sum := sha256.Sum256([]byte(key))
	return sum[:], nil
}
