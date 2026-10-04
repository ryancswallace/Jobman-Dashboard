package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

func (s *Server) reserveDeviceRevocation(w http.ResponseWriter, r *http.Request) {
	s.deviceRevocationCredential(w, r, false)
}
func (s *Server) activateDeviceRevocation(w http.ResponseWriter, r *http.Request) {
	s.deviceRevocationCredential(w, r, true)
}
func (s *Server) deviceRevocationCredential(w http.ResponseWriter, r *http.Request, activate bool) {
	condition := "reserve"
	if activate {
		condition = "mutation"
	}
	id, revision, err := deviceSelection(r, condition)
	var in notifications.DeviceRevocationInput
	if err != nil || decodeDeviceFields(w, r, 512, map[string]any{"installationSecret": &in.InstallationSecret, "revocationId": &in.RevocationID, "revocationCredential": &in.RevocationCredential}) != nil || in.Validate(id) != nil {
		writeDeviceError(w, r, notifications.ErrDeviceInvalid)
		return
	}
	if s.Devices == nil {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	var receipt notifications.DeviceRevocationReceipt
	if activate {
		receipt, err = s.Devices.ActivateRevocation(r.Context(), actor(r), id, revision, in)
	} else {
		receipt, err = s.Devices.ReserveRevocation(r.Context(), actor(r), id, revision, in)
	}
	if err != nil {
		writeDeviceError(w, r, err)
		return
	}
	if receipt.InstallationID != id || receipt.RevocationID != in.RevocationID || receipt.Revision <= 0 || receipt.Intent != "bind" && receipt.Intent != "switch" && receipt.Intent != "existing" || activate && receipt.Intent != "existing" {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(receipt.Revision, 10)+`"`)
	writeJSON(w, http.StatusOK, receipt)
}

// Per-handler fixed memory limits apply equally to known and unknown secrets.
// A limiter rejection never acknowledges revocation; native retains its queue.
type deviceRevocationHandler struct {
	server *Server
	mu     sync.Mutex
	tokens float64
	last   time.Time
	active chan struct{}
}

func newDeviceRevocationHandler(s *Server) *deviceRevocationHandler {
	return &deviceRevocationHandler{server: s, tokens: 120, last: time.Now(), active: make(chan struct{}, 8)}
}
func (h *deviceRevocationHandler) admit() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	h.tokens += now.Sub(h.last).Seconds() * 60
	h.last = now
	if h.tokens > 120 {
		h.tokens = 120
	}
	if h.tokens < 1 {
		return false
	}
	h.tokens--
	return true
}
func (h *deviceRevocationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.admit() {
		writeDeviceError(w, r, notifications.ErrDeviceRateLimited)
		return
	}
	select {
	case h.active <- struct{}{}:
		defer func() { <-h.active }()
	default:
		writeDeviceError(w, r, notifications.ErrDeviceRateLimited)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	deadline, _ := ctx.Deadline()
	// Production net/http HTTP/1 and HTTP/2 writers support request read deadlines.
	// In-memory test writers may not; global server ReadTimeout remains a fallback.
	if err := http.NewResponseController(w).SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	if len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("Cookie")) != 0 || len(r.Header.Values("Origin")) != 0 || len(r.Header.Values("If-Match")) != 0 || len(r.Header.Values("If-None-Match")) != 0 {
		writeDeviceError(w, r, notifications.ErrDeviceInvalid)
		return
	}
	if _, err := groupQuery(r); err != nil {
		writeDeviceError(w, r, notifications.ErrDeviceInvalid)
		return
	}
	var id, secret string
	if decodeDeviceFields(w, r, 512, map[string]any{"revocationId": &id, "revocationCredential": &secret}) != nil || notifications.ValidateDeviceRevocation(id, secret) != nil {
		writeDeviceError(w, r, notifications.ErrDeviceInvalid)
		return
	}
	if h.server.Devices == nil {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	if err := h.server.Devices.RevokeDevice(ctx, id, secret); err != nil {
		writeDeviceError(w, r, monitoring.ErrSource)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
