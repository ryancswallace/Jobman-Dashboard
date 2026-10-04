package reports

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/ryancswallace/jobman-dashboard/internal/observability"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

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
type objectDirectory struct {
	observer *observability.Registry
	root     *os.Root
	access   ObjectAccess
}

type ObjectStore struct {
	*objectDirectory
	sweepMu  sync.Mutex
	sweepDir *os.File
}

func OpenObjects(path string) (*ObjectStore, error) {
	return OpenObjectsWithAccess(path, ObjectAccess{})
}

func OpenObjectsWithAccess(path string, access ObjectAccess) (*ObjectStore, error) {
	directory, err := openObjectDirectory(path, access, true)
	if err != nil {
		return nil, err
	}
	return &ObjectStore{objectDirectory: directory}, nil
}

func openObjectDirectory(path string, access ObjectAccess, writer bool) (*objectDirectory, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || access.Validate() != nil {
		return nil, ErrObject
	}
	// Only the legacy private writer may create its root. A shared root must be
	// provisioned by an operator with the intended immutable UID/GID/mode.
	if writer && access.Mode == "" {
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, ErrObject
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != access.directoryMode() || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, ErrObject
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, ErrObject
	}
	directory := &objectDirectory{root: root, access: access}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) || directory.validateRoot(writer) != nil {
		root.Close()
		return nil, ErrObject
	}
	return directory, nil
}

func (s *objectDirectory) validateRoot(writer bool) error {
	file, err := openObjectNoFollow(s.root, ".")
	if err != nil {
		return ErrObject
	}
	defer file.Close()
	return validateObjectAccess(file, s.access, true, writer)
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
func (s *ObjectStore) Put(ctx context.Context, id string, pair Pair) (result Object, resultErr error) {
	start := time.Now()
	defer func() {
		s.observer.Observe("object", "publish", "", "", reportObservation(resultErr), time.Since(start))
	}()
	if !uuidPattern.MatchString(id) || ctx.Err() != nil || s.validateRoot(true) != nil {
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
	// Check inherited ACLs while the temporary inode still has mode 0600.
	if err = prepareObjectFile(file, s.access); err != nil {
		file.Close()
		return Object{}, ErrObject
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = publishObjectPermissions(file, s.access)
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil || ctx.Err() != nil || s.validateRoot(true) != nil {
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
	return s.objectDirectory.read(ctx, expected, object)
}

func (s *objectDirectory) read(ctx context.Context, expected Subject, object Object) (result Pair, resultErr error) {
	start := time.Now()
	defer func() { s.observer.Observe("object", "read", "", "", reportObservation(resultErr), time.Since(start)) }()
	if object.Validate() != nil || expected.Validate() != nil || ctx.Err() != nil {
		return Pair{}, ErrObject
	}
	pair, actual, err := s.recover(ctx, expected, object.ID)
	if err != nil || actual != object {
		return Pair{}, ErrObject
	}
	return pair, nil
}

// Recover supports a crash after private publication but before committing its
// database reference. It verifies the entire pair against the claimed task's
// subject; callers must still own the current lease and reauthorize publication.
func (s *ObjectStore) Recover(ctx context.Context, expected Subject, id string) (result Pair, resultObject Object, resultErr error) {
	start := time.Now()
	defer func() {
		s.observer.Observe("object", "recover", "", "", reportObservation(resultErr), time.Since(start))
	}()
	return s.objectDirectory.recover(ctx, expected, id)
}

func (s *objectDirectory) recover(ctx context.Context, expected Subject, id string) (Pair, Object, error) {
	if !uuidPattern.MatchString(id) || expected.Validate() != nil || ctx.Err() != nil || s.validateRoot(false) != nil {
		return Pair{}, Object{}, ErrObject
	}
	name := id + ".json"
	info, err := s.root.Lstat(name)
	if err != nil {
		return Pair{}, Object{}, ErrObject
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != s.access.objectMode() || info.Size() < 1 || info.Size() > maximumPairBytes {
		return Pair{}, Object{}, ErrObject
	}
	file, err := openObjectNoFollow(s.root, name)
	if err != nil {
		return Pair{}, Object{}, ErrObject
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || validateObjectAccess(file, s.access, false, false) != nil {
		return Pair{}, Object{}, ErrObject
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumPairBytes+1))
	if err != nil || len(data) > maximumPairBytes || ctx.Err() != nil || validateObjectAccess(file, s.access, false, false) != nil || s.validateRoot(false) != nil {
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
func (s *ObjectStore) Remove(id string) (resultErr error) {
	start := time.Now()
	defer func() {
		s.observer.Observe("object", "remove", "", "", reportObservation(resultErr), time.Since(start))
	}()
	if !uuidPattern.MatchString(id) || s.validateRoot(true) != nil {
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
