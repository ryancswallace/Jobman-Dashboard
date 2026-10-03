package config

import (
	"bytes"
	"errors"
	"net"
	"strconv"
	"strings"
)

type RemoteBroker struct {
	ID                    string   `json:"id"`
	DeploymentID          string   `json:"deploymentId"`
	Origin                string   `json:"origin"`
	NamespaceIDs          []string `json:"namespaceIds"`
	TrustRootsFile        string   `json:"trustRootsFile"`
	ClientCertificateFile string   `json:"clientCertificateFile"`
	ClientKeyFile         string   `json:"clientKeyFile"`
	DelegationKeyFile     string   `json:"delegationKeyFile"`
	DelegationKeyID       string   `json:"delegationKeyId"`
	ServiceID             string   `json:"serviceId"`
	Audience              string   `json:"audience"`
}
type RemoteLogMapping struct {
	BrokerID           string `json:"brokerId"`
	DeploymentID       string `json:"deploymentId"`
	TargetGenerationID string `json:"targetGenerationId"`
	StoreName          string `json:"storeName"`
	StoreVersion       string `json:"storeVersion"`
}
type LogRoot struct {
	DeploymentID       string `json:"deploymentId"`
	TargetGenerationID string `json:"targetGenerationId"`
	StoreName          string `json:"storeName"`
	StoreVersion       string `json:"storeVersion"`
	Root               string `json:"root"`
}
type BrokerRegistration struct {
	KeyID                 string   `json:"keyId"`
	ServiceID             string   `json:"serviceId"`
	Audience              string   `json:"audience"`
	DeploymentID          string   `json:"deploymentId"`
	ClientCertificateFile string   `json:"clientCertificateFile"`
	PublicKeyFile         string   `json:"publicKeyFile"`
	NamespaceIDs          []string `json:"namespaceIds"`
}
type BrokerConfig struct {
	ConfigurationRevision     int64                `json:"configurationRevision"`
	PublicOrigin              string               `json:"publicOrigin"`
	Listen                    string               `json:"listen"`
	ServerTLS                 ServerTLS            `json:"serverTLS"`
	ClientTrustRootsFile      string               `json:"clientTrustRootsFile"`
	StateDirectory            string               `json:"stateDirectory"`
	Controls                  []Control            `json:"controls"`
	Services                  []BrokerRegistration `json:"services"`
	LogRoots                  []LogRoot            `json:"logRoots"`
	ReaderConcurrency         int64                `json:"readerConcurrency"`
	ReaderTimeoutMilliseconds int64                `json:"readerTimeoutMilliseconds"`
}

func LoadBroker(path string) (BrokerConfig, error) {
	var c BrokerConfig
	data, err := ReadPublicFile(path, MaxConfigBytes)
	if err != nil {
		return c, err
	}
	if err = DecodeDocument(bytes.NewReader(data), &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}
func (c BrokerConfig) Validate() error {
	if c.ConfigurationRevision < 1 || c.ReaderConcurrency < 1 || c.ReaderConcurrency > 16 || c.ReaderTimeoutMilliseconds < 1 || c.ReaderTimeoutMilliseconds > 10000 {
		return errors.New("invalid broker revision or bounded reader settings")
	}
	if _, err := httpsURL(c.PublicOrigin, true); err != nil {
		return errors.New("broker publicOrigin must be an HTTPS origin")
	}
	host, port, err := net.SplitHostPort(c.Listen)
	number, pe := strconv.Atoi(port)
	if err != nil || pe != nil || number < 1 || number > 65535 || len(host) > 253 || strings.ContainsAny(host, " /\\?#@\t\r\n") {
		return errors.New("invalid broker listen address")
	}
	for _, path := range []string{c.ServerTLS.CertificateFile, c.ServerTLS.KeyFile, c.ClientTrustRootsFile, c.StateDirectory} {
		if !absolutePath(path) {
			return errors.New("broker paths must be clean and absolute")
		}
	}
	if err := validateControls(c.Controls); err != nil {
		return err
	}
	sources := map[string]Control{}
	for _, source := range c.Controls {
		sources[source.ID] = source
	}
	if len(c.Services) < 1 || len(c.Services) > 64 || len(c.LogRoots) < 1 || len(c.LogRoots) > 1024 {
		return errors.New("broker requires bounded service registrations and log roots")
	}
	keys := map[string]bool{}
	for _, s := range c.Services {
		if keys[s.KeyID] || !text(s.KeyID, 128) || !text(s.ServiceID, 256) || !text(s.Audience, 256) || sources[s.DeploymentID].ID == "" || !absolutePath(s.ClientCertificateFile) || !absolutePath(s.PublicKeyFile) || !allowedNamespaces(s.NamespaceIDs, sources[s.DeploymentID].NamespaceIDs) {
			return errors.New("invalid broker service trust or namespace subset")
		}
		keys[s.KeyID] = true
	}
	mappings := map[string]bool{}
	for _, m := range c.LogRoots {
		key := m.DeploymentID + "/" + m.TargetGenerationID + "/" + m.StoreName + "/" + m.StoreVersion
		if sources[m.DeploymentID].ID == "" || !mappingParts(m.TargetGenerationID, m.StoreName, m.StoreVersion) || !absolutePath(m.Root) || mappings[key] {
			return errors.New("invalid or duplicate broker root mapping")
		}
		mappings[key] = true
	}
	return nil
}
func validateRemoteLogs(controls []Control, brokers []RemoteBroker, mappings []RemoteLogMapping) error {
	if len(brokers) == 0 && len(mappings) == 0 {
		return nil
	}
	if len(brokers) < 1 || len(brokers) > 64 || len(mappings) < 1 || len(mappings) > 1024 {
		return errors.New("configure bounded log brokers and matching store mappings")
	}
	sources := map[string]Control{}
	for _, c := range controls {
		sources[c.ID] = c
	}
	byID := map[string]RemoteBroker{}
	for _, b := range brokers {
		if !text(b.ID, 128) || byID[b.ID].ID != "" || sources[b.DeploymentID].ID == "" || !text(b.DelegationKeyID, 128) || !text(b.ServiceID, 256) || !text(b.Audience, 256) || !allowedNamespaces(b.NamespaceIDs, sources[b.DeploymentID].NamespaceIDs) {
			return errors.New("invalid log broker identity or namespace subset")
		}
		if _, err := httpsURL(b.Origin, true); err != nil {
			return errors.New("log broker origin must be HTTPS")
		}
		for _, path := range []string{b.TrustRootsFile, b.ClientCertificateFile, b.ClientKeyFile, b.DelegationKeyFile} {
			if !absolutePath(path) {
				return errors.New("log broker key/trust paths must be absolute")
			}
		}
		byID[b.ID] = b
	}
	seen := map[string]bool{}
	for _, m := range mappings {
		key := m.DeploymentID + "/" + m.TargetGenerationID + "/" + m.StoreName + "/" + m.StoreVersion
		if byID[m.BrokerID].ID == "" || byID[m.BrokerID].DeploymentID != m.DeploymentID || !mappingParts(m.TargetGenerationID, m.StoreName, m.StoreVersion) || seen[key] {
			return errors.New("invalid or duplicate remote log mapping")
		}
		seen[key] = true
	}
	return nil
}
func allowedNamespaces(want, allowed []string) bool {
	if len(want) < 1 || len(want) > 320 {
		return false
	}
	set := map[string]bool{}
	for _, id := range allowed {
		set[id] = true
	}
	seen := map[string]bool{}
	for _, id := range want {
		if !set[id] || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func mappingParts(generation, store, version string) bool {
	if generation != "" && !uuid(generation) {
		return false
	}
	if len(store) < 1 || len(store) > 64 || store == "." || store == ".." {
		return false
	}
	for _, c := range store {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789._-", c) {
			return false
		}
	}
	n, err := strconv.ParseInt(version, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == version
}
