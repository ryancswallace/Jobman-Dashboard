package reports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/ryancswallace/jobman/diagnostic"
)

// ErrRedactionUnavailable means the explicit log profile cannot be offered
// until the operator configures a value-aware redaction policy.
var ErrRedactionUnavailable = errors.New("diagnosis log redaction policy is unavailable")

// CollectionOptions selects disclosure and a per-stream byte budget. Metadata
// collection never invokes a log reader. Zero LogBytes selects the public core
// 64 KiB limit only when IncludeLogTail is true.
type CollectionOptions struct {
	IncludeLogTail bool
	LogBytes       uint64
	Redaction      *RedactionPolicy
}

// PreparedCollection keeps its bounded snapshot, requested selection and policy
// private and immutable. Metadata is a separately decoded public sealed value;
// mutating that exported value cannot change subsequent collection. The worker
// must independently reauthorize and compare SnapshotFingerprint before work.
type PreparedCollection struct {
	Metadata            diagnostic.Evidence
	SnapshotFingerprint string
	PolicyFingerprint   string
	snapshot            []byte
	selection           diagnostic.SharedSelection
	options             CollectionOptions
}

// PrepareCollection validates the supplied authorized snapshot with the real
// public collector. Its fingerprint excludes incidental capture time while
// retaining source observation times, revisions, run/log identities, omissions,
// recovery epoch, disclosure and redaction policy. No raw log bytes are read.
func PrepareCollection(ctx context.Context, snapshot Snapshot, selection diagnostic.SharedSelection, options CollectionOptions) (PreparedCollection, error) {
	if err := ctx.Err(); err != nil {
		return PreparedCollection{}, err
	}
	epoch, err := strconv.ParseInt(snapshot.RecoveryEpoch, 10, 64)
	if err != nil || epoch < 1 || strconv.FormatInt(epoch, 10) != snapshot.RecoveryEpoch || options.LogBytes > diagnostic.SharedMaximumTailBytes || (!options.IncludeLogTail && options.LogBytes != 0) {
		return PreparedCollection{}, ErrInvalid
	}
	policyID, profile := "", diagnostic.SharedProfileMetadata
	if options.IncludeLogTail {
		if !options.Redaction.ValueRedactionConfigured() {
			return PreparedCollection{}, ErrRedactionUnavailable
		}
		policyID, profile = options.Redaction.Fingerprint(), diagnostic.SharedProfileIncludeLogTail
		if options.LogBytes == 0 {
			options.LogBytes = diagnostic.SharedMaximumTailBytes
		}
	} else {
		// A configured but unused policy must not churn metadata cache identity.
		options.Redaction = nil
	}
	if err := diagnostic.ValidateSharedSnapshot(snapshot.Value); err != nil {
		return PreparedCollection{}, ErrInvalid
	}
	encoded, err := json.Marshal(snapshot.Value)
	if err != nil || len(encoded) > diagnostic.SharedMaximumBytes {
		return PreparedCollection{}, ErrInvalid
	}
	reader := preparedSnapshotReader(encoded)
	collector := diagnostic.SharedCollector{Snapshots: reader}
	metadata, err := collector.Collect(ctx, diagnostic.SharedCollectionRequest{Selection: selection})
	if err != nil {
		return PreparedCollection{}, err
	}
	fingerprint, err := json.Marshal(struct {
		Version, MetadataID, Epoch, Profile, Policy string
		LogBytes                                    uint64
	}{"jobman.dashboard.collection/v1", metadata.EvidenceID, snapshot.RecoveryEpoch, profile, policyID, options.LogBytes})
	if err != nil {
		return PreparedCollection{}, ErrInvalid
	}
	sum := sha256.Sum256(fingerprint)
	return PreparedCollection{Metadata: metadata, SnapshotFingerprint: hex.EncodeToString(sum[:]), PolicyFingerprint: policyID, snapshot: encoded, selection: selection, options: options}, nil
}

// Collect seals the captured snapshot and, only for the explicitly requested log
// profile, authorized manifest-pinned tails supplied by the caller. Reader
// errors (including revocation) propagate; no partial evidence is returned.
func (prepared PreparedCollection) Collect(ctx context.Context, logs diagnostic.LogReader) (diagnostic.Evidence, error) {
	if len(prepared.snapshot) == 0 {
		return diagnostic.Evidence{}, ErrInvalid
	}
	collector := diagnostic.SharedCollector{Snapshots: preparedSnapshotReader(prepared.snapshot), Logs: logs, Sanitizer: prepared.options.Redaction}
	return collector.Collect(ctx, diagnostic.SharedCollectionRequest{Selection: prepared.selection, IncludeLogTail: prepared.options.IncludeLogTail, LogBytes: prepared.options.LogBytes})
}

type preparedSnapshotReader []byte

func (reader preparedSnapshotReader) ReadSnapshot(ctx context.Context, _ diagnostic.SharedSelection) (diagnostic.SharedSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return diagnostic.SharedSnapshot{}, err
	}
	return diagnostic.DecodeSharedSnapshot(bytes.NewReader(reader))
}
