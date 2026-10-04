package main

import (
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/httpapi"
)

func staticFixture(t *testing.T) (config.Config, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	web, private := filepath.Join(base, "web"), filepath.Join(base, "private")
	for _, dir := range []string{web, private} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{filepath.Join(web, "index.html"): "public application", filepath.Join(private, "secret"): "PRIVATE SENTINEL"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(private, "secret")
	return config.Config{WebRoot: web, ServerTLS: config.ServerTLS{KeyFile: secret}, DatabaseURLFile: secret, OIDC: config.OIDC{WebClientSecretFile: secret}, Encryption: config.Encryption{KeyFile: secret}, Reports: config.Reports{ObjectRoot: filepath.Join(private, "reports"), RedactionFile: secret}}, base
}

func mustLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestStaticStartupRejectsResolvedPrivateAliases(t *testing.T) {
	for _, scenario := range []string{"policy inside", "secret parent alias", "web alias", "object alias", "new object under alias", "web under object alias"} {
		t.Run(scenario, func(t *testing.T) {
			c, base := staticFixture(t)
			alias := filepath.Join(base, "alias")
			switch scenario {
			case "policy inside":
				c.Reports.RedactionFile = filepath.Join(c.WebRoot, "index.html")
			case "secret parent alias":
				mustLink(t, c.WebRoot, alias)
				c.Encryption.KeyFile = filepath.Join(alias, "index.html")
			case "web alias":
				mustLink(t, filepath.Join(base, "private"), alias)
				c.WebRoot = alias
			case "object alias":
				mustLink(t, c.WebRoot, alias)
				c.Reports.ObjectRoot = alias
			case "new object under alias":
				mustLink(t, c.WebRoot, alias)
				c.Reports.ObjectRoot = filepath.Join(alias, "new-reports")
			case "web under object alias":
				mustLink(t, base, alias)
				c.Reports.ObjectRoot = alias
			}
			root, err := openStatic(c)
			if root != nil {
				root.Close()
			}
			if err == nil {
				t.Fatal("private/static overlap accepted")
			}
		})
	}
}

func TestStaticRootPinsDirectoryAndConfinesLinksAfterStartup(t *testing.T) {
	c, base := staticFixture(t)
	root, err := openStatic(c)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// Links added after startup remain bounded; internal asset links still work.
	mustLink(t, "index.html", filepath.Join(c.WebRoot, "internal.html"))
	mustLink(t, c.Reports.RedactionFile, filepath.Join(c.WebRoot, "escape.json"))
	mustLink(t, "../private", filepath.Join(c.WebRoot, "escaped-parent"))
	for _, path := range []string{"escape.json", "escaped-parent/secret"} {
		if data, err := fs.ReadFile(root.FS(), path); err == nil {
			t.Fatalf("escape returned %d bytes", len(data))
		}
		response := httptest.NewRecorder()
		(&httpapi.Server{Static: root.FS()}).Handler().ServeHTTP(response, httptest.NewRequest("GET", "/"+path, nil))
		if strings.Contains(response.Body.String(), "PRIVATE SENTINEL") {
			t.Fatal("HTTP exposed a private file")
		}
	}
	if data, err := fs.ReadFile(root.FS(), "internal.html"); err != nil || string(data) != "public application" {
		t.Fatal("confined link unavailable", err)
	}
	if err := os.Rename(c.WebRoot, filepath.Join(base, "original-web")); err != nil {
		t.Fatal(err)
	}
	mustLink(t, filepath.Join(base, "private"), c.WebRoot)
	if data, err := fs.ReadFile(root.FS(), "index.html"); err != nil || string(data) != "public application" {
		t.Fatal("static root followed replacement directory", err)
	}
}

func TestStaticIndexCannotEscapeRoot(t *testing.T) {
	c, _ := staticFixture(t)
	if err := os.Remove(filepath.Join(c.WebRoot, "index.html")); err != nil {
		t.Fatal(err)
	}
	mustLink(t, c.Reports.RedactionFile, filepath.Join(c.WebRoot, "index.html"))
	root, err := openStatic(c)
	if root != nil {
		root.Close()
	}
	if err == nil {
		t.Fatal("external index accepted")
	}
}

func TestCompanionBuildVersionRequiresCanonicalImmutableDependency(t *testing.T) {
	for _, version := range []string{"v0.6.0", "v1.2.3-rc.1", "v0.6.1-0.20261003234632-f47b6058c06e", "v2.0.0+incompatible"} {
		build := &debug.BuildInfo{Deps: []*debug.Module{{Path: "github.com/ryancswallace/jobman-diagnose", Version: version}}}
		if got, err := companionBuildVersion(build); err != nil || got != version {
			t.Fatalf("canonical version %q rejected: %v", version, err)
		}
		build.Deps[0].Replace = &debug.Module{Path: "../jobman-diagnose"}
		if _, err := companionBuildVersion(build); err == nil {
			t.Fatal("replaced companion accepted")
		}
	}
	for _, version := range []string{"", "(devel)", "v1", "v1.2", "1.2.3", "v01.2.3", "v1.02.3", "v1.2.03", "v1.2.3-01", "v1.2.3-rc.01", "v1.2.3+local", "v1.2.3-", "v1.2.3-rc..1", "v1.2.3\n", strings.Repeat("1", 257)} {
		build := &debug.BuildInfo{Deps: []*debug.Module{{Path: "github.com/ryancswallace/jobman-diagnose", Version: version}}}
		if _, err := companionBuildVersion(build); err == nil {
			t.Fatalf("noncanonical version %q accepted", version)
		}
	}
	for _, build := range []*debug.BuildInfo{nil, {}, {Deps: []*debug.Module{nil, {Path: "other", Version: "v1.2.3"}}}} {
		if _, err := companionBuildVersion(build); err == nil {
			t.Fatal("absent companion accepted")
		}
	}
}
