package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

// DeviceService is intentionally separate from worker-only token handoff and
// provider invalidation. No public route can obtain a token or private fence.
type DeviceService interface {
	ReserveRevocation(context.Context, monitoring.Actor, string, int64, notifications.DeviceRevocationInput) (notifications.DeviceRevocationReceipt, error)
	ActivateRevocation(context.Context, monitoring.Actor, string, int64, notifications.DeviceRevocationInput) (notifications.DeviceRevocationReceipt, error)
	RevokeDevice(context.Context, string, string) error
	List(context.Context, monitoring.Actor) ([]notifications.DeviceView, error)
	InspectInstallation(context.Context, monitoring.Actor, string, string) (notifications.InstallationState, error)
	Bind(context.Context, monitoring.Actor, int64, notifications.DeviceRegistration) (notifications.DeviceView, error)
	SwitchBinding(context.Context, monitoring.Actor, int64, notifications.DeviceRegistration, bool) (notifications.DeviceView, error)
	Refresh(context.Context, monitoring.Actor, string, int64, notifications.DeviceRefresh) (notifications.DeviceView, error)
	Settings(context.Context, monitoring.Actor, string, int64, notifications.DeviceSettings) (notifications.DeviceView, error)
	Remove(context.Context, monitoring.Actor, string, int64) error
	Detach(context.Context, monitoring.Actor, string, int64, string) error
}

func (s *Server) registerDeviceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/devices", s.devices)
	mux.HandleFunc("POST /api/v1/devices/{installation}/revocation-reservations", s.reserveDeviceRevocation)
	mux.HandleFunc("POST /api/v1/devices/{installation}/revocation-credentials", s.activateDeviceRevocation)
	mux.Handle("POST /auth/native/device-revocations", newDeviceRevocationHandler(s))
	mux.HandleFunc("POST /api/v1/devices/{installation}/inspect", s.inspectDevice)
	mux.HandleFunc("POST /api/v1/devices/{installation}/bind", s.bindDevice)
	mux.HandleFunc("POST /api/v1/devices/{installation}/switch", s.switchDevice)
	mux.HandleFunc("PUT /api/v1/devices/{installation}/token", s.refreshDevice)
	mux.HandleFunc("PUT /api/v1/devices/{installation}/settings", s.deviceSettings)
	mux.HandleFunc("DELETE /api/v1/devices/{installation}", s.removeDevice)
	mux.HandleFunc("POST /api/v1/devices/{installation}/detach", s.detachDevice)
}
func writeDeviceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, notifications.ErrDeviceInvalid), errors.Is(err, notifications.ErrInvalid):
		err = &api.Error{Code: "invalid_request", Message: "The device settings could not be saved. Refresh the device list, check your choices, and try again."}
	case errors.Is(err, notifications.ErrDeviceNotFound):
		err = monitoring.ErrNotFound
	case errors.Is(err, notifications.ErrDeviceConflict):
		err = &api.Error{Code: "revision_conflict", Message: "The device settings changed. Refresh the device list before trying again."}
	case errors.Is(err, notifications.ErrDeviceCapacity):
		err = &api.Error{Code: "device_capacity", Message: "The server has reached its device record limit. You can still stop iPhone notifications or remove a device. Contact your administrator for help adding devices."}
	case errors.Is(err, notifications.ErrDeviceRateLimited):
		err = &api.Error{Code: "rate_limited", Message: "Too many device changes. Retry later; stopping, muting, detaching, and removing a device remain available."}
	}
	writeError(w, r, err)
}

// A first reservation uses If-None-Match:*. Every subsequent mutation uses
// one positive If-Match decimal string. Proofs/tokens never enter URL parameters.
func deviceSelection(r *http.Request, conditional string) (string, int64, error) {
	id := r.PathValue("installation")
	if !ruleUUID(id) {
		return "", 0, notifications.ErrDeviceInvalid
	}
	if _, err := groupQuery(r); err != nil {
		return "", 0, notifications.ErrDeviceInvalid
	}
	match, none := r.Header.Values("If-Match"), r.Header.Values("If-None-Match")
	if conditional == "inspect" {
		if len(match) != 0 || len(none) != 0 {
			return "", 0, notifications.ErrDeviceInvalid
		}
		return id, 0, nil
	}
	if conditional == "reserve" && len(none) == 1 && none[0] == "*" && len(match) == 0 {
		return id, 0, nil
	}
	if len(match) != 1 || len(none) != 0 {
		return "", 0, notifications.ErrDeviceInvalid
	}
	value := match[0]
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	revision, err := ruleRevision(value)
	return id, revision, err
}
func decodeDeviceFields(w http.ResponseWriter, r *http.Request, limit int64, fields map[string]any) error {
	data, err := ruleBody(w, r, limit)
	if err != nil {
		return notifications.ErrDeviceInvalid
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	object, err := ruleObject(data, names...)
	if err != nil {
		return notifications.ErrDeviceInvalid
	}
	for name, target := range fields {
		if json.Unmarshal(object[name], target) != nil {
			return notifications.ErrDeviceInvalid
		}
	}
	return nil
}
func decodeDeviceRegistration(w http.ResponseWriter, r *http.Request, id, intent string) (notifications.DeviceRegistration, error) {
	result := notifications.DeviceRegistration{InstallationID: id}
	var confirmed bool
	err := decodeDeviceFields(w, r, 16<<10, map[string]any{"installationSecret": &result.InstallationSecret, "revocationId": &result.RevocationID, "revocationCredential": &result.RevocationCredential, "label": &result.Label, "topic": &result.Topic, "environment": &result.Environment, "token": &result.Token, "permission": &result.Permission, "enabled": &result.Enabled, "muted": &result.Muted, intent: &confirmed})
	if err != nil || !confirmed {
		return result, notifications.ErrDeviceInvalid
	}
	// Validate syntax/bounds here. The real service separately checks its immutable
	// operator allowlist; this temporary policy grants no server-side permission.
	syntax, err := notifications.NewDevicePolicy([]notifications.DeviceTopic{{Topic: result.Topic, Environment: result.Environment}})
	if err != nil {
		return result, err
	}
	return result, result.Validate(syntax)
}
func validDeviceTime(t time.Time) bool { return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 }
func validDeviceView(v notifications.DeviceView) bool {
	if !ruleUUID(v.InstallationID) || v.Revision <= 0 || (notifications.DeviceSettings{Label: v.Label}).Validate() != nil || v.State != "bound" || !notifications.DevicePermission(v.Permission) || !validDeviceTime(v.CreatedAt) || !validDeviceTime(v.UpdatedAt) || !validDeviceTime(v.LastSeenAt) || v.UpdatedAt.Before(v.CreatedAt) || v.LastSeenAt.Before(v.CreatedAt) || v.LastSeenAt.After(v.UpdatedAt) {
		return false
	}
	if _, err := notifications.NewDevicePolicy([]notifications.DeviceTopic{{Topic: v.Topic, Environment: v.Environment}}); err != nil {
		return false
	}
	return v.TokenStatus == "current" || v.TokenStatus == "invalid" || v.TokenStatus == "absent"
}
func writeDevice(w http.ResponseWriter, r *http.Request, id string, v notifications.DeviceView) {
	if v.InstallationID != id || !validDeviceView(v) {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(v.Revision, 10)+`"`)
	writeJSON(w, http.StatusOK, v)
}
func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	if _, err := groupQuery(r); err != nil || !noRuleBody(r) {
		writeDeviceError(w, r, notifications.ErrDeviceInvalid)
		return
	}
	if s.Devices == nil {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	items, err := s.Devices.List(r.Context(), actor(r))
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	valid := items != nil && len(items) <= notifications.MaximumBoundDevices
	prior := ""
	for _, v := range items {
		valid = valid && validDeviceView(v) && v.InstallationID > prior
		prior = v.InstallationID
	}
	page := struct {
		Items []notifications.DeviceView `json:"items"`
	}{items}
	encoded, err := json.Marshal(page)
	if !valid || err != nil || len(encoded) > 128<<10 {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
func (s *Server) inspectDevice(w http.ResponseWriter, r *http.Request) {
	id, _, err := deviceSelection(r, "inspect")
	var secret string
	if err != nil || decodeDeviceFields(w, r, 512, map[string]any{"installationSecret": &secret}) != nil {
		writeDeviceError(w, r, notifications.ErrDeviceInvalid)
		return
	}
	if _, err = notifications.InstallationSecretHash(id, secret); err != nil {
		writeDeviceError(w, r, err)
		return
	}
	if s.Devices == nil {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	state, err := s.Devices.InspectInstallation(r.Context(), actor(r), id, secret)
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	if state.InstallationID != id || state.Revision <= 0 || state.BoundToCurrentAccount && !state.HasBinding {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(state.Revision, 10)+`"`)
	writeJSON(w, http.StatusOK, state)
}
func (s *Server) bindDevice(w http.ResponseWriter, r *http.Request)   { s.deviceBinding(w, r, false) }
func (s *Server) switchDevice(w http.ResponseWriter, r *http.Request) { s.deviceBinding(w, r, true) }
func (s *Server) deviceBinding(w http.ResponseWriter, r *http.Request, switching bool) {
	condition, intent := "mutation", "confirmBind"
	if switching {
		condition, intent = "mutation", "confirmSwitch"
	}
	id, revision, err := deviceSelection(r, condition)
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	input, err := decodeDeviceRegistration(w, r, id, intent)
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	if s.Devices == nil {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	var result notifications.DeviceView
	if switching {
		result, err = s.Devices.SwitchBinding(r.Context(), actor(r), revision, input, true)
	} else {
		result, err = s.Devices.Bind(r.Context(), actor(r), revision, input)
	}
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	writeDevice(w, r, id, result)
}
func (s *Server) refreshDevice(w http.ResponseWriter, r *http.Request) {
	id, revision, err := deviceSelection(r, "mutation")
	var input notifications.DeviceRefresh
	if err != nil || decodeDeviceFields(w, r, 8<<10, map[string]any{"installationSecret": &input.InstallationSecret, "token": &input.Token, "permission": &input.Permission}) != nil || input.Validate(id) != nil {
		writeDeviceError(w, r, notifications.ErrDeviceInvalid)
		return
	}
	if s.Devices == nil {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	result, err := s.Devices.Refresh(r.Context(), actor(r), id, revision, input)
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	writeDevice(w, r, id, result)
}
func (s *Server) deviceSettings(w http.ResponseWriter, r *http.Request) {
	id, revision, err := deviceSelection(r, "mutation")
	var input notifications.DeviceSettings
	if err != nil || decodeDeviceFields(w, r, 2<<10, map[string]any{"label": &input.Label, "enabled": &input.Enabled, "muted": &input.Muted}) != nil || input.Validate() != nil {
		writeDeviceError(w, r, notifications.ErrDeviceInvalid)
		return
	}
	if s.Devices == nil {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	result, err := s.Devices.Settings(r.Context(), actor(r), id, revision, input)
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	writeDevice(w, r, id, result)
}
func (s *Server) removeDevice(w http.ResponseWriter, r *http.Request) { s.closeDevice(w, r, false) }
func (s *Server) detachDevice(w http.ResponseWriter, r *http.Request) { s.closeDevice(w, r, true) }
func (s *Server) closeDevice(w http.ResponseWriter, r *http.Request, detach bool) {
	id, revision, err := deviceSelection(r, "mutation")
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	var secret string
	if detach {
		err = decodeDeviceFields(w, r, 512, map[string]any{"installationSecret": &secret})
		if err == nil {
			_, err = notifications.InstallationSecretHash(id, secret)
		}
	} else if !noRuleBody(r) {
		err = notifications.ErrDeviceInvalid
	}
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	if s.Devices == nil {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	if detach {
		err = s.Devices.Detach(r.Context(), actor(r), id, revision, secret)
	} else {
		err = s.Devices.Remove(r.Context(), actor(r), id, revision)
	}
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
