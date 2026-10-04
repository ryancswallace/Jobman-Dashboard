CREATE TABLE dashboard_notification_activation_work (
 rule_id uuid PRIMARY KEY REFERENCES dashboard_notification_rules(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES dashboard_accounts(id),
 rule_revision bigint NOT NULL CHECK(rule_revision>0),
 attempts bigint NOT NULL DEFAULT 0 CHECK(attempts>=0),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease_token uuid,
 lease_expires_at timestamptz,
 CHECK((lease_token IS NULL)=(lease_expires_at IS NULL))
);
CREATE INDEX dashboard_notification_activation_work_due ON dashboard_notification_activation_work(next_attempt_at,rule_id);
INSERT INTO dashboard_notification_activation_work(rule_id,account_id,rule_revision)
 SELECT DISTINCT r.id,r.account_id,r.revision FROM dashboard_notification_rules r
 JOIN dashboard_notification_rule_scopes n ON n.rule_id=r.id
 JOIN dashboard_notification_activations a ON a.id=n.activation_id
 WHERE r.enabled AND r.deleted_at IS NULL AND convert_from(a.payload,'UTF8')::jsonb->>'status'='pending';
