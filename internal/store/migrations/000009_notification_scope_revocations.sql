-- An operator removing a namespace revokes every interval created before the
-- recovery apply transaction, without an unbounded per-account rewrite. A later
-- configuration re-add cannot resurrect those intervals. Explicit reactivation
-- persists a new interval with a later database-owned creation time.
CREATE TABLE dashboard_notification_scope_revocations (
  deployment_id uuid NOT NULL REFERENCES dashboard_event_feeds(deployment_id),
  namespace_id uuid NOT NULL,
  revoked_through timestamptz NOT NULL,
  PRIMARY KEY(deployment_id,namespace_id)
);
CREATE FUNCTION dashboard_monotonic_scope_revocation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'namespace revocation cannot be removed' USING ERRCODE='check_violation';
  END IF;
  IF NEW.deployment_id<>OLD.deployment_id OR NEW.namespace_id<>OLD.namespace_id OR NEW.revoked_through<OLD.revoked_through THEN
    RAISE EXCEPTION 'namespace revocation cannot be rolled back' USING ERRCODE='check_violation';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER dashboard_notification_scope_revocations_monotonic
 BEFORE UPDATE OR DELETE ON dashboard_notification_scope_revocations
 FOR EACH ROW EXECUTE FUNCTION dashboard_monotonic_scope_revocation();
