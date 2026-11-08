package config

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/presentation"
)

func TestDeploymentOptionsRemainOfflineAndRoleBounded(t *testing.T) {
	c := example(t)
	p := presentation.Default()
	p.ProductName = "Example Jobs"
	p.ContentLinkScheme = "org.example.jobs"
	c.Presentation = &p
	c.OIDC.NativeRedirectURI = "org.example.auth://callback"
	c.UserDefaults = &UserDefaults{Timezone: "America/New_York", Appearance: "system", RefreshSeconds: 10}
	c.Operations = &Operations{DatabaseMaxConnections: 8, SessionLifetimeSeconds: 3600}
	c.Retention = &Retention{ReportDays: 90, InboxDays: 7}
	if _, err := Decode(bytes.NewReader(encoded(t, c))); err != nil {
		t.Fatal(err)
	}
	c.OIDC.NativeRedirectURI = "org.example.jobs://callback"
	if c.Validate() == nil {
		t.Fatal("authentication and content registrations must differ")
	}
	w := workerExample(t, WorkerIngestion)
	w.Operations = &Operations{DatabaseMaxConnections: 8}
	if _, err := workerDecode(t, w); err != nil {
		t.Fatal(err)
	}
	w.Operations.SessionLifetimeSeconds = 3600
	if _, err := workerDecode(t, w); err == nil {
		t.Fatal("worker accepted API-only operations")
	}
	for _, suffix := range []string{`"presentation":null`, `"operations":{"unknown":1}`, `"retention":{"reportDays":0,"inboxDays":7}`} {
		base := encoded(t, example(t))
		data := append([]byte("{"+suffix+","), base[1:]...)
		if _, err := Decode(bytes.NewReader(data)); err == nil {
			t.Fatal("invalid optional configuration accepted")
		}
	}
}

func TestDeploymentDefaultsAndBounds(t *testing.T) {
	var operations *Operations
	var retention *Retention
	var defaults *UserDefaults
	if operations.Effective().SessionLifetimeSeconds != 28800 || retention.Effective().ReportDays != 30 || defaults.Preferences().Appearance != "system" {
		t.Fatal("legacy defaults changed")
	}
	for _, c := range []Operations{{DatabaseMaxConnections: -1}, {DatabaseMaxConnections: 129}, {DatabaseMinConnections: 17}, {DatabaseConnectTimeoutSeconds: 31}, {SessionLifetimeSeconds: 299}, {SessionLifetimeSeconds: 28801}, {MonitoringTimeoutMilliseconds: 8001}} {
		if c.Validate() == nil {
			t.Fatalf("unsafe operation accepted: %+v", c)
		}
	}
	for _, c := range []Retention{{0, 30}, {1, 31}, {3651, 7}} {
		if c.Validate() == nil {
			t.Fatal("invalid retention accepted")
		}
	}
	for _, c := range []UserDefaults{{"Local", "system", 10}, {"UTC", "unknown", 10}, {"UTC", "system", 1}} {
		if c.Validate() == nil {
			t.Fatal("invalid defaults accepted")
		}
	}
}

func TestConfigurationSchemasFollowRoles(t *testing.T) {
	for _, role := range []string{"serve", "api", "worker", "operator", "broker"} {
		var output bytes.Buffer
		if err := WriteSchema(&output, role); err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(output.Bytes(), &schema); err != nil {
			t.Fatal(err)
		}
		props := schema["properties"].(map[string]any)
		if schema["additionalProperties"] != false || !strings.Contains(schema["$id"].(string), role) {
			t.Fatal("invalid schema contract")
		}
		_, has := props["presentation"]
		if has != (role == "api" || role == "serve") {
			t.Fatal("presentation leaked across roles")
		}
	}
	if WriteSchema(&bytes.Buffer{}, "unknown") == nil {
		t.Fatal("unknown role accepted")
	}
}
