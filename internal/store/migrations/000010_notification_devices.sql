CREATE TABLE dashboard_notification_installations (
  id uuid PRIMARY KEY,
  creator_account_id uuid NOT NULL REFERENCES dashboard_accounts(id),
  secret_hash bytea NOT NULL CHECK(octet_length(secret_hash)=32),
  revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
  current_binding_id uuid,
  mutation_window_started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  mutation_count integer NOT NULL DEFAULT 0 CHECK(mutation_count BETWEEN 0 AND 30),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX dashboard_notification_installations_creator ON dashboard_notification_installations(creator_account_id,created_at);

CREATE TABLE dashboard_notification_device_bindings (
  id uuid PRIMARY KEY,
  installation_id uuid NOT NULL REFERENCES dashboard_notification_installations(id),
  account_id uuid NOT NULL REFERENCES dashboard_accounts(id),
  label text NOT NULL CHECK(octet_length(label) BETWEEN 1 AND 120),
  topic text NOT NULL CHECK(octet_length(topic) BETWEEN 1 AND 255),
  environment text NOT NULL CHECK(environment IN ('sandbox','production')),
  state text NOT NULL CHECK(state IN ('bound','detached','removed')),
  enabled boolean NOT NULL,
  muted boolean NOT NULL,
  permission text NOT NULL CHECK(permission IN ('not_determined','denied','authorized','provisional','ephemeral')),
  token_version bigint NOT NULL CHECK(token_version>0),
  token_ciphertext bytea CHECK(octet_length(token_ciphertext) BETWEEN 30 AND 1100),
  token_key_id text CHECK(octet_length(token_key_id) BETWEEN 1 AND 64),
  token_digest bytea CHECK(octet_length(token_digest)=32),
  token_registered_at timestamptz NOT NULL,
  token_invalidated_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  last_seen_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  closed_at timestamptz,
  UNIQUE(id,installation_id),
  CHECK((state='bound')=(closed_at IS NULL)),
  CHECK((token_ciphertext IS NULL)=(token_key_id IS NULL)),
  CHECK((token_ciphertext IS NULL)=(token_digest IS NULL)),
  CHECK(state='bound' OR (NOT enabled AND token_ciphertext IS NULL))
);
ALTER TABLE dashboard_notification_installations ADD CONSTRAINT dashboard_notification_installation_binding
 FOREIGN KEY(current_binding_id,id) REFERENCES dashboard_notification_device_bindings(id,installation_id);
CREATE UNIQUE INDEX dashboard_notification_device_active_installation ON dashboard_notification_device_bindings(installation_id) WHERE state='bound';
CREATE UNIQUE INDEX dashboard_notification_device_active_token ON dashboard_notification_device_bindings(topic,environment,token_digest) WHERE state='bound';
CREATE INDEX dashboard_notification_devices_owner ON dashboard_notification_device_bindings(account_id,installation_id) WHERE state='bound';
CREATE INDEX dashboard_notification_device_binding_history ON dashboard_notification_device_bindings(installation_id,created_at DESC);
