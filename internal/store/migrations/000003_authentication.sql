CREATE TABLE dashboard_login_attempts (
  state_hash bytea PRIMARY KEY CHECK (octet_length(state_hash)=32),
  encrypted_payload bytea NOT NULL CHECK (octet_length(encrypted_payload) BETWEEN 1 AND 4096),
  expires_at timestamptz NOT NULL
);
CREATE INDEX dashboard_login_attempts_expiry ON dashboard_login_attempts(expires_at);

-- Bind each server session to the exact verified sign-in alias. Existing
-- foundation databases had no authentication routes and therefore no sessions.
ALTER TABLE dashboard_sessions ADD COLUMN issuer text NOT NULL DEFAULT '';
ALTER TABLE dashboard_sessions ADD COLUMN subject text NOT NULL DEFAULT '';
