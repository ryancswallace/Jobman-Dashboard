// Package presentation defines the deliberately public deployment appearance.
// No runtime, identity, network, authorization or credential configuration belongs here.
package presentation

import (
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}
type Notice struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Severity string `json:"severity"`
}
type Config struct {
	Theme             *Theme   `json:"theme,omitempty"`
	SchemaVersion     int64    `json:"schemaVersion"`
	ProductName       string   `json:"productName"`
	OrganizationName  string   `json:"organizationName"`
	AuthLabel         string   `json:"authLabel"`
	HeaderText        string   `json:"headerText"`
	FooterText        string   `json:"footerText"`
	LogoPath          string   `json:"logoPath"`
	LogoDarkPath      string   `json:"logoDarkPath"`
	HelpLinks         []Link   `json:"helpLinks"`
	Notices           []Notice `json:"notices"`
	ContentLinkScheme string   `json:"contentLinkScheme"`
}

func Default() Config {
	return Config{SchemaVersion: 1, ProductName: "Jobman Dashboard", AuthLabel: "Sign in with your organization", FooterText: "Private workspace", LogoPath: "/logo.svg", LogoDarkPath: "/logo-transparent-dashboard.svg", HelpLinks: []Link{}, Notices: []Notice{}, ContentLinkScheme: "jobman-dashboard"}
}
func Effective(c *Config) Config {
	if c == nil {
		return Default()
	}
	v := *c
	if v.HelpLinks == nil {
		v.HelpLinks = []Link{}
	}
	if v.Notices == nil {
		v.Notices = []Notice{}
	}
	return v
}

var scheme = regexp.MustCompile(`^[a-z][a-z0-9.-]{2,79}$`)
var assetPath = regexp.MustCompile(`^/[A-Za-z0-9_./-]+\.(svg|png|webp)$`)

// ValidScheme accepts the legacy registration and domain-qualified private schemes.
// Reserved browser/system schemes can never be registered by deployment input.
func ValidScheme(value string) bool {
	return scheme.MatchString(value) && (value == "jobman-dashboard" || value == "jobman-dashboard-auth" || strings.Contains(value, ".")) && !strings.Contains(value, "..") && !strings.HasSuffix(value, ".")
}
func ValidRedirect(value string) bool {
	u, err := url.Parse(value)
	return err == nil && ValidScheme(u.Scheme) && u.Scheme != "jobman-dashboard" && u.Host == "callback" && u.User == nil && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && value == u.Scheme+"://callback"
}
func plain(value string, limit int, required bool) bool {
	return (!required || value != "") && utf8.ValidString(value) && len(value) <= limit && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}
func asset(value string) bool {
	return len(value) <= 256 && assetPath.MatchString(value) && !strings.HasPrefix(value, "//") && path.Clean(value) == value
}
func (c Config) Validate() error {
	if err := c.Theme.Validate(); err != nil {
		return err
	}
	if c.SchemaVersion != 1 || !plain(c.ProductName, 120, true) || !plain(c.OrganizationName, 200, false) || !plain(c.AuthLabel, 120, true) || !plain(c.HeaderText, 1000, false) || !plain(c.FooterText, 1000, false) || !asset(c.LogoPath) || !asset(c.LogoDarkPath) || !ValidScheme(c.ContentLinkScheme) || len(c.HelpLinks) > 8 || len(c.Notices) > 8 {
		return errors.New("invalid versioned public presentation")
	}
	ids := map[string]bool{}
	for _, n := range c.Notices {
		if !plain(n.ID, 64, true) || !plain(n.Text, 4000, true) || (n.Severity != "info" && n.Severity != "warning") || ids[n.ID] {
			return errors.New("invalid or duplicate public notice")
		}
		ids[n.ID] = true
	}
	for _, l := range c.HelpLinks {
		u, err := url.Parse(l.URL)
		if !plain(l.Label, 120, true) || len(l.URL) > 2048 || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || strings.ContainsFunc(l.URL, unicode.IsControl) {
			return errors.New("help links require bounded labels and credential-free HTTPS URLs")
		}
	}
	return nil
}
