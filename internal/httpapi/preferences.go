package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

type PreferenceStore interface {
	Preferences(context.Context, string) (api.Preferences, error)
	UpdatePreferences(context.Context, string, string, api.Preferences) (api.Preferences, error)
}

func (s *Server) updatePreferences(w http.ResponseWriter, r *http.Request) {
	if s.Preferences == nil {
		writeError(w, r, &api.Error{Code: "source_unavailable", Message: "Settings are unavailable on this server. Ask your administrator to check its configuration."})
		return
	}
	invalid := func() {
		writeError(w, r, &api.Error{Code: "invalid_request", Message: "These settings could not be saved. Reload settings, check your choices, and try again."})
	}
	typeName, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typeName != "application/json" || r.URL.RawQuery != "" || len(r.Header.Values("If-Match")) != 1 {
		invalid()
		return
	}
	revision := r.Header.Get("If-Match")
	if strings.HasPrefix(revision, "\"") && strings.HasSuffix(revision, "\"") {
		revision = strings.TrimSuffix(strings.TrimPrefix(revision, "\""), "\"")
	}
	number, err := strconv.ParseInt(revision, 10, 64)
	if err != nil || number < 1 || strconv.FormatInt(number, 10) != revision {
		invalid()
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	var settings api.Preferences
	if err := decoder.Decode(&settings); err != nil || settings.Revision != revision {
		invalid()
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		invalid()
		return
	}
	updated, err := s.Preferences.UpdatePreferences(r.Context(), actor(r).Account.ID, revision, settings)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("ETag", "\""+updated.Revision+"\"")
	writeJSON(w, http.StatusOK, updated)
}
