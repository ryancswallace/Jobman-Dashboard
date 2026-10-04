-- PostgreSQL row locks require UPDATE on at least one column. A generated
-- constant grants that authority without granting writes to identity, session,
-- settings or authority fields. No application writes this column. PostgreSQL17
-- rewrites these tables while adding STORED columns; plan migration downtime.
-- Do not add it to activation rows: their whole-row immutability trigger inspects
-- NEW before generated columns are computed.
ALTER TABLE dashboard_accounts ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_identity_aliases ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_sessions ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_login_attempts ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_audit ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_browse_sessions ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_event_feeds ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_notification_delivery_control ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_notification_inbox ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_report_tasks ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_notification_rules ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_notification_rule_versions ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_notification_delivery_attempts ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_notification_installations ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
ALTER TABLE dashboard_notification_device_bindings ADD COLUMN runtime_lock boolean GENERATED ALWAYS AS (true) STORED;
