package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/presentation"
)

func TestPublicPresentationIsAnExplicitUnauthenticatedProjection(t *testing.T) {
	p := presentation.Default()
	p.ProductName = "Example Jobs"
	p.Notices = []presentation.Notice{{ID: "notice", Text: "Authorized users only", Severity: "warning"}}
	themed := p
	themed.Theme = &presentation.Theme{Light: presentation.AccentPair{Accent: "#00675f", Foreground: "#ffffff"}, Dark: presentation.AccentPair{Accent: "#78d9cb", Foreground: "#14141d"}}
	for _, configured := range []*presentation.Config{nil, &p, &themed} {
		s := &Server{Presentation: configured}
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/presentation", nil))
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("public projection failed: %d", response.Code)
		}
		var got presentation.Config
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, presentation.Effective(configured)) {
			t.Fatal("presentation mismatch")
		}
		var fields map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil {
			t.Fatal(err)
		}
		allowed := []string{"schemaVersion", "productName", "organizationName", "authLabel", "headerText", "footerText", "logoPath", "logoDarkPath", "helpLinks", "notices", "contentLinkScheme"}
		if configured != nil && configured.Theme != nil {
			allowed = append(allowed, "theme")
		}
		if len(fields) != len(allowed) {
			t.Fatal("public field allowlist changed")
		}
		for _, name := range allowed {
			if _, ok := fields[name]; !ok {
				t.Fatalf("missing public field %s", name)
			}
		}
		private := httptest.NewRecorder()
		s.Handler().ServeHTTP(private, httptest.NewRequest("GET", "/api/v1/bootstrap", nil))
		if private.Code != 401 {
			t.Fatal("presentation bypassed authentication")
		}
	}
}
