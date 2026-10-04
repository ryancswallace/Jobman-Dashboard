package reports

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

func TestQueueIdentityBindsEverySourceAndDisclosureField(t *testing.T) {
	id := "10000000-0000-4000-8000-000000000001"
	s := Subject{Scope: api.Scope{DeploymentID: id, NamespaceID: id}, ControlInstanceID: id, RecoveryEpoch: "1", JobID: id, Revision: "1", Profile: "metadata", SnapshotFingerprint: strings.Repeat("a", 64), EngineVersion: "test", CollectorVersion: "1", CompanionVersion: "test", JobmanVersion: "test"}
	want, err := s.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Subject){
		func(s *Subject) { s.DeploymentID = "20000000-0000-4000-8000-000000000001" },
		func(s *Subject) { s.NamespaceID = "20000000-0000-4000-8000-000000000001" },
		func(s *Subject) { s.ControlInstanceID = "20000000-0000-4000-8000-000000000001" },
		func(s *Subject) { s.JobID = "20000000-0000-4000-8000-000000000001" },
		func(s *Subject) { s.RunID = "20000000-0000-4000-8000-000000000001" },
		func(s *Subject) { s.RecoveryEpoch = "2" }, func(s *Subject) { s.Revision = "2" },
		func(s *Subject) { s.Profile = "include_log_tail" }, func(s *Subject) { s.SnapshotFingerprint = strings.Repeat("b", 64) },
		func(s *Subject) { s.EngineVersion = "changed" }, func(s *Subject) { s.CollectorVersion = "changed" },
	} {
		candidate := s
		change(&candidate)
		got, err := candidate.Fingerprint()
		if err != nil || bytes.Equal(got, want) {
			t.Fatalf("identity did not distinguish a changed source: %v", err)
		}
	}
	for _, number := range []string{"0", "01", "-1", "9223372036854775808"} {
		bad := s
		bad.Revision = number
		if bad.Validate() == nil {
			t.Fatal("noncanonical revision accepted")
		}
	}
	for _, key := range []string{"short", strings.Repeat("a", 129), "synthetic key has spaces", "synthetic\nnewline"} {
		if _, err := IdempotencyHash(key); err == nil {
			t.Fatal("invalid idempotency key accepted")
		}
	}
}
