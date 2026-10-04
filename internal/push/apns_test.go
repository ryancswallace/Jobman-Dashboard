package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

var testTime = time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)

func testRequest() Request {
	return Request{DeliveryID: "90000000-0000-4000-8000-000000000001", InboxID: "90000000-0000-4000-8000-000000000002", Token: strings.Repeat("a1", 32), Topic: "test.jobman.dashboard", Environment: "sandbox", ExpiresAt: testTime.Add(time.Hour)}
}
func provider(t *testing.T, handler http.Handler) (*APNs, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewAPNs(APNsConfig{TeamID: "ABCDEFGHIJ", KeyID: "0123456789", Topic: "test.jobman.dashboard", Environment: "sandbox", PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	t.Cleanup(c.Close)
	// Test-only pinned loopback transport. Production construction has no custom
	// origin, proxy or trust-root knob that could send tokens to another service.
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	c.endpoint = server.URL
	c.client = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true}, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	c.now = func() time.Time { return testTime }
	return c, key
}
func TestAPNsUsesVerifiedHTTP2SignedTokenAndGenericPayload(t *testing.T) {
	var signed string
	c, key := provider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.TLS == nil || r.Method != "POST" || r.URL.Path != "/3/device/"+testRequest().Token {
			t.Error("wrong handoff protocol")
		}
		signed = strings.TrimPrefix(r.Header.Get("Authorization"), "bearer ")
		for field, value := range map[string]string{"apns-topic": "test.jobman.dashboard", "apns-push-type": "alert", "apns-priority": "10", "apns-collapse-id": testRequest().InboxID, "apns-id": testRequest().DeliveryID, "apns-expiration": fmt.Sprint(testTime.Add(5 * time.Minute).Unix())} {
			if r.Header.Get(field) != value {
				t.Error("incorrect bounded delivery header", field)
			}
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]json.RawMessage
		if json.Unmarshal(body, &payload) != nil || len(payload) != 3 || string(payload["inboxId"]) != `"`+testRequest().InboxID+`"` || string(payload["schemaVersion"]) != "1" {
			t.Error("unsafe application payload")
		}
		if strings.Contains(string(body), "jobId") || strings.Contains(string(body), testRequest().Token) || !strings.Contains(string(body), "A monitored job has an update.") {
			t.Error("payload disclosure differs")
		}
		w.Header().Set("apns-id", r.Header.Get("apns-id"))
		w.WriteHeader(200)
	}))
	result, err := c.Send(context.Background(), testRequest())
	if err != nil || result.Outcome != "accepted" || result.Ambiguous {
		t.Fatal(result, err)
	}
	token, err := jwt.ParseSigned(signed, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Issuer   string `json:"iss"`
		IssuedAt int64  `json:"iat"`
	}
	if err = token.Claims(&key.PublicKey, &claims); err != nil || claims.Issuer != "ABCDEFGHIJ" || claims.IssuedAt != testTime.Unix() || token.Headers[0].KeyID != "0123456789" {
		t.Fatal("invalid provider assertion")
	}
}
func TestAPNsTokenReuseAndClockRollback(t *testing.T) {
	c, _ := provider(t, http.NotFoundHandler())
	first, err := c.authorization(testTime)
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.authorization(testTime.Add(44 * time.Minute))
	if err != nil || again != first {
		t.Fatal("provider token refreshed too often")
	}
	next, err := c.authorization(testTime.Add(45 * time.Minute))
	if err != nil || next == first {
		t.Fatal("provider token not refreshed")
	}
	if _, err = c.authorization(testTime); err == nil {
		t.Fatal("clock rollback reused future token")
	}
}
func TestAPNsResponsesHaveBoundedDeliveryDispositions(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		body, outcome string
		delay         time.Duration
		ambiguous     bool
	}{
		{"unregistered", 410, `{"reason":"Unregistered","timestamp":` + fmt.Sprint(testTime.Add(-time.Minute).UnixMilli()) + `}`, "token_invalid", 0, false},
		{"bad token", 400, `{"reason":"BadDeviceToken"}`, "token_invalid", 0, false},
		{"bad timestamp", 410, `{"reason":"Unregistered","timestamp":"not-an-integer"}`, "retry", 15 * time.Minute, true},
		{"idle connection", 400, `{"reason":"IdleTimeout"}`, "retry", time.Minute, false},
		{"rate", 429, `{"reason":"TooManyRequests"}`, "retry", time.Minute, false},
		{"provider rate", 429, `{"reason":"TooManyProviderTokenUpdates"}`, "provider_error", 20 * time.Minute, false},
		{"provider key", 403, `{"reason":"InvalidProviderToken"}`, "provider_error", 0, false},
		{"large", 413, `{"reason":"PayloadTooLarge"}`, "rejected", 0, false},
		{"unavailable", 503, `{"reason":"ServiceUnavailable"}`, "retry", 15 * time.Minute, false},
		{"new private error", 403, `{"reason":"private diagnostic must not escape"}`, "provider_error", 0, false},
		{"malformed", 500, `<private response>`, "retry", 15 * time.Minute, true},
		{"oversized", 500, strings.Repeat("x", 4097), "retry", 15 * time.Minute, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := provider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("apns-id", r.Header.Get("apns-id"))
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			result, err := c.Send(context.Background(), testRequest())
			if err != nil || result.Outcome != tc.outcome || result.RetryAfter != tc.delay || result.Ambiguous != tc.ambiguous || strings.Contains(result.Reason, "private") {
				t.Fatal(result, err)
			}
			if tc.name == "unregistered" && (result.TokenInvalidAt == nil || !result.TokenInvalidAt.Equal(testTime.Add(-time.Minute))) {
				t.Fatal("APNs invalidation milliseconds lost")
			}
		})
	}
}
func TestAPNsRejectsCrossEnvironmentAndExpiredRequestsBeforeNetwork(t *testing.T) {
	reads := 0
	c, _ := provider(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reads++ }))
	for _, change := range []func(*Request){func(r *Request) { r.Environment = "production" }, func(r *Request) { r.Topic = "another.app" }, func(r *Request) { r.ExpiresAt = testTime }, func(r *Request) { r.ExpiresAt = testTime.Add(25 * time.Hour) }, func(r *Request) { r.Token = "../token" }, func(r *Request) { r.InboxID = nilUUID }} {
		r := testRequest()
		change(&r)
		if _, err := c.Send(context.Background(), r); err == nil {
			t.Fatal("invalid handoff reached provider")
		}
	}
	if reads != 0 {
		t.Fatal("invalid handoff sent")
	}
	for _, value := range []string{"", "0", "AA", strings.Repeat("a", 1026), "gg"} {
		if ValidDeviceToken(value) {
			t.Fatal("invalid token accepted")
		}
	}
	if !ValidDeviceToken("01") || !ValidDeviceToken(strings.Repeat("ab", 64)) {
		t.Fatal("assumed fixed token length")
	}
}
