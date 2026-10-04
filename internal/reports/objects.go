package reports

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/ryancswallace/jobman-diagnose/diagnosis"
)

const maximumPairBytes = (6 << 20) + (16 << 10)

// Pair keeps the exact original sealed evidence and report together. The
// surrounding subject is a Dashboard queue/cache identity, not a replacement
// for either original semantic ID.
type Pair struct {
	Subject  Subject
	Evidence diagnosis.FailureEvidence
	Report   diagnosis.Report
}

func (p Pair) Validate() error {
	if p.Subject.Validate() != nil || diagnosis.ValidateAgainstEvidence(p.Report, p.Evidence) != nil {
		return ErrObject
	}
	s, e := p.Subject, p.Evidence.Core
	if p.Report.Versions.CompanionVersion != s.CompanionVersion || e.Source.JobmanVersion != s.JobmanVersion {
		return ErrObject
	}
	if e.Shared == nil || p.Report.Shared == nil || e.Shared.Source.DeploymentID != s.DeploymentID || e.Shared.Source.ControlInstanceID != s.ControlInstanceID || e.Shared.Source.NamespaceID != s.NamespaceID || e.Subject.JobID != s.JobID || strconv.FormatUint(e.Subject.JobRevision, 10) != s.Revision || e.Shared.Profile != s.Profile || e.Source.CollectorVersion != s.CollectorVersion || p.Report.Versions.EngineVersion != s.EngineVersion {
		return ErrObject
	}
	if s.RunID != "" && (len(e.Shared.Runs) != 1 || e.Shared.Runs[0].ID != s.RunID) {
		return ErrObject
	}
	return nil
}

type diskPair struct {
	Version  int             `json:"version"`
	Subject  Subject         `json:"subject"`
	Evidence json.RawMessage `json:"evidence"`
	Report   json.RawMessage `json:"report"`
}

// ObjectStore owns one private local directory. Callers must complete current
// authorization before returning any loaded object. No filesystem path is ever
// returned to an API client or accepted from one.
type ObjectStore struct {
	root     *os.Root
	sweepMu  sync.Mutex
	sweepDir *os.File
}

func OpenObjects(path string) (*ObjectStore, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrObject
	}
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, ErrObject
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrObject
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, ErrObject
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, ErrObject
	}
	return &ObjectStore{root: root}, nil
}
func (s *ObjectStore) Close() error {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	if s.sweepDir != nil {
		_ = s.sweepDir.Close()
		s.sweepDir = nil
	}
	return s.root.Close()
}

func encodePair(pair Pair) ([]byte, error) {
	if pair.Validate() != nil {
		return nil, ErrObject
	}
	var evidence, report bytes.Buffer
	if diagnosis.EncodeFailureEvidence(&evidence, pair.Evidence) != nil || diagnosis.Encode(&report, pair.Report) != nil {
		return nil, ErrObject
	}
	// Decode before publication enforces the public wire byte/depth ceilings,
	// including escaping expansion, in addition to semantic validation above.
	if _, err := diagnosis.DecodeFailureEvidence(bytes.NewReader(evidence.Bytes()), diagnosis.DecodeLimits{}); err != nil {
		return nil, ErrObject
	}
	if _, err := diagnosis.Decode(bytes.NewReader(report.Bytes()), diagnosis.DecodeLimits{}); err != nil {
		return nil, ErrObject
	}
	data, err := json.Marshal(diskPair{1, pair.Subject, evidence.Bytes(), report.Bytes()})
	if err != nil || len(data) > maximumPairBytes {
		return nil, ErrObject
	}
	return data, nil
}

// Put writes private bytes, syncs them, then atomically links the completed file
// under a new UUID. Link's exclusive destination semantics prevent overwriting
// an existing pair, including on concurrent retries. The temporary name is
// removed and directory synced before any ready database reference is published.
func (s *ObjectStore) Put(ctx context.Context, id string, pair Pair) (Object, error) {
	if !uuidPattern.MatchString(id) || ctx.Err() != nil {
		return Object{}, ErrObject
	}
	data, err := encodePair(pair)
	if err != nil {
		return Object{}, err
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return Object{}, ErrObject
	}
	name := ".pending-" + hex.EncodeToString(nonce[:])
	file, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Object{}, ErrObject
	}
	defer s.root.Remove(name)
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil || ctx.Err() != nil {
		return Object{}, ErrObject
	}
	if err = s.root.Link(name, id+".json"); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Object{}, ErrConflict
		}
		return Object{}, ErrObject
	}
	if s.root.Remove(name) != nil || s.sync() != nil {
		return Object{}, ErrObject
	}
	return objectFor(id, data, pair), nil
}

func objectFor(id string, data []byte, pair Pair) Object {
	sum := sha256.Sum256(data)
	return Object{ID: id, SHA256: hex.EncodeToString(sum[:]), ReportID: pair.Report.ReportID, EvidenceID: pair.Evidence.Core.EvidenceID, AnalysisEvidenceID: pair.Evidence.AnalysisEvidenceID}
}
func (s *ObjectStore) sync() error {
	f, err := s.root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (s *ObjectStore) Read(ctx context.Context, expected Subject, object Object) (Pair, error) {
	if object.Validate() != nil || expected.Validate() != nil || ctx.Err() != nil {
		return Pair{}, ErrObject
	}
	pair, actual, err := s.Recover(ctx, expected, object.ID)
	if err != nil || actual != object {
		return Pair{}, ErrObject
	}
	return pair, nil
}

// Recover supports a crash after private publication but before committing its
// database reference. It verifies the entire pair against the claimed task's
// subject; callers must still own the current lease and reauthorize publication.
func (s *ObjectStore) Recover(ctx context.Context, expected Subject, id string) (Pair, Object, error) {
	if !uuidPattern.MatchString(id) || expected.Validate() != nil || ctx.Err() != nil {
		return Pair{}, Object{}, ErrObject
	}
	name := id + ".json"
	info, err := s.root.Lstat(name)
	if err != nil {
		return Pair{}, Object{}, ErrObject
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() < 1 || info.Size() > maximumPairBytes {
		return Pair{}, Object{}, ErrObject
	}
	file, err := s.root.Open(name)
	if err != nil {
		return Pair{}, Object{}, ErrObject
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return Pair{}, Object{}, ErrObject
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumPairBytes+1))
	if err != nil || len(data) > maximumPairBytes || ctx.Err() != nil {
		return Pair{}, Object{}, ErrObject
	}
	var disk diskPair
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&disk) != nil || disk.Version != 1 || disk.Subject != expected {
		return Pair{}, Object{}, ErrObject
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return Pair{}, Object{}, ErrObject
	}
	evidence, err := diagnosis.DecodeFailureEvidence(bytes.NewReader(disk.Evidence), diagnosis.DecodeLimits{})
	if err != nil {
		return Pair{}, Object{}, ErrObject
	}
	report, err := diagnosis.Decode(bytes.NewReader(disk.Report), diagnosis.DecodeLimits{})
	if err != nil {
		return Pair{}, Object{}, ErrObject
	}
	pair := Pair{disk.Subject, evidence, report}
	if pair.Validate() != nil {
		return Pair{}, Object{}, ErrObject
	}
	return pair, objectFor(id, data, pair), nil
}

// Remove is used only after the database reference has expired. One unlink
// removes report and evidence together. The ID is a validated internal UUID.
func (s *ObjectStore) Remove(id string) error {
	if !uuidPattern.MatchString(id) {
		return ErrObject
	}
	if err := s.root.Remove(id + ".json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrObject
	}
	if s.sync() != nil {
		return ErrObject
	}
	return nil
}
