CREATE INDEX dashboard_sessions_account_created ON dashboard_sessions(account_id,created_at DESC);
CREATE INDEX dashboard_sessions_idle ON dashboard_sessions(touched_at);
