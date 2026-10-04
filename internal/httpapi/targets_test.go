package httpapi

import (
	"encoding/json"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestTargetRoutesStrictAuthenticationAndSelectors(t *testing.T) {
	base := "/api/v1/deployments/a/namespaces/n/targets/id"
	for _, path := range []string{"/api/v1/targets", base, base + "/partitions?generationId=g"} {
		w := httptest.NewRecorder()
		app(t, false).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Fatalf("unauthenticated %s=%d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/v1/targets?limit=201", "/api/v1/targets?limit=0", "/api/v1/targets?phase=running", "/api/v1/targets?scope=x&scope=y", base + "?limit=1", base + "/partitions", base + "/partitions?generationId=g&limit=201", base + "/partitions?generationId=g&generationId=h", base + "/partitions?generationId=g&cursor="} {
		w := httptest.NewRecorder()
		app(t, true).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatalf("invalid %s=%d %s", path, w.Code, w.Body.String())
		}
	}
}
func TestTargetHTTPPreservesSourceQualifiedIdentityAndGenerationPaging(t *testing.T) {
	h := app(t, true)
	get := func(path string, into any) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s=%d %s", path, w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
			t.Fatal(err)
		}
	}
	var catalog api.TargetPage
	get("/api/v1/targets?limit=1", &catalog)
	if catalog.Total != "2" || len(catalog.Totals) != 2 || catalog.NextCursor == "" {
		t.Fatalf("catalog %+v", catalog)
	}
	first := catalog.Items[0]
	get("/api/v1/targets?limit=1&cursor="+url.QueryEscape(catalog.NextCursor), &catalog)
	if catalog.Items[0].TargetID != first.TargetID || catalog.Items[0].DeploymentID == first.DeploymentID {
		t.Fatal("source identity lost")
	}
	base := "/api/v1/deployments/" + first.DeploymentID + "/namespaces/" + first.NamespaceID + "/targets/" + first.TargetID
	var detail api.TargetDetail
	get(base, &detail)
	if detail.Target.Generation.Number != "9007199254740993" || len(detail.Target.Generation.Partitions) != 200 || !detail.Target.Generation.PartitionsTruncated {
		t.Fatal("generation metadata lost")
	}
	var page api.TargetPartitionPage
	path := base + "/partitions?generationId=" + detail.Target.Generation.ID + "&limit=200"
	get(path, &page)
	if page.Total != "205" || len(page.Items) != 200 || page.NextCursor == "" {
		t.Fatal("unbounded partition page")
	}
	next := page.NextCursor
	page = api.TargetPartitionPage{}
	get(path+"&cursor="+url.QueryEscape(next), &page)
	if len(page.Items) != 5 || page.Items[0].Name != "partition-200" || page.NextCursor != "" {
		t.Fatalf("partition continuation %+v", page)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", base+"/partitions?generationId=replaced", nil))
	if w.Code != 409 {
		t.Fatalf("generation replacement=%d", w.Code)
	}
}
