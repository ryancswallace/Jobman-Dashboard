package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func workerExample(t *testing.T, components ...WorkerComponent) WorkerConfig {
	t.Helper()
	base := example(t)
	return WorkerConfig{ConfigurationRevision: 1, DatabaseURLFile: "/private/worker-db", Components: components, Controls: base.Controls}
}
func workerDecode(t *testing.T, c WorkerConfig) (WorkerConfig, error) {
	t.Helper()
	data, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	return DecodeWorker(strings.NewReader(string(data)))
}

func TestWorkerComponentsSelectBoundedCredentialShape(t *testing.T) {
	for _, component := range []WorkerComponent{WorkerIngestion, WorkerNotifications, WorkerDelivery, WorkerReports, WorkerRetention} {
		t.Run(string(component), func(t *testing.T) {
			c := workerExample(t, component)
			switch component {
			case WorkerNotifications:
				c.IdentityIssuer = "https://identity.example.internal/issuer"
				c.Notifications.DeviceTopics = []NotificationTopic{{Topic: "org.example.dashboard", Environment: "sandbox"}}
			case WorkerDelivery:
				c.IdentityIssuer = "https://identity.example.internal/issuer"
				c.Notifications.DeviceTopics = []NotificationTopic{{Topic: "org.example.dashboard", Environment: "sandbox"}}
				c.Notifications.TokenEncryption = &TokenEncryption{Current: Encryption{KeyID: "device-v1", KeyFile: "/private/token-key"}}
				c.Notifications.APNs = []APNsProvider{{Topic: "org.example.dashboard", Environment: "sandbox", TeamID: "AAAAAAAAAA", KeyID: "BBBBBBBBBB", PrivateKeyFile: "/private/apple-key"}}
			case WorkerReports:
				c.Reports.ObjectRoot = "/private/reports"
			case WorkerRetention:
				c.Controls = []Control{} // no Control credential is required
			}
			got, e := workerDecode(t, c)
			if e != nil || !got.Has(component) {
				t.Fatalf("valid component rejected: %v", e)
			}
		})
	}
}
func TestWorkerRejectsCrossRoleAndAmbiguousConfiguration(t *testing.T) {
	base := workerExample(t, WorkerIngestion)
	data, e := json.Marshal(base)
	if e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"oidc", "encryption", "serverTLS", "webRoot", "listen", "publicOrigin"} {
		t.Run(field, func(t *testing.T) {
			document := `{"` + field + `":"SECRET-CANARY",` + string(data[1:])
			if _, e := DecodeWorker(strings.NewReader(document)); e == nil || strings.Contains(e.Error(), "SECRET-CANARY") {
				t.Fatalf("cross-role field accepted/leaked: %v", e)
			}
		})
	}
	cases := map[string]func(*WorkerConfig){
		"no component":        func(c *WorkerConfig) { c.Components = nil },
		"unknown component":   func(c *WorkerConfig) { c.Components = []WorkerComponent{"api"} },
		"duplicate component": func(c *WorkerConfig) { c.Components = append(c.Components, WorkerIngestion) },
		"no sources":          func(c *WorkerConfig) { c.Controls = nil },
		"unused issuer":       func(c *WorkerConfig) { c.IdentityIssuer = "https://identity.example.internal" },
		"missing issuer":      func(c *WorkerConfig) { c.Components = []WorkerComponent{WorkerNotifications} },
		"nonHTTPS issuer": func(c *WorkerConfig) {
			c.Components = []WorkerComponent{WorkerNotifications}
			c.IdentityIssuer = "http://identity.example.internal"
		},
		"unused key": func(c *WorkerConfig) {
			c.Notifications.TokenEncryption = &TokenEncryption{Current: Encryption{KeyID: "k", KeyFile: "/private/key"}}
		},
		"legacy key": func(c *WorkerConfig) {
			c.Notifications.PreviousTokenKeys = []Encryption{{KeyID: "k", KeyFile: "/private/key"}}
		},
		"no provider": func(c *WorkerConfig) {
			c.Components = []WorkerComponent{WorkerDelivery}
			c.IdentityIssuer = "https://identity.example.internal"
		},
		"unused report root": func(c *WorkerConfig) { c.Reports.ObjectRoot = "/private/reports" },
		"report no root":     func(c *WorkerConfig) { c.Components = []WorkerComponent{WorkerReports} },
		"report key absent": func(c *WorkerConfig) {
			c.Components = []WorkerComponent{WorkerReports}
			c.Reports = Reports{ObjectRoot: "/private/reports", RedactionFile: "/private/policy"}
		},
		"report policy absent": func(c *WorkerConfig) {
			c.Components = []WorkerComponent{WorkerReports}
			c.Reports = Reports{ObjectRoot: "/private/reports", PolicyKeyFile: "/private/key"}
		},
		"retention policy": func(c *WorkerConfig) {
			c.Components = []WorkerComponent{WorkerRetention}
			c.Reports = Reports{ObjectRoot: "/private/reports", RedactionFile: "/private/policy", PolicyKeyFile: "/private/key"}
		},
		"unused cursor key": func(c *WorkerConfig) { c.LogCursorKeyFile = "/private/key" },
		"unused device policy": func(c *WorkerConfig) {
			c.Notifications.DeviceTopics = []NotificationTopic{{Topic: "org.example.dashboard", Environment: "sandbox"}}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := workerExample(t, WorkerIngestion)
			change(&c)
			if _, e := workerDecode(t, c); e == nil {
				t.Fatal("invalid role material accepted")
			}
		})
	}
}

func TestWorkerExampleUsesExplicitPurposeKeysWithoutInteractiveSecrets(t *testing.T) {
	c, e := LoadWorker("../../deploy/worker.example.json")
	// Load requires absolute paths, like production; resolve only the public fixture.
	if e == nil {
		t.Fatal("relative configuration path accepted")
	}
	path, e := filepath.Abs("../../deploy/worker.example.json")
	if e != nil {
		t.Fatal(e)
	}
	c, e = LoadWorker(path)
	if e != nil {
		t.Fatal(e)
	}
	if !c.Has(WorkerReports) || c.Has(WorkerDelivery) || c.Reports.PolicyKeyFile == "" || c.LogCursorKeyFile == "" || c.Notifications.TokenEncryption != nil {
		t.Fatal("worker example authority changed")
	}
}
