CREATE TABLE dashboard_accounts (
  id uuid PRIMARY KEY,
  directory_id uuid NOT NULL UNIQUE,
  display_name text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  disabled_at timestamptz
);

CREATE TABLE dashboard_identity_aliases (
  issuer text NOT NULL,
  subject text NOT NULL,
  account_id uuid NOT NULL REFERENCES dashboard_accounts(id),
  verified_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (issuer, subject)
);

CREATE TABLE dashboard_sessions (
  token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
  account_id uuid NOT NULL REFERENCES dashboard_accounts(id),
  csrf_hash bytea NOT NULL CHECK (octet_length(csrf_hash) = 32),
  encrypted_refresh_token bytea,
  encryption_key_id text,
  created_at timestamptz NOT NULL,
  touched_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  CHECK (expires_at > created_at)
);
CREATE INDEX dashboard_sessions_expiry ON dashboard_sessions(expires_at);

CREATE TABLE dashboard_preferences (
  account_id uuid PRIMARY KEY REFERENCES dashboard_accounts(id),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  timezone text NOT NULL DEFAULT 'UTC',
  appearance text NOT NULL DEFAULT 'system' CHECK (appearance IN ('system','light','dark')),
  refresh_seconds integer NOT NULL DEFAULT 5 CHECK (refresh_seconds IN (0,5,10,30)),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- The application payload includes account, query hash, source identity/epoch,
-- authorization versions, cutoffs and unconsumed buffers. No Control credentials.
CREATE TABLE dashboard_browse_sessions (
  id text PRIMARY KEY CHECK (length(id) = 43),
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  payload bytea NOT NULL CHECK (octet_length(payload) <= 4194304),
  expires_at timestamptz NOT NULL
);
CREATE INDEX dashboard_browse_sessions_expiry ON dashboard_browse_sessions(expires_at);

CREATE TABLE dashboard_source_identities (
  deployment_id uuid PRIMARY KEY,
  control_instance_id uuid NOT NULL,
  recovery_epoch text NOT NULL,
  configuration_revision bigint NOT NULL CHECK (configuration_revision > 0),
  verified_at timestamptz NOT NULL
);

CREATE TABLE dashboard_audit (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  account_id uuid,
  action text NOT NULL,
  resource_kind text NOT NULL,
  resource_id text,
  recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  request_id text
);
CREATE INDEX dashboard_audit_retention ON dashboard_audit(recorded_at);
