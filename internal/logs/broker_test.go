package logs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type testSource struct {
	mu       sync.Mutex
	manifest Manifest
	calls    int
	after    func(*testSource)
	err      error
}

func (s *testSource) Manifest(_ context.Context, _ monitoring.Actor, q ManifestQuery) (Manifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.after != nil {
		s.after(s)
	}
	if s.err != nil {
		return Manifest{}, s.err
	}
	m := s.manifest
	m.Chunks = nil
	start := max(int64(0), m.ByteLength-int64(q.TailBytes))
	if q.Offset != nil {
		start = *q.Offset
	}
	if q.AfterSequence != nil {
		start = 0
	}
	for _, c := range s.manifest.Chunks {
		if q.AfterSequence != nil && c.Sequence <= *q.AfterSequence {
			continue
		}
		if c.ByteOffset+c.ByteLength > start || c.ByteLength == 0 {
			m.Chunks = append(m.Chunks, c)
			if len(m.Chunks) == q.Limit {
				break
			}
		}
	}
	return m, nil
}

type testReader struct {
	data  map[string][]byte
	state string
	calls int
}

func (r *testReader) Read(_ context.Context, q FileRequest) FileResult {
	r.calls++
	if r.state != "" {
		return FileResult{State: r.state}
	}
	return FileResult{State: "ok", Bytes: r.data[q.ObjectKey]}
}

func brokerFixture(t *testing.T) (*Broker, *testSource, *testReader, monitoring.Actor, Request) {
	t.Helper()
	now := time.Now().UTC()
	scope := api.Scope{DeploymentID: "11111111-1111-4111-8111-111111111111", NamespaceID: "22222222-2222-4222-8222-222222222222"}
	m := Manifest{Scope: scope, NamespaceName: "research", JobID: "33333333-3333-4333-8333-333333333333", TargetGenerationID: "44444444-4444-4444-8444-444444444444", RunID: "55555555-5555-4555-8555-555555555555", RunNumber: "3", ExecutionID: "66666666-6666-4666-8666-666666666666", Stream: "stdout", InstanceID: "77777777-7777-4777-8777-777777777777", RecoveryEpoch: "1", AuthorizationVersion: "2", AuthorizationExpiresAt: now.Add(time.Minute), AsOf: now, Revision: "8", State: "open", ByteLength: 12, LastSequence: 3}
	r := &testReader{data: make(map[string][]byte)}
	for i, data := range []string{"abcd", "efgh", "ijkl"} {
		seq := int64(i + 1)
		key := fmt.Sprintf("namespaces/%s/jobs/%s/executions/%s/logs/stdout/%08d.chunk", m.NamespaceName, m.JobID, m.ExecutionID, seq)
		sum := sha256.Sum256([]byte(data))
		m.Chunks = append(m.Chunks, Chunk{Sequence: seq, ByteOffset: int64(i * 4), ByteLength: 4, StoreName: "logs", StoreVersion: "1", ObjectKey: key, Checksum: "sha256:" + hex.EncodeToString(sum[:]), CapturedAt: now.Add(-time.Minute)})
		r.data[key] = []byte(data)
	}
	s := &testSource{manifest: m}
	b, err := NewBroker(map[string]ManifestSource{scope.DeploymentID: s}, []Mapping{{DeploymentID: scope.DeploymentID, TargetGenerationID: m.TargetGenerationID, StoreName: "logs", StoreVersion: "1", Root: "/approved"}}, r, []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	b.now = func() time.Time { return now }
	a := monitoring.Actor{Account: api.Account{ID: "synthetic-account"}, DirectoryID: "88888888-8888-4888-8888-888888888888"}
	return b, s, r, a, Request{Scope: scope, JobID: m.JobID, Stream: "stdout", LimitBytes: 5}
}

func decoded(t *testing.T, r api.LogRange) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(r.BytesBase64)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTailAndContinuationPreserveOriginalByteOffsets(t *testing.T) {
	b, s, _, a, q := brokerFixture(t)
	r, err := b.Read(t.Context(), a, q)
	if err != nil {
		t.Fatal(err)
	}
	if decoded(t, r) != "hijkl" || r.StartOffset != "7" || r.EndOffset != "12" || r.RunNumber != "3" || s.calls != 2 || r.NextCursor == "" {
		t.Fatalf("tail %+v calls%d", r, s.calls)
	}
	q.Cursor = r.NextCursor
	r, err = b.Read(t.Context(), a, q)
	if err != nil {
		t.Fatal(err)
	}
	if r.StartOffset != "12" || r.EndOffset != "12" || decoded(t, r) != "" {
		t.Fatalf("duplicate continuation %+v", r)
	}
	// Continuation is a location only: each replay performs both source reads.
	if s.calls != 4 {
		t.Fatal("continuation bypassed authority")
	}
}

func TestCursorBindingAndFinalRevocation(t *testing.T) {
	for _, mutation := range []string{"account", "directory", "namespace", "stream", "run", "signature", "expiry", "grant", "epoch", "execution"} {
		t.Run(mutation, func(t *testing.T) {
			b, s, _, a, q := brokerFixture(t)
			r, err := b.Read(t.Context(), a, q)
			if err != nil {
				t.Fatal(err)
			}
			q.Cursor = r.NextCursor
			switch mutation {
			case "account":
				a.Account.ID = "another"
			case "directory":
				a.DirectoryID = "99999999-9999-4999-8999-999999999999"
			case "namespace":
				q.Scope.NamespaceID = "99999999-9999-4999-8999-999999999999"
			case "stream":
				q.Stream = "stderr"
			case "run":
				q.RunNumber = "4"
			case "signature":
				q.Cursor = "A" + q.Cursor[1:]
			case "expiry":
				now := b.now()
				b.now = func() time.Time { return now.Add(16 * time.Minute) }
			case "grant":
				s.manifest.AuthorizationVersion = "3"
			case "epoch":
				s.manifest.RecoveryEpoch = "2"
			case "execution":
				s.manifest.ExecutionID = "99999999-9999-4999-8999-999999999999"
			}
			if _, err = b.Read(t.Context(), a, q); err == nil {
				t.Fatal("cursor crossed binding")
			}
		})
	}
	b, s, _, a, q := brokerFixture(t)
	s.after = func(s *testSource) {
		if s.calls == 2 {
			s.err = monitoring.ErrForbidden
		}
	}
	r, err := b.Read(t.Context(), a, q)
	if !errors.Is(err, monitoring.ErrForbidden) || r.BytesBase64 != "" || r.ExecutionID != "" {
		t.Fatalf("final revocation leaked response: %+v %v", r, err)
	}
}

func TestManifestRejectsNamespacePoisoningAndGaps(t *testing.T) {
	for _, mutation := range []string{"prefix", "checksum", "size", "order", "gap", "expired", "future", "identity", "count"} {
		t.Run(mutation, func(t *testing.T) {
			b, s, r, a, q := brokerFixture(t)
			q.LimitBytes = 100
			switch mutation {
			case "prefix":
				s.manifest.Chunks[0].ObjectKey = strings.Replace(s.manifest.Chunks[0].ObjectKey, "/research/", "/other/", 1)
			case "checksum":
				s.manifest.Chunks[0].Checksum = "untrusted"
			case "size":
				s.manifest.Chunks[0].ByteLength = MaxChunkBytes + 1
			case "order":
				s.manifest.Chunks[1].Sequence = 1
			case "gap":
				s.manifest.Chunks[1].ByteOffset = 5
			case "expired":
				s.manifest.AuthorizationExpiresAt = b.now().Add(-time.Second)
			case "future":
				s.manifest.AsOf = b.now().Add(time.Hour)
			case "identity":
				s.manifest.InstanceID = "unverified"
			case "count":
				s.manifest.ByteLength = -1
			}
			if _, err := b.Read(t.Context(), a, q); err == nil || r.calls != 0 {
				t.Fatalf("invalid source read: %v %d", err, r.calls)
			}
		})
	}
}

func TestReadFailureNeverSkipsBytes(t *testing.T) {
	for _, state := range []string{"chunk_missing", "chunk_corrupt", "mapping_inaccessible", "read_timeout", "reader_busy"} {
		t.Run(state, func(t *testing.T) {
			b, s, reader, a, q := brokerFixture(t)
			reader.state = state
			r, err := b.Read(t.Context(), a, q)
			if err != nil {
				t.Fatal(err)
			}
			if r.State != state || r.BytesBase64 != "" || r.StartOffset != r.EndOffset || r.NextCursor == "" || s.calls != 2 {
				t.Fatalf("read failure advanced or lacked finalcheck: %+v", r)
			}
		})
	}
}

func TestMappingGenerationCannotCrossRoots(t *testing.T) {
	b, s, r, a, q := brokerFixture(t)
	s.manifest.TargetGenerationID = "99999999-9999-4999-8999-999999999999"
	out, err := b.Read(t.Context(), a, q)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "mapping_inaccessible" || r.calls != 0 {
		t.Fatal("generation mapping crossed")
	}
}

func TestEmptyCompleteChunkStillVerified(t *testing.T) {
	b, s, r, a, q := brokerFixture(t)
	s.manifest.ByteLength = 0
	s.manifest.LastSequence = 1
	s.manifest.State = "complete"
	s.manifest.Chunks = s.manifest.Chunks[:1]
	c := &s.manifest.Chunks[0]
	c.ByteLength = 0
	sum := sha256.Sum256(nil)
	c.Checksum = "sha256:" + hex.EncodeToString(sum[:])
	r.data[c.ObjectKey] = nil
	out, err := b.Read(t.Context(), a, q)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "complete" || out.EndOffset != "0" || r.calls != 1 {
		t.Fatal("empty complete not verified")
	}
	r.state = "chunk_missing"
	out, err = b.Read(t.Context(), a, q)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "chunk_missing" {
		t.Fatal("missing empty file reported complete")
	}
}

func TestZeroLengthChunkBeforeTailIsRejectedWithoutReading(t *testing.T) {
	b, s, reader, actor, request := brokerFixture(t)
	s.manifest.ByteLength = 8
	s.manifest.Chunks[0].ByteLength = 0
	sum := sha256.Sum256(nil)
	s.manifest.Chunks[0].Checksum = "sha256:" + hex.EncodeToString(sum[:])
	reader.data[s.manifest.Chunks[0].ObjectKey] = nil
	s.manifest.Chunks[1].ByteOffset = 0
	s.manifest.Chunks[2].ByteOffset = 4
	// The selected tail starts at byte 3. Previously the empty chunk at byte 0
	// passed validation and caused a negative slice length while assembling it.
	result, err := b.Read(t.Context(), actor, request)
	if !errors.Is(err, monitoring.ErrSource) || reader.calls != 0 || result.BytesBase64 != "" {
		t.Fatalf("invalid zero-length chunk was read: %+v %v calls=%d", result, err, reader.calls)
	}
}

func TestTerminalZeroLengthChunkAtContinuationOffsetIsVerified(t *testing.T) {
	b, s, reader, actor, request := brokerFixture(t)
	s.manifest.State = "complete"
	s.manifest.LastSequence = 4
	terminal := s.manifest.Chunks[2]
	terminal.Sequence = 4
	terminal.ByteOffset = s.manifest.ByteLength
	terminal.ByteLength = 0
	terminal.ObjectKey = strings.Replace(terminal.ObjectKey, "00000003.chunk", "00000004.chunk", 1)
	sum := sha256.Sum256(nil)
	terminal.Checksum = "sha256:" + hex.EncodeToString(sum[:])
	s.manifest.Chunks = append(s.manifest.Chunks, terminal)
	reader.data[terminal.ObjectKey] = nil
	result, err := b.Read(t.Context(), actor, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Cursor = result.NextCursor
	before := reader.calls
	result, err = b.Read(t.Context(), actor, request)
	if err != nil || result.State != "complete" || result.StartOffset != "12" || result.EndOffset != "12" || reader.calls != before+1 {
		t.Fatalf("terminal empty chunk was not verified: %+v %v", result, err)
	}
	reader.state = "chunk_missing"
	result, err = b.Read(t.Context(), actor, request)
	if err != nil || result.State != "chunk_missing" || result.StartOffset != result.EndOffset || result.BytesBase64 != "" {
		t.Fatalf("missing terminal empty chunk reported complete: %+v %v", result, err)
	}
}
