-- Source ingestion is independent of user authorization and delivery. The
-- stored service feed is never exposed directly to web/native callers.
CREATE TABLE dashboard_event_feeds (
  deployment_id uuid PRIMARY KEY,
  namespace_ids uuid[] NOT NULL CHECK(cardinality(namespace_ids) BETWEEN 1 AND 320),
  status text NOT NULL CHECK(status IN ('initializing','active','paused')),
  generation bigint NOT NULL DEFAULT 1 CHECK(generation>0),
  checkpoint bytea CHECK(octet_length(checkpoint) BETWEEN 1 AND 32768),
  cursor text NOT NULL DEFAULT '' CHECK(octet_length(cursor)<=1024),
  last_position bigint NOT NULL DEFAULT 0 CHECK(last_position>=0),
  lease_token uuid,
  lease_expires_at timestamptz,
  last_success_at timestamptz,
  last_error text NOT NULL DEFAULT '' CHECK(last_error IN ('','source_unavailable','event_cursor_expired','source_recovery_changed','event_cursor_scope_changed','invalid_cursor','event_conflict','capacity')),
  retained_count bigint NOT NULL DEFAULT 0 CHECK(retained_count BETWEEN 0 AND 1000000),
  retention_seconds bigint NOT NULL DEFAULT 86400 CHECK(retention_seconds BETWEEN 86400 AND 31536000),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK((lease_token IS NULL)=(lease_expires_at IS NULL)),
  CHECK(status<>'active' OR (checkpoint IS NOT NULL AND cursor<>''))
);

CREATE TABLE dashboard_source_events (
  deployment_id uuid NOT NULL REFERENCES dashboard_event_feeds(deployment_id),
  control_instance_id uuid NOT NULL,
  event_id uuid NOT NULL,
  namespace_id uuid NOT NULL,
  job_id uuid NOT NULL,
  recorded_at timestamptz NOT NULL,
  payload bytea NOT NULL CHECK(octet_length(payload) BETWEEN 1 AND 4096),
  fact_digest bytea NOT NULL CHECK(octet_length(fact_digest)=32),
  first_seen_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  last_seen_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  expires_at timestamptz NOT NULL,
  processed_at timestamptz,
  PRIMARY KEY(deployment_id,control_instance_id,event_id)
);
CREATE INDEX dashboard_source_events_pending ON dashboard_source_events(first_seen_at,deployment_id,control_instance_id,event_id) WHERE processed_at IS NULL;
CREATE INDEX dashboard_source_events_scope ON dashboard_source_events(deployment_id,namespace_id,recorded_at,event_id);
CREATE INDEX dashboard_source_events_retention ON dashboard_source_events(deployment_id,last_seen_at) WHERE processed_at IS NOT NULL;

-- One open gap per source. Recovery must retain the original event identities
-- and record an operator-approved reconciliation receipt before resuming.
CREATE TABLE dashboard_event_gaps (
  id uuid PRIMARY KEY,
  deployment_id uuid NOT NULL REFERENCES dashboard_event_feeds(deployment_id),
  reason text NOT NULL CHECK(reason IN ('event_cursor_expired','source_recovery_changed','event_cursor_scope_changed','invalid_cursor','event_conflict','capacity')),
  prior_checkpoint bytea CHECK(octet_length(prior_checkpoint) BETWEEN 1 AND 32768),
  prior_cursor text NOT NULL CHECK(octet_length(prior_cursor)<=1024),
  detected_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  resolved_at timestamptz,
  recovery_receipt bytea CHECK(octet_length(recovery_receipt) BETWEEN 1 AND 8192),
  CHECK((resolved_at IS NULL)=(recovery_receipt IS NULL))
);
CREATE UNIQUE INDEX dashboard_event_gaps_open ON dashboard_event_gaps(deployment_id) WHERE resolved_at IS NULL;
CREATE INDEX dashboard_event_gaps_retention ON dashboard_event_gaps(resolved_at) WHERE resolved_at IS NOT NULL;
