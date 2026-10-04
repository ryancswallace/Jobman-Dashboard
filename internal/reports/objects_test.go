package reports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-diagnose/deterministic"
	"github.com/ryancswallace/jobman/diagnostic"
)

func fixturePair(t *testing.T) Pair {
	t.Helper()
	data, err := os.ReadFile("testdata/shared-control-failure-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != "65af51d47996bcc7fe2e0508062b9a84544963829457d257db7b87a6f8a0acf2" {
		t.Fatal("immutable public fixture changed")
	}
	core, err := diagnostic.Decode(bytes.NewReader(data), diagnostic.DecodeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := deterministic.Prepare(t.Context(), core)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := deterministic.New("test-build", func() time.Time { return core.CapturedAt })
	if err != nil {
		t.Fatal(err)
	}
	report, err := engine.Diagnose(t.Context(), evidence)
	if err != nil {
		t.Fatal(err)
	}
	subject := Subject{Scope: api.Scope{DeploymentID: core.Shared.Source.DeploymentID, NamespaceID: core.Shared.Source.NamespaceID}, ControlInstanceID: core.Shared.Source.ControlInstanceID, RecoveryEpoch: "1", JobID: core.Subject.JobID, Revision: strconv.FormatUint(core.Subject.JobRevision, 10), RunID: core.Shared.Runs[0].ID, Profile: core.Shared.Profile, SnapshotFingerprint: strings.Repeat("a", 64), EngineVersion: report.Versions.EngineVersion, CollectorVersion: core.Source.CollectorVersion, CompanionVersion: report.Versions.CompanionVersion, JobmanVersion: core.Source.JobmanVersion}
	pair := Pair{subject, evidence, report}
	if pair.Validate() != nil {
		t.Fatal("public deterministic pair did not match shared identity")
	}
	return pair
}

func TestPrivatePairPublicationRecoveryAndImmutableIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "objects")
	store, err := OpenObjects(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pair := fixturePair(t)
	const id = "90000000-0000-4000-8000-000000000001"
	var wg sync.WaitGroup
	results := make(chan Object, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			object, err := store.Put(t.Context(), id, pair)
			if err != nil {
				errs <- err
			} else {
				results <- object
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	if len(results) != 1 || len(errs) != 1 {
		t.Fatal("pair was overwritten or duplicate publication succeeded")
	}
	if err := <-errs; !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	object := <-results
	got, err := store.Read(t.Context(), pair.Subject, object)
	if err != nil || got.Report.ReportID != pair.Report.ReportID || got.Evidence.AnalysisEvidenceID != pair.Evidence.AnalysisEvidenceID {
		t.Fatal("immutable pair identities differ")
	}
	_, recovered, err := store.Recover(t.Context(), pair.Subject, id)
	if err != nil || recovered != object {
		t.Fatal("published-but-uncommitted pair cannot be recovered")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 1 || entries[0].Name() != id+".json" {
		t.Fatal("partial file remains after publication")
	}
	info, err := os.Stat(filepath.Join(path, id+".json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("stored evidence is not private")
	}
	wrong := object
	wrong.ReportID = "sha256:" + strings.Repeat("0", 64)
	if _, err = store.Read(t.Context(), pair.Subject, wrong); err == nil {
		t.Fatal("database identity mismatch returned evidence")
	}
	for _, change := range []func(*Subject){func(s *Subject) { s.DeploymentID = id }, func(s *Subject) { s.NamespaceID = id }, func(s *Subject) { s.JobID = id }, func(s *Subject) { s.RecoveryEpoch = "2" }, func(s *Subject) { s.Profile = "include_log_tail" }, func(s *Subject) { s.SnapshotFingerprint = strings.Repeat("b", 64) }} {
		subject := pair.Subject
		change(&subject)
		if _, err = store.Read(t.Context(), subject, object); err == nil {
			t.Fatal("stored pair crossed source or disclosure boundary")
		}
	}
	if err = store.Remove(id); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Read(t.Context(), pair.Subject, object); err == nil {
		t.Fatal("pair still readable after paired removal")
	}
}

func TestPairRejectsCorruptionCrossEvidenceAndNonprivateFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "objects")
	store, err := OpenObjects(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pair := fixturePair(t)
	const id = "90000000-0000-4000-8000-000000000002"
	bad := pair
	bad.Report.ReportID = "sha256:" + strings.Repeat("0", 64)
	if _, err = store.Put(t.Context(), id, bad); err == nil {
		t.Fatal("invalid report was published")
	}
	bad = pair
	bad.Subject.Profile = "include_log_tail"
	if _, err = store.Put(t.Context(), id, bad); err == nil {
		t.Fatal("subject relabeled evidence disclosure")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = store.Put(canceled, id, pair); err == nil {
		t.Fatal("canceled analysis published")
	}
	object, err := store.Put(t.Context(), id, pair)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, id+".json")
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(file, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Read(t.Context(), pair.Subject, object); err == nil {
		t.Fatal("nonprivate evidence file read")
	}
	if err = os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte("{invalid-secret-canary"), append(bytes.Clone(original), []byte("{}")...), bytes.Replace(original, []byte(pair.Report.ReportID), []byte("sha256:"+strings.Repeat("0", 64)), 1)} {
		if err = os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = store.Read(t.Context(), pair.Subject, object); err == nil || strings.Contains(err.Error(), "secret-canary") {
			t.Fatal("corrupt pair accepted or source contents exposed")
		}
	}
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(path, "other.json")
	if err = os.WriteFile(other, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("other.json", file); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Read(t.Context(), pair.Subject, object); err == nil {
		t.Fatal("linked object was accepted")
	}
	if _, err = store.Put(t.Context(), "../escape", pair); err == nil {
		t.Fatal("object path traversal accepted")
	}
	if err = store.Remove("../escape"); err == nil {
		t.Fatal("remove escaped object root")
	}
}

func TestObjectRootRequiresPrivateRealDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenObjects(path); err == nil {
		t.Fatal("public object root accepted")
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenObjects(link); err == nil {
		t.Fatal("linked root accepted")
	}
	if _, err := OpenObjects("relative"); err == nil {
		t.Fatal("relative object root accepted")
	}
}
