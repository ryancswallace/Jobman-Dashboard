package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ContainsPath compares clean filesystem paths without accessing the filesystem.
// Runtime callers must additionally resolve aliases before applying it.
func ContainsPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// PrivateFilePaths enumerates configured secret material for the static-serving
// boundary. Public certificates and trust roots are intentionally not secrets.
func (c Config) PrivateFilePaths() map[string]string {
	paths := map[string]string{
		"serverTLS.keyFile":        c.ServerTLS.KeyFile,
		"databaseURLFile":          c.DatabaseURLFile,
		"oidc.webClientSecretFile": c.OIDC.WebClientSecretFile,
		"encryption.keyFile":       c.Encryption.KeyFile,
	}
	if c.Reports.RedactionFile != "" {
		paths["reports.redactionFile"] = c.Reports.RedactionFile
	}
	for index, source := range c.Controls {
		paths[fmt.Sprintf("controls[%d].clientKeyFile", index)] = source.ClientKeyFile
		paths[fmt.Sprintf("controls[%d].delegationKeyFile", index)] = source.DelegationKeyFile
	}
	for index, broker := range c.LogBrokers {
		paths[fmt.Sprintf("logBrokers[%d].clientKeyFile", index)] = broker.ClientKeyFile
		paths[fmt.Sprintf("logBrokers[%d].delegationKeyFile", index)] = broker.DelegationKeyFile
	}
	return paths
}

func (c Config) validatePrivatePaths() error {
	for field, path := range c.PrivateFilePaths() {
		if ContainsPath(c.WebRoot, path) {
			return fmt.Errorf("%s must be outside webRoot", field)
		}
	}
	return nil
}
