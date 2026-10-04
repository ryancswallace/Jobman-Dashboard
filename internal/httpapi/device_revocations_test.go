package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func revocationHTTPBody(proof bool) string {
	s := `{"revocationId":"` + testInstallationID + `","revocationCredential":"` + deviceHTTPSecret() + `"`
	if proof {
		s += `,"installationSecret":"` + deviceHTTPSecret() + `"`
	}
	return s + `}`
}
func TestDeviceRevocationHTTPStrictPhases(t *testing.T) {
	for _, suffix := range []string{"revocation-reservations", "revocation-credentials"} {
		f := newDeviceServiceFixture()
		r := deviceHTTPRequest("POST", "/api/v1/devices/"+testInstallationID+"/"+suffix, revocationHTTPBody(true))
		w := httptest.NewRecorder()
		deviceHTTPHandler(f).ServeHTTP(w, r)
		if w.Code != 200 || f.actor.Account.ID == "" || strings.Contains(w.Body.String(), deviceHTTPSecret()) {
			t.Fatal("phase failed or leaked", suffix, w.Code)
		}
	}
	f := newDeviceServiceFixture()
	r := deviceHTTPRequest("POST", "/api/v1/devices/"+testInstallationID+"/revocation-reservations", revocationHTTPBody(true))
	r.Header.Del("If-Match")
	r.Header.Set("If-None-Match", "*")
	w := httptest.NewRecorder()
	deviceHTTPHandler(f).ServeHTTP(w, r)
	if w.Code != 200 || f.revision != 0 {
		t.Fatal("first unbound reservation", w.Code)
	}
}
func TestNativeDeviceRevocationHasNoSessionAuthority(t *testing.T) {
	f := newDeviceServiceFixture()
	authCalls := 0
	h := (&Server{Devices: f, Auth: AuthFunc(func(*http.Request) (monitoring.Actor, error) {
		authCalls++
		return monitoring.Actor{}, errors.New("signed out")
	})}).Handler()
	for range 2 {
		r := httptest.NewRequest("POST", "/auth/native/device-revocations", strings.NewReader(revocationHTTPBody(false)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 204 || w.Body.Len() != 0 {
			t.Fatal("signed-out acknowledgement", w.Code)
		}
	}
	if authCalls != 0 || f.calls != 2 || f.actor.Account.ID != "" {
		t.Fatal("revocation required or obtained session authority")
	}
	for _, header := range []string{"Authorization", "Cookie", "Origin", "If-Match", "If-None-Match"} {
		r := httptest.NewRequest("POST", "/auth/native/device-revocations", strings.NewReader(revocationHTTPBody(false)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(header, "forbidden")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("accepted header", header, w.Code)
		}
	}
	for _, body := range []string{strings.Replace(revocationHTTPBody(false), `"revocationId"`, `"RevocationId"`, 1), strings.TrimSuffix(revocationHTTPBody(false), "}") + `,"revocationId":"` + testInstallationID + `"}`, revocationHTTPBody(true), strings.Repeat(" ", 513) + revocationHTTPBody(false), `{"revocationId":null,"revocationCredential":"` + deviceHTTPSecret() + `"}`} {
		r := httptest.NewRequest("POST", "/auth/native/device-revocations", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("ambiguous input accepted", w.Code)
		}
	}
	f.err = errors.New("synthetic private database detail")
	r := httptest.NewRequest("POST", "/auth/native/device-revocations", strings.NewReader(revocationHTTPBody(false)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), "synthetic private database detail") {
		t.Fatal("failure acknowledged or leaked", w.Code)
	}
}
func TestNativeDeviceRevocationBoundedAdmission(t *testing.T) {
	f := newDeviceServiceFixture()
	h := newDeviceRevocationHandler(&Server{Devices: f})
	h.tokens = 0
	h.last = time.Now().Add(time.Hour)
	r := httptest.NewRequest("POST", "/auth/native/device-revocations", strings.NewReader(revocationHTTPBody(false)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 429 || f.calls != 0 {
		t.Fatal("rate bound bypassed")
	}
	h.tokens = 120
	h.last = time.Now()
	for range cap(h.active) {
		h.active <- struct{}{}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 429 || f.calls != 0 {
		t.Fatal("concurrency bound bypassed")
	}
}

type revocationDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (r *revocationDeadlineRecorder) SetReadDeadline(t time.Time) error { r.deadline = t; return nil }
func TestNativeDeviceRevocationBoundsBodyReadDeadline(t *testing.T) {
	f := newDeviceServiceFixture()
	h := newDeviceRevocationHandler(&Server{Devices: f})
	r := httptest.NewRequest("POST", "/auth/native/device-revocations", strings.NewReader(revocationHTTPBody(false)))
	r.Header.Set("Content-Type", "application/json")
	w := &revocationDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	before := time.Now()
	h.ServeHTTP(w, r)
	if w.Code != 204 || w.deadline.Before(before) || w.deadline.After(time.Now().Add(5*time.Second)) {
		t.Fatal("body read not bounded to request deadline", w.Code, w.deadline)
	}
}
