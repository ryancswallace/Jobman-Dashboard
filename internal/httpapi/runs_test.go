package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

func TestRunRoutesBoundedAndAuthenticated(t *testing.T) {
	base := "/api/v1/deployments/11111111-1111-4111-8111-111111111111/namespaces/33333333-3333-4333-8333-333333333333/jobs/aaaaaaaa-aaaa-4aaa-8aaa-000000000000/runs"
	w := httptest.NewRecorder()
	app(t, false).ServeHTTP(w, httptest.NewRequest("GET", base, nil))
	if w.Code != 401 {
		t.Fatal("missing auth accepted")
	}
	handler := app(t, true)
	for _, query := range []string{"?limit=101", "?limit=0", "?limit=1&limit=2", "?offset=1", "?limit=%zz"} {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", base+query, nil))
		if w.Code != 400 {
			t.Fatalf("invalid %s status=%d", query, w.Code)
		}
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", base, nil))
	var page api.RunPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Total != "1" {
		t.Fatalf("run response=%d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", base+"/"+page.Items[0].ID, nil))
	var detail api.RunDetail
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.Run.ID != page.Items[0].ID {
		t.Fatalf("run detail=%d", w.Code)
	}
}
