package logs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

// Manifest queries may select a run or the current run. TailBytes and Offset
// are exclusive. The source seeks directly; callers never walk an entire log.
type ManifestQuery struct {
	Scope                    api.Scope
	JobID, RunNumber, Stream string
	TailBytes                int
	Offset                   *int64
	AfterSequence            *int64
	Limit                    int
}

type Chunk struct {
	Sequence, ByteOffset, ByteLength             int64
	StoreName, StoreVersion, ObjectKey, Checksum string
	CapturedAt                                   time.Time
}

type Manifest struct {
	Scope                                           api.Scope
	NamespaceName, JobID, TargetGenerationID        string
	RunID, RunNumber, ExecutionID, Stream           string
	InstanceID, RecoveryEpoch, AuthorizationVersion string
	AuthorizationExpiresAt, AsOf                    time.Time
	Revision                                        string
	State                                           string
	Truncated                                       bool
	ByteLength, LastSequence                        int64
	Chunks                                          []Chunk
}

// ManifestSource must check current source authorization on every call, never
// merely authenticate the service or use a prior bootstrap as permission.
type ManifestSource interface {
	Manifest(context.Context, monitoring.Actor, ManifestQuery) (Manifest, error)
}

type Mapping struct {
	DeploymentID       string `json:"deploymentId"`
	TargetGenerationID string `json:"targetGenerationId,omitempty"`
	StoreName          string `json:"storeName"`
	StoreVersion       string `json:"storeVersion"`
	Root               string `json:"root"`
}
type mappingKey struct{ deployment, generation, store, version string }

type Broker struct {
	sources map[string]ManifestSource
	chunks  ChunkReader
	key     []byte
	now     func() time.Time
	gate    chan struct{}
}

// ChunkReader implementations receive source-authorized identity, never a
// caller-selected filesystem path. A remote reader sends only the immutable
// subject/sequence to a storage-local broker which reauthorizes it independently.
type ChunkReader interface {
	ReadChunk(context.Context, monitoring.Actor, Manifest, Chunk) (FileResult, error)
}

type LocalChunks struct {
	roots  map[mappingKey]string
	reader Reader
}

func NewLocalChunks(mappings []Mapping, reader Reader) (*LocalChunks, error) {
	if len(mappings) == 0 || len(mappings) > 1024 || reader == nil {
		return nil, errors.New("incomplete log root configuration")
	}
	l := &LocalChunks{roots: make(map[mappingKey]string), reader: reader}
	for _, m := range mappings {
		k := mappingKey{m.DeploymentID, m.TargetGenerationID, m.StoreName, m.StoreVersion}
		if !uuid(m.DeploymentID) || (m.TargetGenerationID != "" && !uuid(m.TargetGenerationID)) || !name(m.StoreName) || !positive(m.StoreVersion) || l.roots[k] != "" || !validFileRequest(FileRequest{Root: m.Root, ObjectKey: "validation", Checksum: "sha256:" + strings.Repeat("0", 64)}) {
			return nil, errors.New("invalid or duplicate log root mapping")
		}
		l.roots[k] = m.Root
	}
	return l, nil
}

func (l *LocalChunks) ReadChunk(ctx context.Context, _ monitoring.Actor, m Manifest, c Chunk) (FileResult, error) {
	root := l.roots[mappingKey{m.Scope.DeploymentID, m.TargetGenerationID, c.StoreName, c.StoreVersion}]
	if root == "" {
		root = l.roots[mappingKey{m.Scope.DeploymentID, "", c.StoreName, c.StoreVersion}]
	}
	if root == "" {
		return FileResult{State: "mapping_inaccessible"}, nil
	}
	return l.reader.Read(ctx, FileRequest{Root: root, ObjectKey: c.ObjectKey, ByteLength: c.ByteLength, Checksum: c.Checksum}), nil
}

func NewBroker(sources map[string]ManifestSource, mappings []Mapping, reader Reader, cursorKey []byte) (*Broker, error) {
	if len(sources) == 0 || len(sources) > 32 || len(mappings) == 0 || len(mappings) > 1024 || reader == nil || len(cursorKey) != 32 {
		return nil, errors.New("incomplete bounded log broker configuration")
	}
	l, err := NewLocalChunks(mappings, reader)
	if err != nil {
		return nil, err
	}
	return NewWithChunks(sources, l, cursorKey)
}

func NewWithChunks(sources map[string]ManifestSource, chunks ChunkReader, cursorKey []byte) (*Broker, error) {
	if len(sources) == 0 || len(sources) > 32 || chunks == nil || len(cursorKey) != 32 {
		return nil, errors.New("incomplete log service configuration")
	}
	b := &Broker{sources: make(map[string]ManifestSource), chunks: chunks, key: append([]byte(nil), cursorKey...), now: time.Now, gate: make(chan struct{}, 32)}
	for id, source := range sources {
		if !uuid(id) || source == nil {
			return nil, errors.New("invalid log source")
		}
		b.sources[id] = source
	}
	return b, nil
}

type Request struct {
	Scope      api.Scope `json:"scope"`
	JobID      string    `json:"jobId"`
	RunNumber  string    `json:"runNumber,omitempty"`
	Stream     string    `json:"stream"`
	Cursor     string    `json:"cursor,omitempty"`
	LimitBytes int       `json:"limitBytes"`
}

type position struct {
	AccountID, DirectoryID, DeploymentID, NamespaceID, JobID string
	RunID, RunNumber, ExecutionID, Stream                    string
	InstanceID, RecoveryEpoch, AuthorizationVersion          string
	Offset                                                   int64
	ExpiresAt                                                int64
}

func (b *Broker) Read(ctx context.Context, a monitoring.Actor, r Request) (api.LogRange, error) {
	var result api.LogRange
	select {
	case b.gate <- struct{}{}:
		defer func() { <-b.gate }()
	default:
		return result, &api.Error{Code: "rate_limited", Message: "Log readers are busy. Retry shortly."}
	}
	if !uuid(r.Scope.DeploymentID) || !uuid(r.Scope.NamespaceID) || !uuid(r.JobID) || a.Account.ID == "" || !uuid(a.DirectoryID) || (r.Stream != "stdout" && r.Stream != "stderr") || (r.RunNumber != "" && !positive(r.RunNumber)) || r.LimitBytes < 1 || r.LimitBytes > MaxChunkBytes || len(r.Cursor) > 4096 {
		return result, &api.Error{Code: "invalid_request", Message: "Choose a valid job, run, stream and bounded log range."}
	}
	source := b.sources[r.Scope.DeploymentID]
	if source == nil {
		return result, monitoring.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	q := ManifestQuery{Scope: r.Scope, JobID: r.JobID, RunNumber: r.RunNumber, Stream: r.Stream, TailBytes: r.LimitBytes, Limit: 100}
	var cursor position
	if r.Cursor != "" {
		var err error
		cursor, err = b.decode(r.Cursor)
		if err != nil || cursor.AccountID != a.Account.ID || cursor.DirectoryID != a.DirectoryID || cursor.DeploymentID != r.Scope.DeploymentID || cursor.NamespaceID != r.Scope.NamespaceID || cursor.JobID != r.JobID || cursor.Stream != r.Stream || (r.RunNumber != "" && r.RunNumber != cursor.RunNumber) {
			return result, monitoring.ErrCursor
		}
		q.RunNumber = cursor.RunNumber
		q.TailBytes = 0
		q.Offset = &cursor.Offset
	}
	m, err := source.Manifest(ctx, a, q)
	if err != nil {
		return result, err
	}
	if !validManifest(m, q, b.now()) {
		return result, monitoring.ErrSource
	}
	if r.Cursor != "" && (cursor.RunID != m.RunID || cursor.ExecutionID != m.ExecutionID || cursor.InstanceID != m.InstanceID || cursor.RecoveryEpoch != m.RecoveryEpoch || cursor.AuthorizationVersion != m.AuthorizationVersion || cursor.Offset > m.ByteLength) {
		return result, monitoring.ErrCursor
	}
	start := max(int64(0), m.ByteLength-int64(r.LimitBytes))
	if q.Offset != nil {
		start = *q.Offset
	}
	end := start
	result = api.LogRange{ExecutionID: m.ExecutionID, RunID: m.RunID, RunNumber: m.RunNumber, Stream: m.Stream, StartOffset: strconv.FormatInt(start, 10), EndOffset: strconv.FormatInt(start, 10), State: m.State, Truncated: m.Truncated}
	data := make([]byte, 0, r.LimitBytes)
	for _, chunk := range m.Chunks {
		if chunk.ByteOffset+chunk.ByteLength <= end && (chunk.ByteLength > 0 || chunk.ByteOffset < end) {
			continue
		}
		if chunk.ByteOffset > end {
			result.State = "sequence_gap"
			data = nil
			end = start
			break
		}
		read, err := b.chunks.ReadChunk(ctx, a, m, chunk)
		if err != nil {
			return api.LogRange{}, err
		}
		// A freshly published NFS entry can briefly lag its manifest. Retry at
		// most twice and never advance the continuation over missing bytes.
		for attempt := 0; read.State == "chunk_missing" && attempt < 2 && b.now().Sub(chunk.CapturedAt) < 2*time.Second; attempt++ {
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return api.LogRange{}, monitoring.ErrSource
			case <-timer.C:
			}
			read, err = b.chunks.ReadChunk(ctx, a, m, chunk)
			if err != nil {
				return api.LogRange{}, err
			}
		}
		if read.State != "ok" {
			result.State = read.State
			data = nil
			end = start
			break
		}
		sum := sha256.Sum256(read.Bytes)
		if int64(len(read.Bytes)) != chunk.ByteLength || fmt.Sprintf("sha256:%x", sum) != chunk.Checksum {
			result.State = "chunk_corrupt"
			data = nil
			end = start
			break
		}
		from := end - chunk.ByteOffset
		if from < 0 || from > int64(len(read.Bytes)) {
			result.State = "chunk_corrupt"
			data = nil
			end = start
			break
		}
		size := min(int64(r.LimitBytes-len(data)), chunk.ByteLength-from)
		data = append(data, read.Bytes[from:from+size]...)
		end += size
		captured := chunk.CapturedAt
		result.CapturedAt = &captured
		if len(data) == r.LimitBytes {
			break
		}
	}
	if end < m.ByteLength && result.State == m.State {
		if len(data) == 0 {
			result.State = "sequence_gap"
		} else {
			result.State = "more"
		}
	}
	// The final check is another represented-user Control read. An append does
	// not invalidate immutable bytes, but revocation, restore or run replacement
	// makes the entire prepared response inaccessible.
	finalQuery := ManifestQuery{Scope: r.Scope, JobID: r.JobID, RunNumber: m.RunNumber, Stream: r.Stream, Offset: &start, Limit: 1}
	final, err := source.Manifest(ctx, a, finalQuery)
	if err != nil {
		return api.LogRange{}, err
	}
	if !validManifest(final, finalQuery, b.now()) || !sameAuthority(m, final) || m.RunID != final.RunID || m.ExecutionID != final.ExecutionID || final.ByteLength < m.ByteLength || ctx.Err() != nil {
		return api.LogRange{}, monitoring.ErrAuthority
	}
	result.BytesBase64 = base64.StdEncoding.EncodeToString(data)
	result.EndOffset = strconv.FormatInt(end, 10)
	if m.ExecutionID != "" {
		result.NextCursor = b.encode(position{AccountID: a.Account.ID, DirectoryID: a.DirectoryID, DeploymentID: r.Scope.DeploymentID, NamespaceID: r.Scope.NamespaceID, JobID: r.JobID, RunID: m.RunID, RunNumber: m.RunNumber, ExecutionID: m.ExecutionID, Stream: r.Stream, InstanceID: m.InstanceID, RecoveryEpoch: m.RecoveryEpoch, AuthorizationVersion: m.AuthorizationVersion, Offset: end, ExpiresAt: b.now().Add(15 * time.Minute).Unix()})
	}
	return result, nil
}

func sameAuthority(a, b Manifest) bool {
	return a.InstanceID == b.InstanceID && a.RecoveryEpoch == b.RecoveryEpoch && a.AuthorizationVersion == b.AuthorizationVersion && a.Scope == b.Scope && a.JobID == b.JobID && a.TargetGenerationID == b.TargetGenerationID
}

func validManifest(m Manifest, q ManifestQuery, now time.Time) bool {
	if m.Scope != q.Scope || m.JobID != q.JobID || m.Stream != q.Stream || !name(m.NamespaceName) || !uuid(m.InstanceID) || !positive(m.RecoveryEpoch) || !positive(m.AuthorizationVersion) || m.AsOf.IsZero() || m.AsOf.After(now.Add(5*time.Second)) || !now.Before(m.AuthorizationExpiresAt) || m.AuthorizationExpiresAt.After(now.Add(125*time.Second)) || len(m.Chunks) > q.Limit || m.ByteLength < 0 || m.LastSequence < 0 || (!positive(m.Revision) && !(m.State == "not_captured" && m.Revision == "0")) {
		return false
	}
	if m.State != "not_captured" && m.State != "open" && m.State != "complete" {
		return false
	}
	if m.State == "not_captured" && (m.ByteLength != 0 || m.LastSequence != 0 || len(m.Chunks) != 0 || m.Revision != "0") {
		return false
	}
	if m.RunNumber != "" && !positive(m.RunNumber) {
		return false
	}
	if q.RunNumber != "" && q.RunNumber != m.RunNumber {
		return false
	}
	if m.ExecutionID == "" {
		return m.State == "not_captured" && m.ByteLength == 0 && len(m.Chunks) == 0 && (m.RunID == "" || uuid(m.RunID))
	}
	if !uuid(m.RunID) || !positive(m.RunNumber) || !uuid(m.ExecutionID) || !uuid(m.TargetGenerationID) {
		return false
	}
	var last *Chunk
	for i := range m.Chunks {
		c := &m.Chunks[i]
		if c.Sequence < 1 || c.Sequence > m.LastSequence || c.ByteOffset < 0 || c.ByteLength < 0 || c.ByteLength > MaxChunkBytes || c.ByteOffset > m.ByteLength || c.ByteLength > m.ByteLength-c.ByteOffset || !name(c.StoreName) || !positive(c.StoreVersion) || c.CapturedAt.IsZero() || c.CapturedAt.After(now.Add(5*time.Second)) {
			return false
		}
		if c.ByteLength == 0 && (c.Sequence != m.LastSequence || c.ByteOffset != m.ByteLength || m.State != "complete") {
			return false
		}
		expected := fmt.Sprintf("namespaces/%s/jobs/%s/executions/%s/logs/%s/%08d.chunk", m.NamespaceName, m.JobID, m.ExecutionID, m.Stream, c.Sequence)
		if c.ObjectKey != expected || !validFileRequest(FileRequest{Root: "/", ObjectKey: c.ObjectKey, ByteLength: c.ByteLength, Checksum: c.Checksum}) {
			return false
		}
		if last != nil && (c.Sequence != last.Sequence+1 || c.ByteOffset != last.ByteOffset+last.ByteLength) {
			return false
		}
		last = c
	}
	return true
}

func (b *Broker) encode(p position) string {
	raw, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, b.key)
	mac.Write([]byte("jobman-log-cursor/v1\x00"))
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (b *Broker) decode(s string) (position, error) {
	var p position
	parts := strings.Split(s, ".")
	if len(parts) != 2 {
		return p, monitoring.ErrCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(raw) > 2400 {
		return p, monitoring.ErrCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return p, monitoring.ErrCursor
	}
	mac := hmac.New(sha256.New, b.key)
	mac.Write([]byte("jobman-log-cursor/v1\x00"))
	mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) || json.Unmarshal(raw, &p) != nil || p.Offset < 0 || p.ExpiresAt <= b.now().Unix() || p.ExpiresAt > b.now().Add(15*time.Minute).Unix() {
		return p, monitoring.ErrCursor
	}
	return p, nil
}
func positive(s string) bool {
	v, e := strconv.ParseInt(s, 10, 64)
	return e == nil && v > 0 && strconv.FormatInt(v, 10) == s
}
func name(s string) bool {
	if len(s) < 1 || len(s) > 64 || s == "." || s == ".." {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789._-", c) {
			return false
		}
	}
	return true
}
func uuid(s string) bool {
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range s {
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
