// Package config decodes deployment configuration without reading credentials,
// changing process state, contacting providers, or enabling fixture fallback.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"unicode"
)

const MaxConfigBytes int64 = 256 << 10
const MaxSecretBytes int64 = 1 << 20

type Config struct {
	ConfigurationRevision int64      `json:"configurationRevision"`
	PublicOrigin          string     `json:"publicOrigin"`
	Listen                string     `json:"listen"`
	WebRoot               string     `json:"webRoot"`
	ServerTLS             ServerTLS  `json:"serverTLS"`
	DatabaseURLFile       string     `json:"databaseURLFile"`
	OIDC                  OIDC       `json:"oidc"`
	Encryption            Encryption `json:"encryption"`
	Controls              []Control  `json:"controls"`
}

type ServerTLS struct {
	CertificateFile string `json:"certificateFile"`
	KeyFile         string `json:"keyFile"`
}

type OIDC struct {
	Issuer              string   `json:"issuer"`
	APIAudience         string   `json:"apiAudience"`
	WebClientID         string   `json:"webClientId"`
	WebClientSecretFile string   `json:"webClientSecretFile"`
	NativeClientID      string   `json:"nativeClientId"`
	NativeRedirectURI   string   `json:"nativeRedirectURI"`
	Scopes              []string `json:"scopes"`
	DirectoryIDClaim    string   `json:"directoryIdClaim"`
	ClientIDClaim       string   `json:"clientIdClaim"`
	TrustRootsFile      string   `json:"trustRootsFile"`
}

type Encryption struct {
	KeyFile string `json:"keyFile"`
	KeyID   string `json:"keyId"`
}

type Control struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Origin                string   `json:"origin"`
	ExpectedInstanceID    string   `json:"expectedInstanceId"`
	NamespaceIDs          []string `json:"namespaceIds"`
	TrustRootsFile        string   `json:"trustRootsFile"`
	ClientCertificateFile string   `json:"clientCertificateFile"`
	ClientKeyFile         string   `json:"clientKeyFile"`
	DelegationKeyFile     string   `json:"delegationKeyFile"`
	DelegationKeyID       string   `json:"delegationKeyId"`
	ServiceID             string   `json:"serviceId"`
	Audience              string   `json:"audience"`
}

// Load reads a bounded regular config file. Credential paths are validated but
// are not opened; callers deliberately load each credential with ReadSecret.
func Load(path string) (Config, error) {
	f, err := openRegular(path, false)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration: %w", err)
	}
	defer f.Close()
	return Decode(f)
}

// Decode rejects unknown keys, duplicate keys (including nested objects), nulls,
// noncanonical key casing, trailing documents and oversized configuration.
func Decode(reader io.Reader) (Config, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxConfigBytes+1))
	if err != nil || len(data) > int(MaxConfigBytes) {
		return Config{}, errors.New("configuration is unreadable or exceeds 256 KiB")
	}
	checker := json.NewDecoder(bytes.NewReader(data))
	checker.UseNumber()
	if err := checkKeys(checker, reflect.TypeFor[Config](), "configuration"); err != nil {
		return Config{}, err
	}
	if _, err := checker.Token(); err != io.EOF {
		return Config{}, errors.New("configuration must contain exactly one JSON object")
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		// Do not include parser excerpts: they could contain an accidentally
		// pasted inline credential even though that field is rejected.
		return Config{}, errors.New("configuration has an invalid JSON value")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func checkKeys(decoder *json.Decoder, schema reflect.Type, path string) error {
	token, err := decoder.Token()
	if err != nil || token == nil {
		return fmt.Errorf("%s must not be null and must match its JSON type", path)
	}
	switch schema.Kind() {
	case reflect.Struct:
		if token != json.Delim('{') {
			return fmt.Errorf("%s must be an object", path)
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < schema.NumField(); i++ {
			field := schema.Field(i)
			fields[field.Tag.Get("json")] = field.Type
		}
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			field, exists := fields[key]
			if err != nil || !ok || !exists || seen[key] {
				return fmt.Errorf("%s contains an unknown or duplicate field", path)
			}
			seen[key] = true
			if err := checkKeys(decoder, field, path+"."+key); err != nil {
				return err
			}
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
			return fmt.Errorf("%s is not a complete object", path)
		}
	case reflect.Slice:
		if token != json.Delim('[') {
			return fmt.Errorf("%s must be an array", path)
		}
		for decoder.More() {
			if err := checkKeys(decoder, schema.Elem(), path+"[]"); err != nil {
				return err
			}
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
			return fmt.Errorf("%s is not a complete array", path)
		}
	case reflect.String:
		if _, ok := token.(string); !ok {
			return fmt.Errorf("%s must be a string", path)
		}
	case reflect.Int64:
		value, ok := token.(json.Number)
		if !ok {
			return fmt.Errorf("%s must be a signed 64-bit integer", path)
		}
		if _, err := strconv.ParseInt(string(value), 10, 64); err != nil {
			return fmt.Errorf("%s must be a signed 64-bit integer", path)
		}
	default:
		return errors.New("configuration schema contains an unsupported type")
	}
	return nil
}

func (c Config) Validate() error {
	if c.ConfigurationRevision < 1 {
		return errors.New("configurationRevision must be a positive signed 64-bit integer")
	}
	if _, err := httpsURL(c.PublicOrigin, true); err != nil {
		return errors.New("publicOrigin must be an HTTPS origin without credentials, query, fragment or path")
	}
	host, port, err := net.SplitHostPort(c.Listen)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 || len(host) > 253 || strings.ContainsAny(host, " /\\?#@\t\r\n") {
		return errors.New("listen must be a host:port bind address with a numeric port")
	}
	for field, path := range map[string]string{
		"webRoot": c.WebRoot, "serverTLS.certificateFile": c.ServerTLS.CertificateFile,
		"serverTLS.keyFile": c.ServerTLS.KeyFile, "databaseURLFile": c.DatabaseURLFile,
		"oidc.webClientSecretFile": c.OIDC.WebClientSecretFile, "oidc.trustRootsFile": c.OIDC.TrustRootsFile,
		"encryption.keyFile": c.Encryption.KeyFile,
	} {
		if !absolutePath(path) {
			return fmt.Errorf("%s must be a clean absolute file-system path", field)
		}
	}
	if _, err := httpsURL(c.OIDC.Issuer, false); err != nil {
		return errors.New("oidc.issuer must be an HTTPS URL without credentials, query or fragment")
	}
	for field, value := range map[string]string{
		"oidc.apiAudience": c.OIDC.APIAudience, "oidc.webClientId": c.OIDC.WebClientID,
		"oidc.nativeClientId": c.OIDC.NativeClientID, "oidc.directoryIdClaim": c.OIDC.DirectoryIDClaim,
		"oidc.clientIdClaim": c.OIDC.ClientIDClaim,
	} {
		if !text(value, 512) {
			return fmt.Errorf("%s must be a nonempty identifier of at most 512 bytes", field)
		}
	}
	if c.OIDC.WebClientID == c.OIDC.NativeClientID {
		return errors.New("web and native OIDC registrations must have distinct client IDs")
	}
	if c.OIDC.APIAudience == c.OIDC.WebClientID || c.OIDC.APIAudience == c.OIDC.NativeClientID {
		return errors.New("OIDC API audience must be distinct from web and native client IDs")
	}
	if !text(c.Encryption.KeyID, 64) {
		return errors.New("encryption.keyId must be a nonempty identifier of at most 64 bytes")
	}
	if c.OIDC.NativeRedirectURI != "jobman-dashboard-auth://callback" {
		return errors.New("oidc.nativeRedirectURI must match the registered native callback jobman-dashboard-auth://callback")
	}
	if len(c.OIDC.Scopes) == 0 || len(c.OIDC.Scopes) > 32 {
		return errors.New("oidc.scopes requires 1 to 32 unique scopes including openid")
	}
	scopes := map[string]bool{}
	for _, scope := range c.OIDC.Scopes {
		if !text(scope, 256) || strings.ContainsFunc(scope, unicode.IsSpace) || scopes[scope] {
			return errors.New("oidc.scopes contains an invalid or duplicate scope")
		}
		scopes[scope] = true
	}
	if !scopes["openid"] {
		return errors.New("oidc.scopes must include openid")
	}
	if len(c.Controls) == 0 || len(c.Controls) > 32 {
		return errors.New("controls requires 1 to 32 explicitly configured sources")
	}
	ids, instances, origins := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for index, control := range c.Controls {
		if !uuid(control.ID) || !uuid(control.ExpectedInstanceID) || ids[control.ID] || instances[control.ExpectedInstanceID] {
			return fmt.Errorf("controls[%d] requires unique immutable source and instance UUIDs", index)
		}
		ids[control.ID], instances[control.ExpectedInstanceID] = true, true
		origin, err := httpsURL(control.Origin, true)
		if err != nil || origins[origin] {
			return fmt.Errorf("controls[%d].origin must be a unique HTTPS origin without credentials, query, fragment or path", index)
		}
		origins[origin] = true
		if !text(control.Name, 120) || !text(control.ServiceID, 512) || !text(control.Audience, 512) || !text(control.DelegationKeyID, 128) {
			return fmt.Errorf("controls[%d] requires bounded name, serviceId, audience and delegationKeyId values", index)
		}
		if len(control.NamespaceIDs) == 0 || len(control.NamespaceIDs) > 320 {
			return fmt.Errorf("controls[%d].namespaceIds requires 1 to 320 unique namespace UUIDs", index)
		}
		namespaces := map[string]bool{}
		for _, id := range control.NamespaceIDs {
			if !uuid(id) || namespaces[id] {
				return fmt.Errorf("controls[%d].namespaceIds contains an invalid or duplicate UUID", index)
			}
			namespaces[id] = true
		}
		for field, path := range map[string]string{
			"trustRootsFile": control.TrustRootsFile, "clientCertificateFile": control.ClientCertificateFile,
			"clientKeyFile": control.ClientKeyFile, "delegationKeyFile": control.DelegationKeyFile,
		} {
			if !absolutePath(path) {
				return fmt.Errorf("controls[%d].%s must be a clean absolute file-system path", index, field)
			}
		}
	}
	return nil
}

func httpsURL(value string, originOnly bool) (string, error) {
	u, err := url.Parse(value)
	if err != nil || len(value) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(value, "#") || strings.HasSuffix(u.Host, ":") || strings.ContainsAny(u.Host, "\\% \t\r\n") || (originOnly && u.Path != "" && u.Path != "/") {
		return "", errors.New("invalid HTTPS URL")
	}
	port := u.Port()
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", errors.New("invalid HTTPS port")
		}
	}
	// Equivalent origins must not register one service more than once.
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	if host == "" {
		return "", errors.New("invalid HTTPS host")
	}
	if port == "" || port == "443" {
		port = "443"
	}
	return "https://" + net.JoinHostPort(host, port), nil
}

func absolutePath(value string) bool {
	return len(value) > 1 && len(value) <= 4096 && filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

func text(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

func uuid(value string) bool {
	if len(value) != 36 || value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// ReadSecret does not trim or transform bytes. In particular, encryption and
// signing keys are not silently changed. Text-credential parsing belongs to the
// caller. Symlinks, nonregular files and group/other permissions are rejected.
func ReadSecret(path string, maxBytes int64) ([]byte, error) {
	return readFile(path, maxBytes, true)
}

// ReadPublicFile is the bounded regular-file loader for public certificates and
// trust roots. It permits public read bits but retains no-follow and byte limits.
func ReadPublicFile(path string, maxBytes int64) ([]byte, error) {
	return readFile(path, maxBytes, false)
}

func readFile(path string, maxBytes int64, private bool) ([]byte, error) {
	if maxBytes < 1 || maxBytes > MaxSecretBytes {
		return nil, errors.New("file read limit must be between 1 byte and 1 MiB")
	}
	f, err := openRegular(path, private)
	if err != nil {
		return nil, fmt.Errorf("open data file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() < 1 || info.Size() > maxBytes {
		return nil, errors.New("data file is empty, unreadable or exceeds the configured limit")
	}
	value, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || len(value) == 0 || int64(len(value)) > maxBytes {
		return nil, errors.New("data file is empty, unreadable or exceeds the configured limit")
	}
	return value, nil
}

// Kept separate from decoding so a rejected document cannot cause credential
// file access. openNoFollow also avoids blocking on a substituted FIFO.
func openRegular(path string, private bool) (*os.File, error) {
	if !absolutePath(path) {
		return nil, errors.New("file path must be clean and absolute")
	}
	f, err := openNoFollow(path)
	if err != nil {
		return nil, errors.New("file is not accessible as a regular non-symlink file")
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || (private && info.Mode().Perm()&0077 != 0) {
		f.Close()
		return nil, errors.New("file must be regular and secret files must deny group and other access")
	}
	return f, nil
}
