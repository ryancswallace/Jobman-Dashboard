package monitoring

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"slices"
	"sync"
	"time"
)

// CursorStore implementations must atomically compare versions when advancing.
// Load returns detached state; network I/O must never hold a database lock.
type CursorStore interface {
	Create(context.Context, []byte, time.Time) (string, error)
	Load(context.Context, string) ([]byte, uint64, error)
	Advance(context.Context, string, uint64, []byte) error
}

type memoryRecord struct {
	data     []byte
	version  uint64
	expires  time.Time
	identity CursorIdentity
	serial   uint64
}

// MemoryCursors is for explicit development fixtures and tests only. Production
// uses PostgreSQL so cursors survive process restart and replicas share state.
type MemoryCursors struct {
	mu      sync.Mutex
	records map[string]memoryRecord
	Now     func() time.Time
	Max     int
	serial  uint64
}

type CursorIdentity struct {
	AccountID string `json:"accountId"`
	QueryHash string `json:"queryHash"`
	Initial   bool   `json:"initial,omitempty"`
}

func DecodeCursorIdentity(data []byte) (CursorIdentity, error) {
	var id CursorIdentity
	if len(data) > 4<<20 || json.Unmarshal(data, &id) != nil || id.AccountID == "" || id.QueryHash == "" {
		return id, failure("invalid_request", "This list could not be loaded. Refresh the list and try again.")
	}
	return id, nil
}

var ErrCursorQuota = failure("rate_limited", "Too many lists are open. Wait for earlier browsing sessions to expire, then try again.")

func NewMemoryCursors() *MemoryCursors {
	return &MemoryCursors{records: make(map[string]memoryRecord), Now: time.Now, Max: 1000}
}

func (s *MemoryCursors) Create(_ context.Context, data []byte, expires time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	identity, err := DecodeCursorIdentity(data)
	if err != nil {
		return "", err
	}
	for k, v := range s.records {
		if !s.Now().Before(v.expires) {
			delete(s.records, k)
		}
	}
	// Polling replaces only unused initial continuations for the same query.
	// Consumed pages remain replayable for Back/retry until their original TTL.
	if identity.Initial {
		keys := []string{}
		for k, v := range s.records {
			if v.identity.AccountID == identity.AccountID && v.identity.QueryHash == identity.QueryHash && v.identity.Initial && v.version == 1 {
				keys = append(keys, k)
			}
		}
		slices.SortFunc(keys, func(a, b string) int {
			if s.records[a].serial < s.records[b].serial {
				return -1
			}
			return 1
		})
		for len(keys) >= 3 {
			delete(s.records, keys[0])
			keys = keys[1:]
		}
	}
	if len(s.records) >= s.Max {
		return "", ErrCursorQuota
	}
	if !s.withinBudget(identity.AccountID, "", len(data)) {
		return "", ErrCursorQuota
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(b[:])
	s.serial++
	s.records[id] = memoryRecord{data: append([]byte(nil), data...), version: 1, expires: expires, identity: identity, serial: s.serial}
	return id, nil
}

func (s *MemoryCursors) Load(_ context.Context, id string) ([]byte, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.records[id]
	if !ok || !s.Now().Before(v.expires) {
		delete(s.records, id)
		return nil, 0, ErrCursor
	}
	return append([]byte(nil), v.data...), v.version, nil
}

func (s *MemoryCursors) Advance(_ context.Context, id string, version uint64, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.records[id]
	if !ok || v.version != version || !s.Now().Before(v.expires) {
		return ErrCursor
	}
	if len(data) == 0 {
		delete(s.records, id)
		return nil
	}
	identity, err := DecodeCursorIdentity(data)
	if err != nil {
		return err
	}
	if identity.AccountID != v.identity.AccountID || identity.QueryHash != v.identity.QueryHash {
		return ErrCursor
	}
	if !s.withinBudget(identity.AccountID, id, len(data)) {
		return ErrCursorQuota
	}
	v.data = append([]byte(nil), data...)
	v.version++
	s.records[id] = v
	return nil
}

func (s *MemoryCursors) withinBudget(account, except string, newBytes int) bool {
	total, own, count := newBytes, newBytes, 1
	for k, v := range s.records {
		if k == except {
			continue
		}
		total += len(v.data)
		if v.identity.AccountID == account {
			own += len(v.data)
			count++
		}
	}
	return total <= 128<<20 && own <= 16<<20 && count <= 100
}

func encodeState(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err == nil && len(b) > 4<<20 {
		return nil, failure("rate_limited", "This list is too large to load. Narrow your selection and try again.")
	}
	return b, err
}
