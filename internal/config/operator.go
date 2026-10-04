package config

import (
	"bytes"
	"errors"
	"io"
	"unicode/utf8"
)

// OperatorConfig is read-only observation configuration, not a runtime service
// identity. It contains no source endpoint, issuer, listener or signing material.
// Its schema version does not advance a source's configuration identity fence.
type OperatorConfig struct {
	SchemaVersion   int64                `json:"schemaVersion"`
	DatabaseURLFile string               `json:"databaseURLFile"`
	Deployments     []OperatorDeployment `json:"deployments"`
}
type OperatorDeployment struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

func LoadOperator(path string) (OperatorConfig, error) {
	data, err := ReadPublicFile(path, MaxConfigBytes)
	if err != nil {
		return OperatorConfig{}, err
	}
	return DecodeOperator(bytes.NewReader(data))
}
func DecodeOperator(reader io.Reader) (OperatorConfig, error) {
	var c OperatorConfig
	if err := DecodeDocument(reader, &c); err != nil {
		return OperatorConfig{}, err
	}
	if err := c.Validate(); err != nil {
		return OperatorConfig{}, err
	}
	if c.Deployments == nil {
		c.Deployments = []OperatorDeployment{}
	}
	return c, nil
}
func (c OperatorConfig) Validate() error {
	if c.SchemaVersion != 1 || !absolutePath(c.DatabaseURLFile) || len(c.Deployments) > 32 {
		return errors.New("operator configuration requires schemaVersion1, a private database URL path and at most32 deployments")
	}
	seen := map[string]bool{}
	for _, d := range c.Deployments {
		if !uuid(d.ID) || seen[d.ID] || !utf8.ValidString(d.Name) || d.Name != "" && !text(d.Name, 200) {
			return errors.New("operator deployments require unique canonical UUIDs and optional bounded display names")
		}
		seen[d.ID] = true
	}
	return nil
}
