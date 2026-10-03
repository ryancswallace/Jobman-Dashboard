-- Quotas and unused-poll replacement are evaluated under one short advisory
-- transaction lock. Discard pre-upgrade ephemeral cursors; never infer owners.
DELETE FROM dashboard_browse_sessions;
ALTER TABLE dashboard_browse_sessions
  ADD COLUMN account_id text NOT NULL,
  ADD COLUMN query_hash text NOT NULL,
  ADD COLUMN initial_page boolean NOT NULL DEFAULT false,
  ADD COLUMN created_at timestamptz NOT NULL DEFAULT clock_timestamp();
CREATE INDEX dashboard_browse_account ON dashboard_browse_sessions(account_id,query_hash,created_at);
