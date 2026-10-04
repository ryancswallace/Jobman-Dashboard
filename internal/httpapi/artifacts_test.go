package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

func TestArtifactMetadataRoutePreservesPagingWithoutByteAccess(t *testing.T) {
	base := "/api/v1/deployments/11111111-1111-4111-8111-111111111111/namespaces/33333333-3333-4333-8333-333333333333/jobs/aaaaaaaa-aaaa-4aaa-8aaa-000000000000/artifacts"
	for _, query := range []string{"?limit=101", "?limit=0", "?runNumber=0", "?runNumber=-1", "?runNumber=1&runNumber=2", "?path=/private", "?limit=%zz"} {
		w := httptest.NewRecorder()
		app(t, true).ServeHTTP(w, httptest.NewRequest("GET", base+query, nil))
		if w.Code != 400 {
			t.Fatalf("bad query %s: %d", query, w.Code)
		}
	}
	w := httptest.NewRecorder()
	app(t, false).ServeHTTP(w, httptest.NewRequest("GET", base, nil))
	if w.Code != 401 {
		t.Fatal("missing authentication")
	}
	handler := app(t, true)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", base+"?limit=50", nil))
	var first api.ArtifactPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &first) != nil || len(first.Items) != 50 || first.Total != "55" || first.NextCursor == "" {
		t.Fatalf("metadata: %d %s", w.Code, w.Body.String())
	}
	for _, word := range []string{"objectKey", "storeName", "downloadUrl", "bytesBase64"} {
		if strings.Contains(w.Body.String(), word) {
			t.Fatalf("metadata exposed %s", word)
		}
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", base+"?limit=50&cursor="+url.QueryEscape(first.NextCursor), nil))
	var next api.ArtifactPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &next) != nil || len(next.Items) != 5 || next.Items[0].Name != "synthetic-result-050" || next.NextCursor != "" {
		t.Fatalf("continuation %d %s", w.Code, w.Body.String())
	}
}
