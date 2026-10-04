package config

import (
	"errors"
	"path/filepath"
)

// Observability is optional and creates no public TCP or application route.
// OperatorConfigFile refers only to a separate read-only observation identity.
type Observability struct {
	SocketPath         string `json:"socketPath"`
	OperatorConfigFile string `json:"operatorConfigFile,omitempty"`
}

func (c *Observability) Validate() error {
	if c == nil {
		return nil
	}
	if !absolutePath(c.SocketPath) || len(c.SocketPath) > 100 || filepath.Dir(c.SocketPath) == string(filepath.Separator) || c.OperatorConfigFile != "" && !absolutePath(c.OperatorConfigFile) {
		return errors.New("observability requires a bounded absolute Unix socket path and optional absolute operator config path")
	}
	return nil
}
