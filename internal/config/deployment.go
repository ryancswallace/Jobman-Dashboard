package config

import (
	"errors"
	"slices"
	"time"
	_ "time/tzdata"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

// UserDefaults initializes new accounts only; it is not an enforcement policy.
type UserDefaults struct {
	Timezone       string `json:"timezone"`
	Appearance     string `json:"appearance"`
	RefreshSeconds int64  `json:"refreshSeconds"`
}

func (c *UserDefaults) Preferences() api.Preferences {
	if c == nil {
		return api.DefaultPreferences()
	}
	return api.Preferences{Revision: "1", Timezone: c.Timezone, Appearance: c.Appearance, RefreshSeconds: int(c.RefreshSeconds)}
}
func (c *UserDefaults) Validate() error {
	if c == nil {
		return nil
	}
	_, err := time.LoadLocation(c.Timezone)
	if err != nil || c.Timezone == "" || c.Timezone == "Local" || len(c.Timezone) > 128 || !slices.Contains([]string{"system", "light", "dark"}, c.Appearance) || !slices.Contains([]int64{0, 5, 10, 30}, c.RefreshSeconds) {
		return errors.New("invalid new-user preferences")
	}
	return nil
}

// Operations contains bounded tuning, not protocol limits or authorization policy.
// Zero means the documented default; negative/out-of-range values are rejected.
type Operations struct {
	DatabaseMaxConnections        int64 `json:"databaseMaxConnections,omitempty"`
	DatabaseMinConnections        int64 `json:"databaseMinConnections,omitempty"`
	DatabaseConnectTimeoutSeconds int64 `json:"databaseConnectTimeoutSeconds,omitempty"`
	SessionLifetimeSeconds        int64 `json:"sessionLifetimeSeconds,omitempty"`
	MonitoringTimeoutMilliseconds int64 `json:"monitoringTimeoutMilliseconds,omitempty"`
}

func (c *Operations) Effective() Operations {
	v := Operations{DatabaseMaxConnections: 16, DatabaseMinConnections: 1, DatabaseConnectTimeoutSeconds: 5, SessionLifetimeSeconds: 28800, MonitoringTimeoutMilliseconds: 1500}
	if c == nil {
		return v
	}
	if c.DatabaseMaxConnections != 0 {
		v.DatabaseMaxConnections = c.DatabaseMaxConnections
	}
	if c.DatabaseMinConnections != 0 {
		v.DatabaseMinConnections = c.DatabaseMinConnections
	}
	if c.DatabaseConnectTimeoutSeconds != 0 {
		v.DatabaseConnectTimeoutSeconds = c.DatabaseConnectTimeoutSeconds
	}
	if c.SessionLifetimeSeconds != 0 {
		v.SessionLifetimeSeconds = c.SessionLifetimeSeconds
	}
	if c.MonitoringTimeoutMilliseconds != 0 {
		v.MonitoringTimeoutMilliseconds = c.MonitoringTimeoutMilliseconds
	}
	return v
}
func (c *Operations) Validate() error {
	v := c.Effective()
	if v.DatabaseMaxConnections < 1 || v.DatabaseMaxConnections > 128 || v.DatabaseMinConnections < 1 || v.DatabaseMinConnections > v.DatabaseMaxConnections || v.DatabaseConnectTimeoutSeconds < 1 || v.DatabaseConnectTimeoutSeconds > 30 || v.SessionLifetimeSeconds < 300 || v.SessionLifetimeSeconds > 28800 || v.MonitoringTimeoutMilliseconds < 100 || v.MonitoringTimeoutMilliseconds > 8000 {
		return errors.New("operational settings exceed supported bounds")
	}
	return nil
}

// Retention changes only future record expiry. Existing records retain their
// original expiry and replay/deduplication history keeps its enforced minimum.
type Retention struct {
	ReportDays int64 `json:"reportDays"`
	InboxDays  int64 `json:"inboxDays"`
}

func (c *Retention) Effective() Retention {
	if c == nil {
		return Retention{ReportDays: 30, InboxDays: 30}
	}
	return *c
}
func (c *Retention) Validate() error {
	v := c.Effective()
	if v.ReportDays < 1 || v.ReportDays > 3650 || v.InboxDays < 1 || v.InboxDays > 30 {
		return errors.New("reportDays must be 1..3650 and inboxDays 1..30; longer inbox retention requires coordinated replay journal support")
	}
	return nil
}
