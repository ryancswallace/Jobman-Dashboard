-- Reserved credentials cannot deliver until their acknowledged second phase.
-- Tombstones remain after closure, preventing opaque IDs from changing targets.
CREATE TABLE dashboard_notification_device_revocations (
  id uuid PRIMARY KEY,
  installation_id uuid NOT NULL REFERENCES dashboard_notification_installations(id),
  account_id uuid NOT NULL REFERENCES dashboard_accounts(id),
  origin_binding_id uuid REFERENCES dashboard_notification_device_bindings(id),
  target_binding_id uuid NOT NULL,
  intent text NOT NULL CHECK(intent IN ('bind','switch','existing')),
  state text NOT NULL CHECK(state IN ('reserved','active','revoked')),
  secret_hash bytea CHECK(octet_length(secret_hash)=32),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  activated_at timestamptz,
  revoked_at timestamptz,
  CHECK((state='revoked')=(secret_hash IS NULL)),
  CHECK((state='revoked')=(revoked_at IS NOT NULL)),
  CHECK(state<>'active' OR activated_at IS NOT NULL),
  CHECK((intent='bind')=(origin_binding_id IS NULL)),
  CHECK(intent<>'existing' OR origin_binding_id=target_binding_id)
);
CREATE INDEX dashboard_notification_device_revocations_installation ON dashboard_notification_device_revocations(installation_id,state);
CREATE INDEX dashboard_notification_device_revocations_target ON dashboard_notification_device_revocations(target_binding_id,state);
