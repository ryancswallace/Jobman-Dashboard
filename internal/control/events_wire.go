package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/events"
)

type eventResponse struct {
	APIVersion                  string      `json:"apiVersion"`
	Kind                        string      `json:"kind"`
	ControlInstanceID           string      `json:"controlInstanceId"`
	RecoveryEpoch               string      `json:"recoveryEpoch"`
	AsOf                        time.Time   `json:"asOf"`
	HeadCursor                  string      `json:"headCursor"`
	OldestCursor                string      `json:"oldestCursor"`
	RetentionSeconds            string      `json:"retentionSeconds"`
	BacklogCount                string      `json:"backlogCount"`
	OldestUnpublishedRecordedAt *time.Time  `json:"oldestUnpublishedRecordedAt,omitempty"`
	Items                       []eventItem `json:"items,omitempty"`
	NextCursor                  string      `json:"nextCursor,omitempty"`
	HasMore                     *bool       `json:"hasMore,omitempty"`
}

type eventItem struct {
	EventID             string     `json:"eventId"`
	Position            string     `json:"position"`
	NamespaceID         string     `json:"namespaceId"`
	JobID               string     `json:"jobId"`
	RunID               string     `json:"runId,omitempty"`
	RunNumber           string     `json:"runNumber,omitempty"`
	OwnerPrincipalID    string     `json:"ownerPrincipalId"`
	OldPhase            string     `json:"oldPhase"`
	NewPhase            string     `json:"newPhase"`
	Outcome             string     `json:"outcome"`
	JobRevision         string     `json:"jobRevision"`
	ObservedCompletedAt *time.Time `json:"observedCompletedAt,omitempty"`
	RecordedAt          time.Time  `json:"recordedAt"`
	Imported            *bool      `json:"imported"`
	Reconciliation      *bool      `json:"reconciliation"`
}

func (s *EventSource) get(ctx context.Context, path string, query url.Values, dest any, delegated bool) (resultErr error) {
	start := time.Now()
	defer func() {
		s.client.config.Observer.Observe("source", "events", s.client.config.DeploymentID, "service", observationOutcome(resultErr), time.Since(start))
	}()
	u := *s.client.base
	u.Path, u.RawQuery = path, query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return events.ErrUnavailable
	}
	request.Header.Set("Accept", "application/json")
	if delegated {
		value, err := s.client.config.Signer.AuthorizeEvents(s.client.config.NamespaceIDs)
		if err != nil {
			return events.ErrAuthority
		}
		request.Header.Set("Authorization", value)
	}
	response, err := s.client.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return events.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return events.ErrAuthority
	}
	if response.StatusCode != http.StatusOK {
		if response.StatusCode != http.StatusConflict && response.StatusCode != http.StatusBadRequest {
			return events.ErrUnavailable
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 4097))
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err != nil || len(data) > 4096 || decodeEventJSON(data, &body, true) != nil {
			return events.ErrUnavailable
		}
		if path == "/v1/monitoring-events" && response.StatusCode == http.StatusBadRequest && body.Error.Code == "invalid_request" {
			return &events.RecoveryError{Reason: events.CursorInvalid}
		}
		if response.StatusCode == http.StatusConflict {
			for _, reason := range []events.RecoveryReason{events.CursorExpired, events.SourceChanged, events.ScopeChanged} {
				if body.Error.Code == string(reason) {
					return &events.RecoveryError{Reason: reason}
				}
			}
		}
		return events.ErrUnavailable
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || response.ContentLength > 2<<20 {
		return events.ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 || decodeEventJSON(data, dest, delegated) != nil {
		return events.ErrInvalid
	}
	return nil
}

// Reject duplicate members and excessive nesting before struct decoding can
// collapse ambiguous source data. Exact feeds also reject unknown/case aliases.
func decodeEventJSON(data []byte, dest any, exact bool) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	nodes := 0
	if err := scanEventJSON(decoder, 0, &nodes); err != nil {
		return events.ErrInvalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return events.ErrInvalid
	}
	if exact {
		var value any
		if json.Unmarshal(data, &value) != nil || !exactEventFields(value, reflect.TypeOf(dest).Elem()) {
			return events.ErrInvalid
		}
	}
	if json.Unmarshal(data, dest) != nil {
		return events.ErrInvalid
	}
	return nil
}

func scanEventJSON(decoder *json.Decoder, depth int, nodes *int) error {
	*nodes++
	if depth > 16 || *nodes > 10000 {
		return events.ErrInvalid
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	if delim != '{' && delim != '[' {
		return events.ErrInvalid
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delim == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return events.ErrInvalid
			}
			seen[name] = true
		}
		if err := scanEventJSON(decoder, depth+1, nodes); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || delim == '{' && end != json.Delim('}') || delim == '[' && end != json.Delim(']') {
		return events.ErrInvalid
	}
	return nil
}

func exactEventFields(value any, schema reflect.Type) bool {
	if reflect.PointerTo(schema).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return true
	}
	if schema.Kind() == reflect.Pointer {
		return exactEventFields(value, schema.Elem())
	}
	switch schema.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return value == nil
		}
		fields := make(map[string]reflect.Type, schema.NumField())
		for i := range schema.NumField() {
			field := schema.Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			fields[name] = field.Type
		}
		for name, child := range object {
			field, exists := fields[name]
			if !exists || !exactEventFields(child, field) {
				return false
			}
		}
	case reflect.Slice:
		if values, ok := value.([]any); ok {
			for _, child := range values {
				if !exactEventFields(child, schema.Elem()) {
					return false
				}
			}
		}
	}
	return true
}
