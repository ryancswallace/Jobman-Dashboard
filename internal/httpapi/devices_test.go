package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

const testInstallationID = "81000000-0000-4000-8000-000000000001"

func deviceHTTPSecret() string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32))
}
func deviceHTTPBody(intent string) string {
	body, _ := json.Marshal(map[string]any{"installationSecret": deviceHTTPSecret(), "revocationId": testInstallationID, "revocationCredential": deviceHTTPSecret(), "label": "Synthetic phone", "topic": "test.jobman.dashboard", "environment": "sandbox", "token": "ab112233", "permission": "authorized", "enabled": true, "muted": false, intent: true})
	return string(body)
}
func deviceHTTPProof() string { return `{"installationSecret":"` + deviceHTTPSecret() + `"}` }

type deviceServiceFixture struct {
	value          notifications.DeviceView
	items          []notifications.DeviceView
	state          notifications.InstallationState
	err            error
	calls          int
	op, id, secret string
	revision       int64
	actor          monitoring.Actor
	registration   notifications.DeviceRegistration
	refresh        notifications.DeviceRefresh
	settings       notifications.DeviceSettings
	confirmed      bool
}

func newDeviceServiceFixture() *deviceServiceFixture {
	now := time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)
	v := notifications.DeviceView{InstallationID: testInstallationID, Revision: 9007199254740993, Label: "Synthetic phone", Topic: "test.jobman.dashboard", Environment: "sandbox", State: "bound", Enabled: true, Permission: "authorized", TokenStatus: "current", CreatedAt: now, UpdatedAt: now, LastSeenAt: now}
	return &deviceServiceFixture{value: v, items: []notifications.DeviceView{v}, state: notifications.InstallationState{InstallationID: testInstallationID, Revision: v.Revision, HasBinding: true}}
}
func (f *deviceServiceFixture) record(op string, a monitoring.Actor, id string, revision int64) {
	f.calls++
	f.op = op
	f.actor = a
	f.id = id
	f.revision = revision
}
func (f *deviceServiceFixture) List(_ context.Context, a monitoring.Actor) ([]notifications.DeviceView, error) {
	f.record("list", a, "", 0)
	return f.items, f.err
}
func (f *deviceServiceFixture) InspectInstallation(_ context.Context, a monitoring.Actor, id, secret string) (notifications.InstallationState, error) {
	f.record("inspect", a, id, 0)
	f.secret = secret
	return f.state, f.err
}
func (f *deviceServiceFixture) Bind(_ context.Context, a monitoring.Actor, revision int64, input notifications.DeviceRegistration) (notifications.DeviceView, error) {
	f.record("bind", a, input.InstallationID, revision)
	f.registration = input
	return f.value, f.err
}
func (f *deviceServiceFixture) SwitchBinding(_ context.Context, a monitoring.Actor, revision int64, input notifications.DeviceRegistration, confirmed bool) (notifications.DeviceView, error) {
	f.record("switch", a, input.InstallationID, revision)
	f.registration = input
	f.confirmed = confirmed
	return f.value, f.err
}
func (f *deviceServiceFixture) Refresh(_ context.Context, a monitoring.Actor, id string, revision int64, input notifications.DeviceRefresh) (notifications.DeviceView, error) {
	f.record("refresh", a, id, revision)
	f.refresh = input
	return f.value, f.err
}
func (f *deviceServiceFixture) Settings(_ context.Context, a monitoring.Actor, id string, revision int64, input notifications.DeviceSettings) (notifications.DeviceView, error) {
	f.record("settings", a, id, revision)
	f.settings = input
	return f.value, f.err
}
func (f *deviceServiceFixture) Remove(_ context.Context, a monitoring.Actor, id string, revision int64) error {
	f.record("remove", a, id, revision)
	return f.err
}
func (f *deviceServiceFixture) Detach(_ context.Context, a monitoring.Actor, id string, revision int64, secret string) error {
	f.record("detach", a, id, revision)
	f.secret = secret
	return f.err
}
func deviceHTTPHandler(f *deviceServiceFixture) http.Handler {
	return (&Server{Devices: f, Auth: AuthFunc(func(*http.Request) (monitoring.Actor, error) {
		return monitoring.Actor{Account: api.Account{ID: "82000000-0000-4000-8000-000000000001"}, DirectoryID: "83000000-0000-4000-8000-000000000001", Issuer: "https://synthetic.example", Subject: "current-device-actor"}, nil
	})}).Handler()
}
func deviceHTTPRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" && !strings.HasSuffix(path, "/inspect") {
		r.Header.Set("If-Match", `"9007199254740993"`)
	}
	return r
}
func TestDeviceHTTPRoutesUseCurrentActorAndPrivateBodies(t *testing.T) {
	base := "/api/v1/devices/" + testInstallationID
	for _, tc := range []struct {
		method, path, body, op string
		status                 int
	}{{"GET", "/api/v1/devices", "", "list", 200}, {"POST", base + "/inspect", deviceHTTPProof(), "inspect", 200}, {"POST", base + "/bind", deviceHTTPBody("confirmBind"), "bind", 200}, {"POST", base + "/switch", deviceHTTPBody("confirmSwitch"), "switch", 200}, {"PUT", base + "/token", `{"installationSecret":"` + deviceHTTPSecret() + `","token":"ab334455","permission":"denied"}`, "refresh", 200}, {"PUT", base + "/settings", `{"label":"Browser edited","enabled":false,"muted":true}`, "settings", 200}, {"DELETE", base, "", "remove", 204}, {"POST", base + "/detach", deviceHTTPProof(), "detach", 204}} {
		t.Run(tc.op, func(t *testing.T) {
			f := newDeviceServiceFixture()
			r := deviceHTTPRequest(tc.method, tc.path, tc.body)
			w := httptest.NewRecorder()
			deviceHTTPHandler(f).ServeHTTP(w, r)
			if w.Code != tc.status || f.calls != 1 || f.op != tc.op || f.actor.Subject != "current-device-actor" || f.actor.DirectoryID == "" {
				t.Fatal("route/current actor", w.Code, w.Body.String())
			}
			if tc.op != "list" && f.id != testInstallationID {
				t.Fatal("installation route binding")
			}
			if tc.op != "list" && tc.op != "inspect" && f.revision != 9007199254740993 {
				t.Fatal("wide CAS revision rounded")
			}
			if tc.op == "switch" && !f.confirmed {
				t.Fatal("explicit switch lost")
			}
			if tc.op == "refresh" && (f.refresh.Permission != "denied" || f.refresh.InstallationSecret != deviceHTTPSecret()) {
				t.Fatal("private refresh fields lost")
			}
			if tc.op == "settings" && (f.settings.Enabled || !f.settings.Muted) {
				t.Fatal("remote stop mutated")
			}
			for _, private := range []string{deviceHTTPSecret(), "ab112233", "ab334455", "current-device-actor", "bindingId", "tokenVersion", "secret_hash"} {
				if strings.Contains(w.Body.String(), private) {
					t.Fatal("response contains private device state")
				}
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("device response cacheable")
			}
			if tc.status == 200 && tc.op != "list" && w.Header().Get("ETag") != `"9007199254740993"` {
				t.Fatal("decimal revision ETag missing")
			}
		})
	}
	f := newDeviceServiceFixture()
	r := deviceHTTPRequest("POST", base+"/bind", deviceHTTPBody("confirmBind"))
	r.Header.Del("If-Match")
	r.Header.Set("If-None-Match", "*")
	w := httptest.NewRecorder()
	deviceHTTPHandler(f).ServeHTTP(w, r)
	if w.Code != 400 || f.calls != 0 {
		t.Fatal("explicit first registration condition", w.Code)
	}
}
func TestDeviceHTTPRejectsAmbiguousAndOversizedInputs(t *testing.T) {
	base := "/api/v1/devices/" + testInstallationID
	for name, body := range map[string]string{"duplicate": strings.Replace(deviceHTTPBody("confirmBind"), `"enabled":true`, `"enabled":true,"enabled":false`, 1), "escaped duplicate": strings.Replace(deviceHTTPBody("confirmBind"), `"muted":false`, `"muted":false,"mu\u0074ed":true`, 1), "case": strings.Replace(deviceHTTPBody("confirmBind"), `"label"`, `"Label"`, 1), "null": strings.Replace(deviceHTTPBody("confirmBind"), `"enabled":true`, `"enabled":null`, 1), "no intent": strings.Replace(deviceHTTPBody("confirmBind"), `"confirmBind":true,`, "", 1), "false intent": strings.Replace(deviceHTTPBody("confirmBind"), `"confirmBind":true`, `"confirmBind":false`, 1), "owner injection": strings.TrimSuffix(deviceHTTPBody("confirmBind"), "}") + `,"accountId":"other"}`, "secret padding": strings.Replace(deviceHTTPBody("confirmBind"), deviceHTTPSecret(), deviceHTTPSecret()+"=", 1), "uppercase token": strings.Replace(deviceHTTPBody("confirmBind"), "ab112233", "AB112233", 1), "oversized": strings.Repeat(" ", 16<<10) + deviceHTTPBody("confirmBind"), "trailing": deviceHTTPBody("confirmBind") + "{}"} {
		t.Run(name, func(t *testing.T) {
			f := newDeviceServiceFixture()
			w := httptest.NewRecorder()
			deviceHTTPHandler(f).ServeHTTP(w, deviceHTTPRequest("POST", base+"/bind", body))
			if w.Code != 400 || f.calls != 0 {
				t.Fatal("invalid bind reached store", w.Code)
			}
		})
	}
	for _, tc := range []struct{ method, path, body string }{{"GET", "/api/v1/devices?limit=1", ""}, {"GET", "/api/v1/devices", "{}"}, {"POST", base + "/inspect?installationSecret=private", deviceHTTPProof()}, {"POST", base + "/inspect", `{"installationSecret":"` + deviceHTTPSecret() + `","accountId":"other"}`}, {"PUT", base + "/token", `{"installationSecret":"` + deviceHTTPSecret() + `","token":"ab","permission":"authorized","enabled":true}`}, {"PUT", base + "/settings", `{"label":"x","enabled":true,"muted":false,"token":"ab"}`}, {"DELETE", base, "{}"}, {"POST", base + "/detach", "{}"}} {
		f := newDeviceServiceFixture()
		w := httptest.NewRecorder()
		deviceHTTPHandler(f).ServeHTTP(w, deviceHTTPRequest(tc.method, tc.path, tc.body))
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("private/unknown input reached device store", tc.path, w.Code)
		}
	}
	for _, value := range []string{"0", "01", "+1", "1e2", "*", `W/"1"`, `"1","2"`, "9223372036854775808"} {
		f := newDeviceServiceFixture()
		r := deviceHTTPRequest("DELETE", base, "")
		r.Header.Set("If-Match", value)
		w := httptest.NewRecorder()
		deviceHTTPHandler(f).ServeHTTP(w, r)
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("bad revision admitted", value, w.Code)
		}
	}
	for _, mode := range []string{"missing", "mixed", "repeat-match", "repeat-none", "none-switch", "none-inspect"} {
		f := newDeviceServiceFixture()
		path, body := base+"/bind", deviceHTTPBody("confirmBind")
		if mode == "none-switch" {
			path, body = base+"/switch", deviceHTTPBody("confirmSwitch")
		}
		if mode == "none-inspect" {
			path, body = base+"/inspect", deviceHTTPProof()
		}
		r := deviceHTTPRequest("POST", path, body)
		switch mode {
		case "missing":
			r.Header.Del("If-Match")
		case "mixed":
			r.Header.Set("If-None-Match", "*")
		case "repeat-match":
			r.Header.Add("If-Match", "2")
		default:
			r.Header.Del("If-Match")
			r.Header.Set("If-None-Match", "*")
			if mode == "repeat-none" {
				r.Header.Add("If-None-Match", "*")
			}
		}
		w := httptest.NewRecorder()
		deviceHTTPHandler(f).ServeHTTP(w, r)
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("ambiguous condition admitted", mode, w.Code)
		}
	}
}
func TestDeviceHTTPAuthenticationAndSafeErrors(t *testing.T) {
	for _, err := range []error{&api.Error{Code: "unauthenticated", Message: "Sign in"}, monitoring.ErrForbidden} {
		f := newDeviceServiceFixture()
		handler := (&Server{Devices: f, Auth: AuthFunc(func(r *http.Request) (monitoring.Actor, error) { return monitoring.Actor{}, err })}).Handler()
		r := deviceHTTPRequest("POST", "/api/v1/devices/"+testInstallationID+"/inspect", deviceHTTPProof())
		r.Header.Set("X-CSRF-Token", "rejected")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 401 && w.Code != 403 || f.calls != 0 || strings.Contains(w.Body.String(), deviceHTTPSecret()) {
			t.Fatal("authentication/CSRF rejection did not stop private operation", w.Code)
		}
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
		retry  bool
	}{{notifications.ErrDeviceNotFound, 404, "not_found_or_inaccessible", false}, {notifications.ErrDeviceConflict, 409, "revision_conflict", false}, {notifications.ErrDeviceInvalid, 400, "invalid_request", false}, {notifications.ErrDeviceCapacity, 429, "device_capacity", false}, {notifications.ErrDeviceRateLimited, 429, "rate_limited", true}, {notifications.ErrDeviceUnavailable, 503, "source_unavailable", false}, {errors.New("SQL secret token=ab112233 owner=private"), 503, "source_unavailable", false}} {
		f := newDeviceServiceFixture()
		f.err = tc.err
		r := deviceHTTPRequest("POST", "/api/v1/devices/"+testInstallationID+"/inspect", deviceHTTPProof())
		w := httptest.NewRecorder()
		deviceHTTPHandler(f).ServeHTTP(w, r)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) || strings.Contains(w.Body.String(), "ab112233") || strings.Contains(w.Body.String(), "SQL") || (w.Header().Get("Retry-After") != "") != tc.retry {
			t.Fatal("unsafe device error", w.Code, w.Body.String())
		}
	}
}
func TestDeviceHTTPBoundsAndRejectsBrokenProjection(t *testing.T) {
	f := newDeviceServiceFixture()
	f.items = nil
	for n := 1; n <= 50; n++ {
		v := f.value
		v.InstallationID = fmt.Sprintf("81000000-0000-4000-8000-%012d", n)
		v.Label = strings.Repeat("<>&", 40)
		v.Topic = strings.Repeat("a", 251) + ".app"
		f.items = append(f.items, v)
	}
	w := httptest.NewRecorder()
	deviceHTTPHandler(f).ServeHTTP(w, deviceHTTPRequest("GET", "/api/v1/devices", ""))
	if w.Code != 200 || w.Body.Len() > 128<<10 {
		t.Fatal("bounded complete worst-case list", w.Code, w.Body.Len())
	}
	for _, mutation := range []func(*deviceServiceFixture){func(f *deviceServiceFixture) {
		f.items = nil
		for n := 1; n <= 51; n++ {
			v := f.value
			v.InstallationID = fmt.Sprintf("81000000-0000-4000-8000-%012d", n)
			f.items = append(f.items, v)
		}
	}, func(f *deviceServiceFixture) { f.items[0].Revision = 0 }, func(f *deviceServiceFixture) { f.items[0].TokenStatus = "secret" }, func(f *deviceServiceFixture) { f.items[0].LastSeenAt = f.items[0].UpdatedAt.Add(time.Hour) }, func(f *deviceServiceFixture) { f.items = []notifications.DeviceView{f.value, f.value} }, func(f *deviceServiceFixture) { f.items = nil }} {
		f := newDeviceServiceFixture()
		mutation(f)
		w := httptest.NewRecorder()
		deviceHTTPHandler(f).ServeHTTP(w, deviceHTTPRequest("GET", "/api/v1/devices", ""))
		if w.Code != 503 {
			t.Fatal("malformed device projection serialized", w.Code)
		}
	}
	f = newDeviceServiceFixture()
	f.state.HasBinding = false
	f.state.BoundToCurrentAccount = true
	w = httptest.NewRecorder()
	deviceHTTPHandler(f).ServeHTTP(w, deviceHTTPRequest("POST", "/api/v1/devices/"+testInstallationID+"/inspect", deviceHTTPProof()))
	if w.Code != 503 {
		t.Fatal("invalid inspection projection serialized")
	}
}

func (f *deviceServiceFixture) ReserveRevocation(_ context.Context, a monitoring.Actor, id string, rev int64, in notifications.DeviceRevocationInput) (notifications.DeviceRevocationReceipt, error) {
	f.record("reserve", a, id, rev)
	f.secret = in.RevocationCredential
	return notifications.DeviceRevocationReceipt{InstallationID: id, Revision: f.value.Revision, RevocationID: in.RevocationID, Intent: "bind"}, f.err
}
func (f *deviceServiceFixture) ActivateRevocation(_ context.Context, a monitoring.Actor, id string, rev int64, in notifications.DeviceRevocationInput) (notifications.DeviceRevocationReceipt, error) {
	f.record("activate", a, id, rev)
	return notifications.DeviceRevocationReceipt{InstallationID: id, Revision: f.value.Revision, RevocationID: in.RevocationID, Intent: "existing"}, f.err
}
func (f *deviceServiceFixture) RevokeDevice(_ context.Context, id, secret string) error {
	f.record("revoke", monitoring.Actor{}, id, 0)
	f.secret = secret
	return f.err
}
