ALTER TABLE dashboard_notification_deliveries ADD COLUMN deployment_id uuid;
UPDATE dashboard_notification_deliveries d SET deployment_id=i.deployment_id FROM dashboard_notification_inbox i WHERE i.id=d.inbox_id;
ALTER TABLE dashboard_notification_deliveries ALTER COLUMN deployment_id SET NOT NULL;
ALTER TABLE dashboard_notification_deliveries ADD COLUMN revision bigint NOT NULL DEFAULT 1 CHECK(revision>0);
ALTER TABLE dashboard_notification_deliveries ADD COLUMN claims bigint NOT NULL DEFAULT 0 CHECK(claims>=0);
ALTER TABLE dashboard_notification_deliveries ADD COLUMN lease_hold_generation bigint;
ALTER TABLE dashboard_notification_deliveries ADD COLUMN lease_fence bytea CHECK(octet_length(lease_fence) BETWEEN 1 AND 40000);
ALTER TABLE dashboard_notification_deliveries ADD CONSTRAINT notification_delivery_hold_lease CHECK((lease_token IS NULL)=(lease_hold_generation IS NULL));
ALTER TABLE dashboard_notification_deliveries ADD CONSTRAINT notification_delivery_source_lease CHECK((lease_token IS NULL)=(lease_fence IS NULL));
CREATE INDEX dashboard_notification_deliveries_source_due ON dashboard_notification_deliveries(deployment_id,next_attempt_at,id) WHERE state='pending';
CREATE INDEX dashboard_notification_deliveries_expiry ON dashboard_notification_deliveries(expires_at,id) WHERE state='pending';

CREATE TABLE dashboard_notification_delivery_attempts (
 delivery_id uuid NOT NULL REFERENCES dashboard_notification_deliveries(id) ON DELETE CASCADE,
 number integer NOT NULL CHECK(number BETWEEN 1 AND 128),
 token_version bigint NOT NULL CHECK(token_version>0),
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 completed_at timestamptz,
 outcome text NOT NULL DEFAULT 'unknown' CHECK(outcome IN ('unknown','accepted','retry','token_invalid','rejected','provider_error')),
 reason text NOT NULL DEFAULT '' CHECK(length(reason)<=128),
 provider_id uuid,
 ambiguous boolean NOT NULL DEFAULT true,
 PRIMARY KEY(delivery_id,number),
 CHECK((completed_at IS NULL)=(outcome='unknown'))
);
-- Unknown attempts record crash/ambiguous handoff; replay keeps the same APNs ID.
CREATE TABLE dashboard_notification_provider_health (
 topic text NOT NULL CHECK(length(topic) BETWEEN 1 AND 255),
 environment text NOT NULL CHECK(environment IN ('sandbox','production')),
 retry_not_before timestamptz NOT NULL,
 last_failure_at timestamptz NOT NULL,
 last_reason text NOT NULL CHECK(length(last_reason)<=128),
 failures bigint NOT NULL CHECK(failures>0),
 PRIMARY KEY(topic,environment)
);
