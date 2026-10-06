package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

type InboxService interface {
	List(context.Context, monitoring.Actor, notifications.InboxQuery) (notifications.InboxPage, error)
	Get(context.Context, monitoring.Actor, string) (notifications.InboxItem, error)
	SetRead(context.Context, monitoring.Actor, string, bool) (notifications.InboxItem, error)
}

func (s *Server) registerInboxRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/inbox", s.inbox)
	mux.HandleFunc("GET /api/v1/inbox/{inbox}", s.inboxItem)
	mux.HandleFunc("PATCH /api/v1/inbox/{inbox}", s.updateInbox)
}
func writeInboxError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, notifications.ErrInvalid) {
		err = &api.Error{Code: "invalid_request", Message: "The notification request is invalid. Refresh your inbox and try again."}
	}
	writeError(w, r, err)
}
func writeInbox(w http.ResponseWriter, r *http.Request, value any) {
	valid := false
	switch v := value.(type) {
	case notifications.InboxPage:
		valid = v.Validate() == nil
	case notifications.InboxItem:
		valid = v.Validate() == nil
	}
	data, err := json.Marshal(value)
	if !valid || err != nil || len(data) > 2<<20 {
		writeInboxError(w, r, monitoring.ErrSource)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
func (s *Server) inbox(w http.ResponseWriter, r *http.Request) {
	values, err := groupQuery(r, "scope", "cursor", "limit", "unread")
	if err != nil || !noRuleBody(r) {
		writeInboxError(w, r, notifications.ErrInvalid)
		return
	}
	limit, err := groupLimit(values, "limit", 20, notifications.MaximumInboxPage)
	if err != nil || values.Has("limit") && strconv.Itoa(limit) != values.Get("limit") {
		writeInboxError(w, r, notifications.ErrInvalid)
		return
	}
	q := notifications.InboxQuery{Limit: limit, Cursor: values.Get("cursor")}
	if values.Has("unread") {
		if values.Get("unread") != "true" && values.Get("unread") != "false" {
			writeInboxError(w, r, notifications.ErrInvalid)
			return
		}
		q.Unread = values.Get("unread") == "true"
	}
	if values.Has("scope") {
		entries, err := ruleArray(json.RawMessage(values.Get("scope")), notifications.MaximumNamespaces)
		if err != nil {
			writeInboxError(w, r, notifications.ErrInvalid)
			return
		}
		q.Scopes = make([]notifications.NamespaceRef, len(entries))
		for i, entry := range entries {
			if _, err = ruleObject(entry, "deploymentId", "namespaceId"); err != nil || json.Unmarshal(entry, &q.Scopes[i]) != nil {
				writeInboxError(w, r, notifications.ErrInvalid)
				return
			}
		}
	}
	q, err = q.Canonical()
	if err != nil {
		writeInboxError(w, r, err)
		return
	}
	if s.Inbox == nil {
		writeInboxError(w, r, monitoring.ErrSource)
		return
	}
	page, err := s.Inbox.List(r.Context(), actor(r), q)
	if err != nil {
		writeInboxError(w, r, err)
		return
	}
	writeInbox(w, r, page)
}
func inboxSelection(r *http.Request) (string, error) {
	id := r.PathValue("inbox")
	if !ruleUUID(id) {
		return "", notifications.ErrInvalid
	}
	if _, err := groupQuery(r); err != nil {
		return "", notifications.ErrInvalid
	}
	return id, nil
}
func (s *Server) inboxItem(w http.ResponseWriter, r *http.Request) {
	id, err := inboxSelection(r)
	if err != nil || !noRuleBody(r) {
		writeInboxError(w, r, notifications.ErrInvalid)
		return
	}
	if s.Inbox == nil {
		writeInboxError(w, r, monitoring.ErrSource)
		return
	}
	result, err := s.Inbox.Get(r.Context(), actor(r), id)
	if err != nil {
		writeInboxError(w, r, err)
		return
	}
	if result.ID != id {
		writeInboxError(w, r, monitoring.ErrSource)
		return
	}
	writeInbox(w, r, result)
}
func (s *Server) updateInbox(w http.ResponseWriter, r *http.Request) {
	id, err := inboxSelection(r)
	if err != nil {
		writeInboxError(w, r, err)
		return
	}
	data, err := ruleBody(w, r, 1024)
	if err != nil {
		writeInboxError(w, r, err)
		return
	}
	fields, err := ruleObject(data, "read")
	var read bool
	if err != nil || json.Unmarshal(fields["read"], &read) != nil {
		writeInboxError(w, r, notifications.ErrInvalid)
		return
	}
	if s.Inbox == nil {
		writeInboxError(w, r, monitoring.ErrSource)
		return
	}
	result, err := s.Inbox.SetRead(r.Context(), actor(r), id, read)
	if err != nil {
		writeInboxError(w, r, err)
		return
	}
	if result.ID != id {
		writeInboxError(w, r, monitoring.ErrSource)
		return
	}
	writeInbox(w, r, result)
}
