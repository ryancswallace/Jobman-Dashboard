-- Scope predicates always include the owner and current Control instance.
-- Read state is mutable, so use an independent partial unread index.
CREATE INDEX dashboard_notification_inbox_scope
 ON dashboard_notification_inbox(account_id,deployment_id,namespace_id,control_instance_id,created_at DESC,id DESC) INCLUDE(expires_at,read_at);
CREATE INDEX dashboard_notification_inbox_unread_scope
 ON dashboard_notification_inbox(account_id,deployment_id,namespace_id,control_instance_id,expires_at) WHERE read_at IS NULL;
