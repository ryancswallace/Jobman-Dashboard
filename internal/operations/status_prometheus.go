package operations

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type metricFamily struct {
	name, help, kind string
	samples          []string
}

func writePrometheus(w io.Writer, s HealthSnapshot) error {
	families := []*metricFamily{}
	byName := map[string]*metricFamily{}
	add := func(name, help, kind, labels, value string) {
		f := byName[name]
		if f == nil {
			f = &metricFamily{name: "jobman_dashboard_" + name, help: help, kind: kind}
			families, byName[name] = append(families, f), f
		}
		f.samples = append(f.samples, f.name+labels+" "+value)
	}
	number := func(n float64) string { return strconv.FormatFloat(n, 'f', 3, 64) }
	stamp := func(t time.Time) string { return number(float64(t.UnixMilli()) / 1000) }
	age := func(t time.Time) string { return number(max(0, s.ObservedAt.Sub(t).Seconds())) }
	boolNumber := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	add("operator_snapshot_timestamp_seconds", "Local database snapshot time; not a live dependency probe.", "gauge", "", stamp(s.ObservedAt))
	add("notification_delivery_held", "Whether durable notification delivery is held by the operator.", "gauge", "", boolNumber(s.Hold.Held))
	add("notification_hold_generation", "Current durable delivery hold generation.", "gauge", "", s.Hold.Generation)
	if t := s.Hold.RestoreRecordedThrough; t != nil {
		add("notification_restore_cutoff_timestamp_seconds", "Original recorded times through this cutoff are suppressed after restore.", "gauge", "", stamp(*t))
	}
	backlog := func(component, deployment string, b Backlog, includeAge bool) {
		labels := `{component=` + quoteMetric(component) + `,deployment=` + quoteMetric(deployment) + `}`
		add("notification_pending", "Pending durable work at the local snapshot.", "gauge", labels, b.Pending)
		if b.OldestAt != nil && includeAge {
			add("notification_oldest_age_seconds", "Age of oldest pending durable work since local insertion.", "gauge", labels, age(*b.OldestAt))
		}
	}
	queue := func(component, deployment string, q QueueHealth, includeAge bool) {
		backlog(component, deployment, q.Backlog, includeAge)
		labels := `{component=` + quoteMetric(component) + `,deployment=` + quoteMetric(deployment) + `}`
		add("notification_due", "Pending work whose retry time has arrived and has no active lease; source and hold gates are not evaluated here.", "gauge", labels, q.Due)
		add("notification_leased", "Pending work with an unexpired database lease.", "gauge", labels, q.Leased)
	}
	queue("activation", "", s.Activation, false)
	if t := s.Activation.OldestAt; t != nil {
		add("notification_activation_rule_update_age_seconds", "Age since the oldest pending rule's current update, a lower-bound queue age proxy.", "gauge", "", age(*t))
	}
	for _, source := range s.Sources {
		labels := `{deployment=` + quoteMetric(source.DeploymentID) + `}`
		for _, state := range []string{"uninitialized", "initializing", "active", "paused"} {
			add("feed_state", "Stored feed state; active does not assert current source availability.", "gauge", strings.TrimSuffix(labels, "}")+`,state=`+quoteMetric(state)+`}`, boolNumber(source.State == state))
		}
		if source.Feed == nil {
			continue
		}
		f := source.Feed
		for _, reason := range []string{"none", "source_unavailable", "event_cursor_expired", "source_recovery_changed", "event_cursor_scope_changed", "invalid_cursor", "event_conflict", "capacity"} {
			active := f.LastError == reason || f.LastError == "" && reason == "none"
			add("feed_last_error", "Last stored safe feed error class.", "gauge", strings.TrimSuffix(labels, "}")+`,reason=`+quoteMetric(reason)+`}`, boolNumber(active))
		}
		add("feed_retained_events", "Durable retained source-event identities in this Dashboard.", "gauge", labels, f.RetainedEvents)
		add("feed_retention_seconds", "Source replay retention high-water observed by this Dashboard.", "gauge", labels, f.RetentionSeconds)
		if t := f.LastSuccessAt; t != nil {
			add("feed_last_success_age_seconds", "Age of last successful feed transaction; not a live source readiness check.", "gauge", labels, age(*t))
		}
		if t := f.PrunedRecordedThrough; t != nil {
			add("feed_pruned_recorded_through_timestamp_seconds", "Recorded-time exclusion watermark for identities already pruned locally.", "gauge", labels, stamp(*t))
		}
		if t := f.SuppressRecordedThrough; t != nil {
			add("feed_suppress_recorded_through_timestamp_seconds", "Stored source recovery suppression cutoff.", "gauge", labels, stamp(*t))
		}
		add("feed_checkpoint_present", "Whether a validated stored checkpoint is available.", "gauge", labels, boolNumber(f.Checkpoint != nil))
		if c := f.Checkpoint; c != nil {
			add("source_observation_age_seconds", "Age of the stored source checkpoint observation; not current source liveness.", "gauge", labels, age(c.AsOf))
			add("source_observed_unpublished_events", "Source unpublished backlog at the stored checkpoint observation.", "gauge", labels, c.UnpublishedEvents)
			if t := c.OldestUnpublishedAt; t != nil {
				add("source_observed_oldest_unpublished_age_seconds", "Age now of the oldest unpublished event reported in the stored checkpoint.", "gauge", labels, age(*t))
			}
		}
		backlog("events", source.DeploymentID, *source.Events, true)
		backlog("fanout", source.DeploymentID, *source.Fanout, true)
		queue("evaluation", source.DeploymentID, *source.Evaluation, true)
		queue("delivery", source.DeploymentID, *source.Delivery, true)
	}
	labels := `{component="apns"}`
	add("notification_provider_recorded_failures_total", "Shared durable provider failures recorded locally; not phone delivery failures.", "counter", labels, s.Provider.RecordedFailures)
	add("notification_provider_backoff_entries", "Shared provider identities with retry backoff still in effect.", "gauge", labels, s.Provider.BackingOff)
	if t := s.Provider.LastFailureAt; t != nil {
		add("notification_provider_last_failure_age_seconds", "Age of the latest recorded shared provider failure.", "gauge", labels, age(*t))
	}
	if t := s.Provider.RetryNotBefore; t != nil {
		add("notification_provider_backoff_remaining_seconds", "Longest remaining shared provider cooldown.", "gauge", labels, number(max(0, t.Sub(s.ObservedAt).Seconds())))
	}
	for _, f := range families {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s\n", f.name, f.help, f.name, f.kind, strings.Join(f.samples, "\n")); err != nil {
			return err
		}
	}
	return nil
}
