-- Only bounded request metadata enters the queue. Sealed evidence and reports
-- are stored together in the private object root before a ready row is committed.
CREATE TABLE dashboard_report_tasks (
  id uuid PRIMARY KEY,
  deployment_id uuid NOT NULL,
  namespace_id uuid NOT NULL,
  job_id uuid NOT NULL,
  equivalent_key bytea NOT NULL CHECK (octet_length(equivalent_key)=32),
  subject bytea NOT NULL CHECK (octet_length(subject) BETWEEN 1 AND 4096),
  state text NOT NULL CHECK (state IN ('queued','collecting','analyzing','ready','failed')),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '30 days',
  next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 3),
  lease_token uuid,
  lease_expires_at timestamptz,
  failure_code text NOT NULL DEFAULT '' CHECK (failure_code IN ('','source_unavailable','authorization_unavailable','forbidden','snapshot_changed','invalid_evidence','worker_interrupted','analysis_failed')),
  object bytea CHECK (octet_length(object) BETWEEN 1 AND 1024),
  CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)),
  CHECK ((state IN ('collecting','analyzing')) = (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)),
  CHECK ((state='ready') = (object IS NOT NULL)),
  CHECK (expires_at > created_at)
);
CREATE UNIQUE INDEX dashboard_report_one_pending ON dashboard_report_tasks(equivalent_key)
  WHERE state IN ('queued','collecting','analyzing');
CREATE INDEX dashboard_report_equivalent_ready ON dashboard_report_tasks(equivalent_key,created_at DESC)
  WHERE state='ready';
CREATE INDEX dashboard_report_queue ON dashboard_report_tasks(next_attempt_at,created_at,id)
  WHERE state IN ('queued','collecting','analyzing');
CREATE INDEX dashboard_report_subject ON dashboard_report_tasks(deployment_id,namespace_id,job_id,created_at DESC,id DESC);
CREATE INDEX dashboard_report_expiry ON dashboard_report_tasks(expires_at);

-- A shared task has separately owned requester bindings. No access token or
-- refresh token is persisted; workers resolve only previously verified aliases
-- and must obtain fresh represented-user source authorization.
CREATE TABLE dashboard_report_requesters (
  task_id uuid NOT NULL REFERENCES dashboard_report_tasks(id) ON DELETE CASCADE,
  account_id uuid NOT NULL REFERENCES dashboard_accounts(id),
  issuer text NOT NULL,
  subject text NOT NULL,
  requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY(task_id,account_id)
);
CREATE INDEX dashboard_report_requesters_account ON dashboard_report_requesters(account_id,task_id);

CREATE TABLE dashboard_report_idempotency (
  account_id uuid NOT NULL REFERENCES dashboard_accounts(id),
  key_hash bytea NOT NULL CHECK (octet_length(key_hash)=32),
  request_key bytea NOT NULL CHECK (octet_length(request_key)=32),
  task_id uuid NOT NULL REFERENCES dashboard_report_tasks(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY(account_id,key_hash)
);
CREATE INDEX dashboard_report_idempotency_task ON dashboard_report_idempotency(task_id);
CREATE INDEX dashboard_report_request_rate ON dashboard_report_idempotency(account_id,created_at DESC);
