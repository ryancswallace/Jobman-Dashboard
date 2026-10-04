// Package observability contains bounded, content-free process observations.
// No method accepts a URL, user/job identifier, token, cursor or raw error text.
package observability

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maximumMeasurements = 2048

var buckets = []float64{.01, .05, .25, 1, 5, 15, 60}
var outcomes = []string{"ok", "denied", "unavailable", "invalid", "conflict", "rate_limited", "cancelled", "failed", "accepted", "retry", "suppressed", "expired", "unregistered", "invalid_token", "provider_error", "chunk_missing", "chunk_corrupt", "sequence_gap", "mapping_inaccessible", "reader_timeout", "reader_busy", "read_timeout", "reader_unavailable", "invalid_manifest", "open", "complete", "more", "not_captured"}
var operationsByComponent = map[string][]string{
	"http":   {"api", "auth", "static", "other"},
	"source": {"capabilities", "namespace", "jobs", "summary", "groups", "targets", "logs", "artifacts", "evidence", "events"},
	"logs":   {"read", "chunk"}, "report": {"task"}, "object": {"read", "publish", "recover", "remove", "sweep"}, "delivery": {"attempt", "recorded_to_acceptance"}, "retention": {"authentication", "browse", "notification_delivery", "notifications", "diagnosis", "source_events"},
}

type measurementKey struct{ component, operation, deployment, mode string }
type measurement struct {
	count       uint64
	sum         float64
	buckets     [7]uint64
	outcomes    map[string]uint64
	last        time.Time
	lastOutcome string
	bytes       uint64
}
type authority struct{ observed, verified, expires time.Time }
type Build struct{ Version, Revision, GoVersion, OS, Architecture string }
type Registry struct {
	mu          sync.Mutex
	role        string
	revision    int64
	build       Build
	started     time.Time
	ids         map[string]bool
	values      map[measurementKey]*measurement
	authorities map[string]authority
	mismatches  map[string]uint64
	dropped     uint64
	gauges      func() map[string]float64
}

func NewRegistry(role string, revision int64, ids []string, build Build) (*Registry, error) {
	if !slices.Contains([]string{"api", "serve", "worker", "broker"}, role) || revision < 1 || len(ids) > 32 {
		return nil, fmt.Errorf("invalid process observation configuration")
	}
	r := &Registry{role: role, revision: revision, build: build, started: time.Now().UTC(), ids: map[string]bool{}, values: map[measurementKey]*measurement{}, authorities: map[string]authority{}, mismatches: map[string]uint64{}}
	for _, id := range ids {
		if !canonicalUUID(id) || r.ids[id] {
			return nil, fmt.Errorf("invalid process observation source set")
		}
		r.ids[id] = true
	}
	for _, v := range []string{build.Version, build.Revision, build.GoVersion, build.OS, build.Architecture} {
		if len(v) > 128 || strings.ContainsFunc(v, func(c rune) bool {
			return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-_+", c)
		}) {
			return nil, fmt.Errorf("invalid process build metadata")
		}
	}
	return r, nil
}
func canonicalUUID(s string) bool {
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range s {
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
func (r *Registry) Observe(component, operation, deployment, mode, outcome string, elapsed time.Duration) {
	r.ObserveBytes(component, operation, deployment, mode, outcome, elapsed, 0)
}
func (r *Registry) ObserveBytes(component, operation, deployment, mode, outcome string, elapsed time.Duration, size int64) {
	if r == nil {
		return
	}
	if !slices.Contains(operationsByComponent[component], operation) || deployment != "" && !r.ids[deployment] || !slices.Contains([]string{"", "interactive", "worker", "service"}, mode) || !slices.Contains(outcomes, outcome) || elapsed < 0 || size < 0 {
		return
	}
	key := measurementKey{component, operation, deployment, mode}
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.values[key]
	if m == nil {
		if len(r.values) >= maximumMeasurements {
			r.dropped++
			return
		}
		m = &measurement{outcomes: map[string]uint64{}}
		r.values[key] = m
	}
	seconds := elapsed.Seconds()
	m.count++
	m.sum += seconds
	m.bytes += uint64(size)
	for i, b := range buckets {
		if seconds <= b {
			m.buckets[i]++
		}
	}
	m.outcomes[outcome]++
	m.last = time.Now().UTC()
	m.lastOutcome = outcome
}

// ObserveAuthority records only a fully validated discovery's oldest directory
// proof and earliest expiry. It is a recent read observation, not AD-change lag.
func (r *Registry) ObserveAuthority(deployment string, verified, expires time.Time) {
	if r == nil || !r.ids[deployment] || verified.IsZero() || expires.IsZero() || expires.Before(verified) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authorities[deployment] = authority{time.Now().UTC(), verified, expires}
}
func (r *Registry) Mismatch(kind string) {
	if r == nil || !slices.Contains([]string{"schema", "source_identity", "source_contract"}, kind) {
		return
	}
	r.mu.Lock()
	r.mismatches[kind]++
	r.mu.Unlock()
}

// SetPoolGauges is called once during startup. Only fixed pool-stat names render.
func (r *Registry) SetPoolGauges(fn func() map[string]float64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.gauges = fn
	r.mu.Unlock()
}
func quote(s string) string { return strconv.Quote(s) }
func labels(k measurementKey) string {
	return `component=` + quote(k.component) + `,operation=` + quote(k.operation) + `,deployment=` + quote(k.deployment) + `,mode=` + quote(k.mode)
}
func (r *Registry) Render() []byte {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var b bytes.Buffer
	fmt.Fprintf(&b, "# TYPE jobman_dashboard_process_info gauge\njobman_dashboard_process_info{role=%s,version=%s,revision=%s,go=%s,os=%s,architecture=%s} 1\n", quote(r.role), quote(r.build.Version), quote(r.build.Revision), quote(r.build.GoVersion), quote(r.build.OS), quote(r.build.Architecture))
	fmt.Fprintf(&b, "# TYPE jobman_dashboard_configuration_revision gauge\njobman_dashboard_configuration_revision %d\n# TYPE jobman_dashboard_process_start_timestamp_seconds gauge\njobman_dashboard_process_start_timestamp_seconds %.3f\n", r.revision, float64(r.started.UnixMilli())/1000)
	b.WriteString("# HELP jobman_dashboard_observed_operations_total Process-local completed observations; no current dependency availability is implied.\n# TYPE jobman_dashboard_observed_operations_total counter\n# TYPE jobman_dashboard_operation_duration_seconds histogram\n# TYPE jobman_dashboard_observed_bytes_total counter\n# TYPE jobman_dashboard_last_observation_timestamp_seconds gauge\n")
	keys := make([]measurementKey, 0, len(r.values))
	for k := range r.values {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return labels(keys[i]) < labels(keys[j]) })
	for _, k := range keys {
		m := r.values[k]
		l := labels(k)
		for _, outcome := range outcomes {
			if n := m.outcomes[outcome]; n > 0 {
				fmt.Fprintf(&b, "jobman_dashboard_observed_operations_total{%s,outcome=%s} %d\n", l, quote(outcome), n)
			}
		}
		for i, limit := range buckets {
			fmt.Fprintf(&b, "jobman_dashboard_operation_duration_seconds_bucket{%s,le=%s} %d\n", l, quote(strconv.FormatFloat(limit, 'g', -1, 64)), m.buckets[i])
		}
		fmt.Fprintf(&b, "jobman_dashboard_operation_duration_seconds_bucket{%s,le=\"+Inf\"} %d\njobman_dashboard_operation_duration_seconds_count{%s} %d\njobman_dashboard_operation_duration_seconds_sum{%s} %g\n", l, m.count, l, m.count, l, m.sum)
		fmt.Fprintf(&b, "jobman_dashboard_observed_bytes_total{%s} %d\njobman_dashboard_last_observation_timestamp_seconds{%s,outcome=%s} %.3f\n", l, m.bytes, l, quote(m.lastOutcome), float64(m.last.UnixMilli())/1000)
	}
	b.WriteString("# HELP jobman_dashboard_authorization_proof_age_seconds Age now of the oldest directory proof in the last successful discovery; not AD revocation lag.\n# TYPE jobman_dashboard_authorization_proof_age_seconds gauge\n# TYPE jobman_dashboard_authorization_observation_timestamp_seconds gauge\n# TYPE jobman_dashboard_authorization_proof_expiry_timestamp_seconds gauge\n")
	ids := make([]string, 0, len(r.authorities))
	for id := range r.authorities {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := r.authorities[id]
		fmt.Fprintf(&b, "jobman_dashboard_authorization_proof_age_seconds{deployment=%s} %g\njobman_dashboard_authorization_observation_timestamp_seconds{deployment=%s} %.3f\njobman_dashboard_authorization_proof_expiry_timestamp_seconds{deployment=%s} %.3f\n", quote(id), max(0, time.Since(a.verified).Seconds()), quote(id), float64(a.observed.UnixMilli())/1000, quote(id), float64(a.expires.UnixMilli())/1000)
	}
	b.WriteString("# TYPE jobman_dashboard_configuration_mismatches_total counter\n")
	for _, kind := range []string{"schema", "source_identity", "source_contract"} {
		fmt.Fprintf(&b, "jobman_dashboard_configuration_mismatches_total{kind=%s} %d\n", quote(kind), r.mismatches[kind])
	}
	fmt.Fprintf(&b, "# TYPE jobman_dashboard_observation_capacity_dropped_total counter\njobman_dashboard_observation_capacity_dropped_total %d\n", r.dropped)
	if r.gauges != nil {
		v := r.gauges()
		for _, name := range []string{"acquired", "idle", "total", "maximum", "acquire_count", "acquire_wait_seconds", "empty_acquire_count", "cancelled_acquire_count"} {
			if n, ok := v[name]; ok && n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0) {
				fmt.Fprintf(&b, "# TYPE jobman_dashboard_database_pool_%s gauge\njobman_dashboard_database_pool_%s %g\n", name, name, n)
			}
		}
	}
	return b.Bytes()
}
