-- Private rule snapshots and activation boundaries are server-owned. Public
-- projections must not expose represented principal IDs or opaque feed cursors.
CREATE TABLE dashboard_notification_rules (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES dashboard_accounts(id),
  revision bigint NOT NULL CHECK(revision>0),
  enabled boolean NOT NULL,
  payload bytea NOT NULL CHECK(octet_length(payload) BETWEEN 1 AND 524288),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  deleted_at timestamptz,
  UNIQUE(id,account_id),
  CHECK(updated_at>=created_at),
  CHECK(deleted_at IS NULL OR (NOT enabled AND deleted_at=updated_at))
);
CREATE INDEX dashboard_notification_rules_owner ON dashboard_notification_rules(account_id,created_at,id) WHERE deleted_at IS NULL;

CREATE TABLE dashboard_notification_rule_versions (
  rule_id uuid NOT NULL,
  revision bigint NOT NULL CHECK(revision>0),
  account_id uuid NOT NULL,
  payload bytea NOT NULL CHECK(octet_length(payload) BETWEEN 1 AND 524288),
  regular_mutation boolean NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY(rule_id,revision),
  FOREIGN KEY(rule_id,account_id) REFERENCES dashboard_notification_rules(id,account_id)
);
CREATE INDEX dashboard_notification_rule_versions_owner ON dashboard_notification_rule_versions(account_id,recorded_at DESC);
ALTER TABLE dashboard_notification_rules ADD CONSTRAINT dashboard_notification_rules_current_version
  FOREIGN KEY(id,revision) REFERENCES dashboard_notification_rule_versions(rule_id,revision) DEFERRABLE INITIALLY DEFERRED;

-- An interval UUID cannot be reused after replacement, even by the same rule.
-- Unchanged intervals can be referenced by multiple historical rule versions.
CREATE TABLE dashboard_notification_activations (
  id uuid PRIMARY KEY,
  rule_id uuid NOT NULL,
  created_revision bigint NOT NULL,
  deployment_id uuid NOT NULL,
  namespace_id uuid NOT NULL,
  payload bytea NOT NULL CHECK(octet_length(payload) BETWEEN 1 AND 4096),
  FOREIGN KEY(rule_id,created_revision) REFERENCES dashboard_notification_rule_versions(rule_id,revision)
);
CREATE INDEX dashboard_notification_activations_rule ON dashboard_notification_activations(rule_id);

-- Current scope selection is indexed independently from immutable history.
-- It bounds candidate lookup without treating a past namespace as subscribed.
CREATE TABLE dashboard_notification_rule_scopes (
  rule_id uuid NOT NULL,
  account_id uuid NOT NULL,
  deployment_id uuid NOT NULL,
  namespace_id uuid NOT NULL,
  activation_id uuid NOT NULL REFERENCES dashboard_notification_activations(id),
  PRIMARY KEY(rule_id,deployment_id,namespace_id),
  FOREIGN KEY(rule_id,account_id) REFERENCES dashboard_notification_rules(id,account_id)
);
CREATE INDEX dashboard_notification_rule_scopes_source
 ON dashboard_notification_rule_scopes(deployment_id,namespace_id,account_id,rule_id);

-- A grant denial is a monotonic override, not a new subscription interval.
-- It must remain writable even when user-edit history/rate quotas are full.
-- Matching and delivery consult it in addition to immutable rule snapshots.
CREATE TABLE dashboard_notification_activation_revocations (
  activation_id uuid PRIMARY KEY REFERENCES dashboard_notification_activations(id),
  revoked_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE FUNCTION dashboard_immutable_notification_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'notification history is immutable' USING ERRCODE='check_violation';
END;
$$;
CREATE TRIGGER dashboard_notification_versions_immutable BEFORE UPDATE ON dashboard_notification_rule_versions
  FOR EACH ROW EXECUTE FUNCTION dashboard_immutable_notification_history();
CREATE TRIGGER dashboard_notification_activations_immutable BEFORE UPDATE ON dashboard_notification_activations
  FOR EACH ROW EXECUTE FUNCTION dashboard_immutable_notification_history();
CREATE TRIGGER dashboard_notification_revocations_immutable BEFORE UPDATE OR DELETE ON dashboard_notification_activation_revocations
  FOR EACH ROW EXECUTE FUNCTION dashboard_immutable_notification_history();
