package monitoring

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A traversal retains 64 prior pages plus the current and next page selectors.
// It consumes one ordinary quota-accounted cursor row, regardless of its length.
const groupCursorWindow = 66

type groupCursorPoint struct {
	AfterIndex   int    `json:"afterIndex"`
	SourceCursor string `json:"sourceCursor"`
	LastEdge     string `json:"lastEdge,omitempty"`
}

type groupCursorHistory struct {
	CursorIdentity
	Format    int                `json:"format"`
	Expires   time.Time          `json:"expires"`
	Authority string             `json:"authority"`
	First     uint64             `json:"first"`
	Points    []groupCursorPoint `json:"points"`
}

func groupPoint(state groupResourceCursor) groupCursorPoint {
	return groupCursorPoint{state.AfterIndex, state.SourceCursor, state.LastEdge}
}

func validGroupPoint(point groupCursorPoint) bool {
	return point.AfterIndex >= -1 && point.AfterIndex <= 9999 && len(point.SourceCursor) <= 8192 && len(point.LastEdge) <= 512
}

func groupPosition(cursor string) (string, uint64, error) {
	parts := strings.Split(cursor, ".")
	if len(parts) != 3 || parts[0] != "g" || len(parts[1]) != 43 || len(parts[2]) > 6 {
		return "", 0, ErrCursor
	}
	key, err := base64.RawURLEncoding.DecodeString(parts[1])
	position, numberErr := strconv.ParseUint(parts[2], 10, 64)
	if err != nil || len(key) != 32 || base64.RawURLEncoding.EncodeToString(key) != parts[1] || numberErr != nil || position < 1 || position > 100000 || strconv.FormatUint(position, 10) != parts[2] {
		return "", 0, ErrCursor
	}
	return parts[1], position, nil
}

func groupToken(key string, position uint64) string {
	return "g." + key + "." + strconv.FormatUint(position, 10)
}

func (e *Engine) loadGroupHistory(ctx context.Context, key string, expected groupResourceCursor) (groupCursorHistory, uint64, error) {
	data, version, err := e.cursors.Load(ctx, key)
	var history groupCursorHistory
	if err != nil {
		return history, 0, ErrCursor
	}
	if len(data) > 4<<20 || json.Unmarshal(data, &history) != nil || history.Format != 1 || history.AccountID != expected.AccountID || history.QueryHash != expected.QueryHash || history.Authority != expected.Authority || !e.Now().Before(history.Expires) || history.First < 1 || history.First > 100000 || len(history.Points) < 1 || len(history.Points) > groupCursorWindow || uint64(len(history.Points)-1) > 100000-history.First {
		return history, 0, ErrCursor
	}
	for _, point := range history.Points {
		if !validGroupPoint(point) {
			return history, 0, ErrCursor
		}
	}
	return history, version, nil
}

func (e *Engine) groupCursor(ctx context.Context, a Actor, q groupResourceQuery, authority, cursor string) (groupResourceCursor, error) {
	state := groupResourceCursor{CursorIdentity: CursorIdentity{AccountID: a.Account.ID, QueryHash: groupHash(q), Initial: cursor == ""}, Expires: e.Now().Add(15 * time.Minute), Authority: authority, AfterIndex: -1}
	if cursor == "" {
		return state, nil
	}
	if strings.HasPrefix(cursor, "g.") {
		key, position, err := groupPosition(cursor)
		if err != nil {
			return state, err
		}
		history, version, err := e.loadGroupHistory(ctx, key, state)
		if err != nil || position < history.First || position-history.First >= uint64(len(history.Points)) {
			return state, ErrCursor
		}
		if version == 1 && history.Initial {
			// Protect a visited traversal from initial-poll replacement. A
			// concurrent successful visit is harmless; reload its exact state.
			data, encodeErr := encodeState(history)
			if encodeErr != nil {
				return state, encodeErr
			}
			if err := e.cursors.Advance(ctx, key, version, data); err != nil && !errors.Is(err, ErrCursor) {
				return state, err
			}
			history, version, err = e.loadGroupHistory(ctx, key, state)
			if err != nil || position < history.First || position-history.First >= uint64(len(history.Points)) {
				return state, ErrCursor
			}
		}
		point := history.Points[position-history.First]
		state.Initial, state.Expires = false, history.Expires
		state.AfterIndex, state.SourceCursor, state.LastEdge = point.AfterIndex, point.SourceCursor, point.LastEdge
		state.key, state.version, state.position, state.history = key, version, position, &history
		return state, nil
	}
	// Pre-upgrade single-position tokens remain readable until their original
	// expiry. The next continuation starts a bounded traversal without deleting
	// that legacy Back/retry selector or extending its lifetime.
	if !strings.HasPrefix(cursor, "r.") || len(cursor) > 512 {
		return state, ErrCursor
	}
	key := strings.TrimPrefix(cursor, "r.")
	data, version, err := e.cursors.Load(ctx, key)
	if err != nil {
		return state, ErrCursor
	}
	expected := state
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) > 4<<20 || decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF || state.AccountID != expected.AccountID || state.QueryHash != expected.QueryHash || state.Authority != authority || !e.Now().Before(state.Expires) {
		return state, ErrCursor
	}
	if version == 1 && state.Initial {
		if err = e.cursors.Advance(ctx, key, version, data); err != nil && !errors.Is(err, ErrCursor) {
			return state, err
		}
	}
	state.Initial = false
	return state, nil
}

func (e *Engine) saveGroupCursor(ctx context.Context, state groupResourceCursor) (string, error) {
	if !e.Now().Before(state.Expires) {
		return "", ErrCursor
	}
	if !validGroupPoint(groupPoint(state)) {
		return "", errGroupsUnsupported
	}
	if state.history == nil {
		history := groupCursorHistory{CursorIdentity: state.CursorIdentity, Format: 1, Expires: state.Expires, Authority: state.Authority, First: 1, Points: []groupCursorPoint{groupPoint(state)}}
		data, err := encodeState(history)
		if err != nil {
			return "", err
		}
		key, err := e.cursors.Create(ctx, data, history.Expires)
		if err != nil {
			return "", err
		}
		return groupToken(key, 1), nil
	}
	next := state.position + 1
	point := groupPoint(state)
	history := *state.history
	end := history.First + uint64(len(history.Points))
	if next < end {
		// Back/retry must reuse the already captured successor. It never
		// truncates a later branch, refreshes expiry or evicts newer history.
		if next < history.First || history.Points[next-history.First] != point {
			return "", ErrCursor
		}
		return e.replayGroupSuccessor(ctx, state, next, point)
	}
	if next != end || next > 100000 {
		return "", ErrCursor
	}
	history.Initial = false
	history.Points = append(slices.Clone(history.Points), point)
	if len(history.Points) > groupCursorWindow {
		history.Points = slices.Clone(history.Points[1:])
		history.First++
	}
	data, err := encodeState(history)
	if err != nil {
		return "", err
	}
	if err = e.cursors.Advance(ctx, state.key, state.version, data); err == nil {
		return groupToken(state.key, next), nil
	}
	if !errors.Is(err, ErrCursor) {
		return "", err
	}
	// A concurrent reader may have published this exact continuation. One
	// bounded reread accepts only that immutable successor, never a new fork.
	return e.replayGroupSuccessor(ctx, state, next, point)
}

func (e *Engine) replayGroupSuccessor(ctx context.Context, state groupResourceCursor, next uint64, point groupCursorPoint) (string, error) {
	current, _, err := e.loadGroupHistory(ctx, state.key, state)
	if err != nil || !current.Expires.Equal(state.Expires) || state.position < current.First || next < current.First || next-current.First >= uint64(len(current.Points)) || current.Points[next-current.First] != point {
		return "", ErrCursor
	}
	return groupToken(state.key, next), nil
}
