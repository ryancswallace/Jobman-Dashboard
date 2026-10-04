package config

import (
	"bytes"
	"errors"
	"io"
	"slices"
)

type WorkerComponent string

const (
	WorkerIngestion     WorkerComponent = "ingestion"
	WorkerNotifications WorkerComponent = "notifications"
	WorkerDelivery      WorkerComponent = "delivery"
	WorkerReports       WorkerComponent = "reports"
	WorkerRetention     WorkerComponent = "retention"
)

// WorkerConfig deliberately has no interactive OIDC registration, public HTTP listener,
// static root, server TLS identity or authentication master encryption key. Strict
// decoding rejects those fields before any credential is read.
type WorkerConfig struct {
	Observability         *Observability     `json:"observability,omitempty"`
	ConfigurationRevision int64              `json:"configurationRevision"`
	DatabaseURLFile       string             `json:"databaseURLFile"`
	Components            []WorkerComponent  `json:"components"`
	IdentityIssuer        string             `json:"identityIssuer,omitempty"`
	Controls              []Control          `json:"controls"`
	DeliveryHold          bool               `json:"deliveryHold,omitempty"`
	LogBrokers            []RemoteBroker     `json:"logBrokers,omitempty"`
	LogMappings           []RemoteLogMapping `json:"logMappings,omitempty"`
	LogCursorKeyFile      string             `json:"logCursorKeyFile,omitempty"`
	Reports               Reports            `json:"reports,omitempty"`
	Notifications         Notifications      `json:"notifications,omitempty"`
}

func (c WorkerConfig) Has(component WorkerComponent) bool {
	return slices.Contains(c.Components, component)
}

func LoadWorker(path string) (WorkerConfig, error) {
	data, err := ReadPublicFile(path, MaxConfigBytes)
	if err != nil {
		return WorkerConfig{}, err
	}
	return DecodeWorker(bytes.NewReader(data))
}

func DecodeWorker(r io.Reader) (WorkerConfig, error) {
	var c WorkerConfig
	if err := DecodeDocument(r, &c); err != nil {
		return WorkerConfig{}, err
	}
	if err := c.Validate(); err != nil {
		return WorkerConfig{}, err
	}
	return c, nil
}

func (c WorkerConfig) Validate() error {
	if err := c.Observability.Validate(); err != nil {
		return err
	}
	if c.ConfigurationRevision < 1 || !absolutePath(c.DatabaseURLFile) || len(c.Components) < 1 || len(c.Components) > 5 {
		return errors.New("worker requires a positive revision, private database URL path and 1–5 explicit components")
	}
	seen := map[WorkerComponent]bool{}
	for _, component := range c.Components {
		if !slices.Contains([]WorkerComponent{WorkerIngestion, WorkerNotifications, WorkerDelivery, WorkerReports, WorkerRetention}, component) || seen[component] {
			return errors.New("worker components are invalid or duplicated")
		}
		seen[component] = true
	}
	if len(c.Controls) > 0 || c.Has(WorkerIngestion) || c.Has(WorkerNotifications) || c.Has(WorkerDelivery) || c.Has(WorkerReports) {
		if err := validateControls(c.Controls); err != nil {
			return err
		}
	}
	needsIssuer := c.Has(WorkerNotifications) || c.Has(WorkerDelivery)
	if needsIssuer {
		if _, err := httpsURL(c.IdentityIssuer, false); err != nil {
			return errors.New("notification workers require the exact trusted HTTPS identity issuer")
		}
	} else if c.IdentityIssuer != "" {
		return errors.New("identityIssuer is used only by notification or delivery workers")
	}
	if err := validateRemoteLogs(c.Controls, c.LogBrokers, c.LogMappings); err != nil {
		return err
	}
	if len(c.LogBrokers) > 0 || len(c.LogMappings) > 0 || c.LogCursorKeyFile != "" {
		if !c.Has(WorkerReports) || len(c.LogBrokers) == 0 || !absolutePath(c.LogCursorKeyFile) {
			return errors.New("worker remote logs require reports and a dedicated log cursor key")
		}
	}
	if err := c.Reports.ValidateObjectAccess(); err != nil {
		return err
	}
	if c.Reports.ObjectRoot != "" {
		if !absolutePath(c.Reports.ObjectRoot) || c.Reports.ObjectRoot == "/" || !c.Has(WorkerReports) && !c.Has(WorkerRetention) {
			return errors.New("worker report root requires reports or retention and a clean private absolute path")
		}
	} else if c.Has(WorkerReports) || c.Reports.RedactionFile != "" || c.Reports.PolicyKeyFile != "" {
		return errors.New("report worker material requires a private object root")
	}
	if c.Reports.RedactionFile != "" || c.Reports.PolicyKeyFile != "" {
		if !c.Has(WorkerReports) || !absolutePath(c.Reports.RedactionFile) || !absolutePath(c.Reports.PolicyKeyFile) {
			return errors.New("report worker redaction requires an explicit private policy and dedicated policy key")
		}
	}
	if err := validateNotifications(c.Notifications, Encryption{}, Events{Enabled: true}); err != nil {
		return err
	}
	if len(c.Notifications.PreviousTokenKeys) > 0 {
		return errors.New("workers cannot load legacy authentication-derived token root keys")
	}
	if c.Has(WorkerDelivery) {
		if len(c.Notifications.APNs) == 0 || c.Notifications.TokenEncryption == nil {
			return errors.New("delivery workers require approved APNs providers and dedicated token encryption keys")
		}
	} else if len(c.Notifications.APNs) > 0 || c.Notifications.TokenEncryption != nil {
		return errors.New("provider and token encryption material is used only by delivery workers")
	}
	if len(c.Notifications.DeviceTopics) > 0 && !c.Has(WorkerNotifications) && !c.Has(WorkerDelivery) {
		return errors.New("device topic policy is used only by notification or delivery workers")
	}
	return nil
}
