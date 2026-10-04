-- The quota row serializes only short admission/resolve transactions. Historical
-- deduplication records do not consume pending-work quota.
CREATE TABLE dashboard_notification_work_quota (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 pending_evaluations integer NOT NULL DEFAULT 0 CHECK(pending_evaluations BETWEEN 0 AND 100000),
 pending_deliveries integer NOT NULL DEFAULT 0 CHECK(pending_deliveries BETWEEN 0 AND 1000000)
);
INSERT INTO dashboard_notification_work_quota(singleton) VALUES(true);
CREATE INDEX dashboard_source_events_fanout_pending ON dashboard_source_events(deployment_id,first_seen_at,event_id) WHERE processed_at IS NULL;

CREATE TABLE dashboard_notification_fanout (
 deployment_id uuid NOT NULL,
 control_instance_id uuid NOT NULL,
 event_id uuid NOT NULL,
 after_rule_id uuid,
 done boolean NOT NULL DEFAULT false,
 account_count integer NOT NULL DEFAULT 0 CHECK(account_count BETWEEN 0 AND 10000),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 lease_token uuid,
 lease_expires_at timestamptz,
 lease_hold_generation bigint,
 lease_fence bytea CHECK(octet_length(lease_fence) BETWEEN 1 AND 40000),
 PRIMARY KEY(deployment_id,control_instance_id,event_id),
 FOREIGN KEY(deployment_id,control_instance_id,event_id) REFERENCES dashboard_source_events(deployment_id,control_instance_id,event_id) ON DELETE CASCADE,
 CHECK((lease_token IS NULL)=(lease_expires_at IS NULL)),
 CHECK((lease_token IS NULL)=(lease_hold_generation IS NULL)),
 CHECK((lease_token IS NULL)=(lease_fence IS NULL)),
 CHECK(NOT done OR lease_token IS NULL)
);
CREATE INDEX dashboard_notification_fanout_pending ON dashboard_notification_fanout(deployment_id,lease_expires_at) WHERE NOT done;

CREATE TABLE dashboard_notification_evaluations (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES dashboard_accounts(id),
 deployment_id uuid NOT NULL,
 control_instance_id uuid NOT NULL,
 event_id uuid NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','complete','suppressed')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 attempts bigint NOT NULL DEFAULT 0 CHECK(attempts>=0),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease_token uuid,
 lease_expires_at timestamptz,
 lease_hold_generation bigint,
 lease_fence bytea CHECK(octet_length(lease_fence) BETWEEN 1 AND 40000),
 inbox_id uuid,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 resolved_at timestamptz,
 UNIQUE(account_id,deployment_id,control_instance_id,event_id),
 FOREIGN KEY(deployment_id,control_instance_id,event_id) REFERENCES dashboard_source_events(deployment_id,control_instance_id,event_id) ON DELETE CASCADE,
 CHECK((lease_token IS NULL)=(lease_expires_at IS NULL)),
 CHECK((lease_token IS NULL)=(lease_hold_generation IS NULL)),
 CHECK((lease_token IS NULL)=(lease_fence IS NULL)),
 CHECK((state='pending')=(resolved_at IS NULL)),
 CHECK(state='pending' OR lease_token IS NULL),
 CHECK((state='complete')=(inbox_id IS NOT NULL))
);
CREATE INDEX dashboard_notification_evaluations_due ON dashboard_notification_evaluations(deployment_id,next_attempt_at,id) WHERE state='pending';
CREATE INDEX dashboard_notification_evaluations_event_pending ON dashboard_notification_evaluations(deployment_id,control_instance_id,event_id) WHERE state='pending';
CREATE TABLE dashboard_notification_evaluation_matches (
 evaluation_id uuid NOT NULL REFERENCES dashboard_notification_evaluations(id) ON DELETE CASCADE,
 rule_id uuid NOT NULL,
 rule_revision bigint NOT NULL,
 activation_id uuid NOT NULL REFERENCES dashboard_notification_activations(id),
 PRIMARY KEY(evaluation_id,rule_id),
 FOREIGN KEY(rule_id,rule_revision) REFERENCES dashboard_notification_rule_versions(rule_id,revision)
);
CREATE TRIGGER dashboard_notification_evaluation_matches_immutable BEFORE UPDATE ON dashboard_notification_evaluation_matches
 FOR EACH ROW EXECUTE FUNCTION dashboard_immutable_notification_history();

-- Inbox lifetime starts at insertion, independently of the original event's
-- retention deadline. Do not cascade source-event tombstone cleanup into inbox.
CREATE TABLE dashboard_notification_inbox (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES dashboard_accounts(id),
 deployment_id uuid NOT NULL,
 control_instance_id uuid NOT NULL,
 event_id uuid NOT NULL,
 namespace_id uuid NOT NULL,
 job_id uuid NOT NULL,
 recorded_at timestamptz NOT NULL,
 event_payload bytea NOT NULL CHECK(octet_length(event_payload) BETWEEN 1 AND 4096),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '30 days',
 read_at timestamptz,
 UNIQUE(account_id,deployment_id,control_instance_id,event_id),
 CHECK(expires_at>created_at)
);
CREATE INDEX dashboard_notification_inbox_owner ON dashboard_notification_inbox(account_id,created_at DESC,id DESC);
CREATE INDEX dashboard_notification_inbox_expiry ON dashboard_notification_inbox(expires_at,id);
CREATE TABLE dashboard_notification_inbox_matches (
 inbox_id uuid NOT NULL REFERENCES dashboard_notification_inbox(id) ON DELETE CASCADE,
 rule_id uuid NOT NULL,
 rule_revision bigint NOT NULL,
 activation_id uuid NOT NULL REFERENCES dashboard_notification_activations(id),
 PRIMARY KEY(inbox_id,rule_id),
 FOREIGN KEY(rule_id,rule_revision) REFERENCES dashboard_notification_rule_versions(rule_id,revision)
);
CREATE TRIGGER dashboard_notification_inbox_matches_immutable BEFORE UPDATE ON dashboard_notification_inbox_matches
 FOR EACH ROW EXECUTE FUNCTION dashboard_immutable_notification_history();

-- Enqueue captures exact binding/token fences, never plaintext APNs tokens.
-- Sender implementation must reauthorize and recheck all gates before handoff.
CREATE TABLE dashboard_notification_deliveries (
 id uuid PRIMARY KEY,
 inbox_id uuid NOT NULL REFERENCES dashboard_notification_inbox(id),
 account_id uuid NOT NULL REFERENCES dashboard_accounts(id),
 installation_id uuid NOT NULL REFERENCES dashboard_notification_installations(id),
 binding_id uuid NOT NULL REFERENCES dashboard_notification_device_bindings(id),
 token_version bigint NOT NULL CHECK(token_version>0),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','accepted','suppressed','expired','failed')),
 attempts bigint NOT NULL DEFAULT 0 CHECK(attempts>=0),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease_token uuid,
 lease_expires_at timestamptz,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 resolved_at timestamptz,
 UNIQUE(inbox_id,binding_id),
 CHECK((lease_token IS NULL)=(lease_expires_at IS NULL)),
 CHECK((state='pending')=(resolved_at IS NULL)),
 CHECK(state='pending' OR lease_token IS NULL)
);
CREATE INDEX dashboard_notification_deliveries_due ON dashboard_notification_deliveries(next_attempt_at,id) WHERE state='pending';
CREATE INDEX dashboard_notification_deliveries_inbox ON dashboard_notification_deliveries(inbox_id);
