package presentation

import "testing"

func TestPresentationDefaultsAndBoundaries(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
	if got := Effective(nil); got.ProductName != "Jobman Dashboard" || got.HelpLinks == nil || got.Notices == nil {
		t.Fatal("neutral default changed")
	}
	for name, mutate := range map[string]func(*Config){
		"schema":          func(c *Config) { c.SchemaVersion = 2 },
		"control":         func(c *Config) { c.HeaderText = "hidden\ntext" },
		"external asset":  func(c *Config) { c.LogoPath = "https://example.invalid/logo.svg" },
		"traversal":       func(c *Config) { c.LogoPath = "/a/../logo.svg" },
		"encoded asset":   func(c *Config) { c.LogoPath = "/%2f/logo.svg" },
		"active scheme":   func(c *Config) { c.ContentLinkScheme = "javascript" },
		"credential link": func(c *Config) { c.HelpLinks = []Link{{Label: "Help", URL: "https://user:secret@example.invalid"}} },
		"unsafe link":     func(c *Config) { c.HelpLinks = []Link{{Label: "Help", URL: "javascript:alert(1)"}} },
		"duplicate notice": func(c *Config) {
			c.Notices = []Notice{{ID: "one", Text: "First", Severity: "info"}, {ID: "one", Text: "Second", Severity: "warning"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := Default()
			mutate(&c)
			if c.Validate() == nil {
				t.Fatal("unsafe presentation accepted")
			}
		})
	}
	c := Default()
	c.ContentLinkScheme = "org.example.jobs"
	c.HelpLinks = []Link{{Label: "Support", URL: "https://support.example.invalid/guide"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"https://callback", "org.example.auth://callback/", "org.example.auth://callback?code=secret", "org.example.auth://callback#fragment", "jobman-dashboard://callback"} {
		if ValidRedirect(value) {
			t.Fatalf("unsafe callback accepted: %s", value)
		}
	}
	if !ValidRedirect("org.example.auth://callback") {
		t.Fatal("registered callback rejected")
	}
}
