package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPObservationPreservesStatusAndHidesContent(t *testing.T) {
	r, err := NewRegistry("api", 1, nil, Build{})
	if err != nil {
		t.Fatal(err)
	}
	handler := r.HTTP(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if http.NewResponseController(w) == nil {
			t.Fatal("missing response controller")
		}
		w.WriteHeader(503)
		_, _ = w.Write([]byte("private response"))
	}))
	req := httptest.NewRequest("GET", "/api/v1/jobs/private-job?token=private-token", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	raw := string(r.Render())
	if w.Code != 503 || w.Body.String() != "private response" || !strings.Contains(raw, `operation="api",deployment="",mode="",outcome="unavailable"} 1`) {
		t.Fatal(raw)
	}
	for _, private := range []string{"private-job", "private-token", "private response"} {
		if strings.Contains(raw, private) {
			t.Fatal("request data escaped")
		}
	}
}

func TestHTTPPanicObservationFailsWithoutSwallowingPanic(t *testing.T) {
	r := testRegistry(t)
	handler := r.HTTP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("private-panic") }))
	func() {
		defer func() {
			if recover() != "private-panic" {
				t.Fatal("observer swallowed or changed panic")
			}
		}()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/jobs", nil))
	}()
	raw := string(r.Render())
	if strings.Contains(raw, "private-panic") || !strings.Contains(raw, `operation="api",deployment="",mode="",outcome="unavailable"} 1`) {
		t.Fatal(raw)
	}
}
