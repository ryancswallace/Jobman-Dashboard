//go:build integration

package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/httpapi"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type labInstallPreferenceStore struct {
	value  api.Preferences
	writes int
}

func (s *labInstallPreferenceStore) Preferences(context.Context, string) (api.Preferences, error) {
	return s.value, nil
}

func (s *labInstallPreferenceStore) UpdatePreferences(_ context.Context, _, revision string, value api.Preferences) (api.Preferences, error) {
	if revision != s.value.Revision {
		return api.Preferences{}, &api.Error{Code: "revision_conflict", Message: "Reload settings."}
	}
	number, _ := strconv.Atoi(revision)
	value.Revision = strconv.Itoa(number + 1)
	s.value = value
	s.writes++
	return value, nil
}

// Exercise the harness's actual request body against the product HTTP handler.
// The failed live attempt omitted the body revision and was rejected before
// reaching persistence, despite sending the corresponding If-Match header.
func TestLabInstallationPreferenceRequestUsesActualHTTPContract(t *testing.T) {
	store := &labInstallPreferenceStore{value: api.DefaultPreferences()}
	handler := (&httpapi.Server{Preferences: store, Auth: httpapi.AuthFunc(func(*http.Request) (monitoring.Actor, error) {
		return monitoring.Actor{Account: api.Account{ID: "71000000-0000-4000-8000-000000000001"}}, nil
	})}).Handler()
	send := func(raw []byte, revision string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/v1/preferences", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("If-Match", revision)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	old := []byte(`{"timezone":"America/New_York","appearance":"dark","refreshSeconds":10}`)
	if w := send(old, store.value.Revision); w.Code != http.StatusBadRequest || store.writes != 0 {
		t.Fatal("Missing body revision must fail before persistence")
	}
	original := store.value
	body, err := auth.LabInstallationPreferenceBody(original)
	if err != nil {
		t.Fatal(err)
	}
	w := send(body, original.Revision)
	var updated api.Preferences
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &updated) != nil || store.writes != 1 || updated != store.value || updated.Revision != "2" || updated.Timezone != "America/New_York" || updated.Appearance != "dark" || updated.RefreshSeconds != 10 || w.Header().Get("ETag") != `"2"` {
		t.Fatal("Reviewed installation settings must satisfy the product persistence contract")
	}
	if w := send(body, original.Revision); w.Code != http.StatusConflict || store.writes != 1 {
		t.Fatal("The accepted request must not bypass optimistic concurrency on replay")
	}
}
