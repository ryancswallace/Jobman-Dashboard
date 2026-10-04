-- Explicit operator recovery never turns current terminal snapshots into events.
-- The normal feed remains paused while the private recovery journal advances.
ALTER TABLE dashboard_event_feeds ADD COLUMN pruned_recorded_through timestamptz;
ALTER TABLE dashboard_event_feeds ADD COLUMN suppress_recorded_through timestamptz;
ALTER TABLE dashboard_source_events ADD COLUMN notification_suppressed boolean NOT NULL DEFAULT false;

CREATE TABLE dashboard_event_recoveries (
  id uuid PRIMARY KEY,
  gap_id uuid NOT NULL REFERENCES dashboard_event_gaps(id),
  deployment_id uuid NOT NULL REFERENCES dashboard_event_feeds(deployment_id),
  plan bytea NOT NULL CHECK(octet_length(plan) BETWEEN 1 AND 131072),
  status text NOT NULL CHECK(status IN ('replaying','ready','applied','superseded','quarantined')),
  revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
  cursor text NOT NULL CHECK(octet_length(cursor) BETWEEN 1 AND 1024),
  last_position bigint NOT NULL DEFAULT 0 CHECK(last_position>=0),
  checkpoint bytea NOT NULL CHECK(octet_length(checkpoint) BETWEEN 1 AND 32768),
  pages bigint NOT NULL DEFAULT 0 CHECK(pages BETWEEN 0 AND 10000),
  scanned bigint NOT NULL DEFAULT 0 CHECK(scanned BETWEEN 0 AND 2000000),
  added bigint NOT NULL DEFAULT 0 CHECK(added BETWEEN 0 AND 1000000 AND added<=scanned),
  lease_token uuid,
  lease_expires_at timestamptz,
  reconciliation_receipt bytea CHECK(octet_length(reconciliation_receipt) BETWEEN 1 AND 131072),
  scope_removal_checked boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK((lease_token IS NULL)=(lease_expires_at IS NULL)),
  CHECK(status<>'applied' OR (reconciliation_receipt IS NOT NULL AND scope_removal_checked)),
  CHECK(status NOT IN ('ready','applied') OR pages>0)
);
CREATE UNIQUE INDEX dashboard_event_recovery_current ON dashboard_event_recoveries(deployment_id)
  WHERE status IN ('replaying','ready','quarantined');
CREATE INDEX dashboard_event_recovery_history ON dashboard_event_recoveries(deployment_id,created_at,id);
