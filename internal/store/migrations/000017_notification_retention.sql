-- A monotonic source retention high-water is already maintained by ingestion.
CREATE FUNCTION dashboard_notification_retention_seconds() RETURNS bigint LANGUAGE sql STABLE AS $$
 SELECT GREATEST(3024000,COALESCE(max(retention_seconds),86400)+432000) FROM dashboard_event_feeds
$$;

-- Inbox payload expires independently from the longer-lived delivery journal.
ALTER TABLE dashboard_notification_deliveries ADD COLUMN original_inbox_id uuid;
UPDATE dashboard_notification_deliveries SET original_inbox_id=inbox_id;
ALTER TABLE dashboard_notification_deliveries ALTER COLUMN original_inbox_id SET NOT NULL;
ALTER TABLE dashboard_notification_deliveries ALTER COLUMN inbox_id DROP NOT NULL;
ALTER TABLE dashboard_notification_deliveries DROP CONSTRAINT dashboard_notification_deliveries_inbox_id_fkey;
ALTER TABLE dashboard_notification_deliveries ADD CONSTRAINT dashboard_notification_deliveries_inbox_id_fkey FOREIGN KEY(inbox_id) REFERENCES dashboard_notification_inbox(id) ON DELETE SET NULL;
ALTER TABLE dashboard_notification_deliveries ADD CONSTRAINT dashboard_notification_delivery_pending_inbox CHECK(state<>'pending' OR inbox_id IS NOT NULL);
CREATE FUNCTION dashboard_notification_delivery_inbox_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.inbox_id IS NULL THEN RAISE EXCEPTION 'new delivery requires inbox'; END IF;
  NEW.original_inbox_id:=NEW.inbox_id;
 ELSIF NEW.original_inbox_id<>OLD.original_inbox_id OR NEW.inbox_id IS NOT NULL AND NEW.inbox_id<>OLD.original_inbox_id THEN
  RAISE EXCEPTION 'delivery inbox identity is immutable';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER dashboard_notification_delivery_inbox_identity BEFORE INSERT OR UPDATE ON dashboard_notification_deliveries FOR EACH ROW EXECUTE FUNCTION dashboard_notification_delivery_inbox_identity();
CREATE INDEX dashboard_notification_inbox_event ON dashboard_notification_inbox(deployment_id,control_instance_id,event_id);
CREATE INDEX dashboard_notification_delivery_retention ON dashboard_notification_deliveries(resolved_at,id) WHERE state<>'pending';

-- Keep the immutable authority origin independently from bulky rule snapshots.
ALTER TABLE dashboard_notification_activations ADD COLUMN account_id uuid REFERENCES dashboard_accounts(id);
ALTER TABLE dashboard_notification_activations ADD COLUMN origin_recorded_at timestamptz;
ALTER TABLE dashboard_notification_activations ADD COLUMN retired_at timestamptz;
DROP TRIGGER dashboard_notification_activations_immutable ON dashboard_notification_activations;
UPDATE dashboard_notification_activations a SET account_id=v.account_id,origin_recorded_at=v.recorded_at FROM dashboard_notification_rule_versions v WHERE v.rule_id=a.rule_id AND v.revision=a.created_revision;
ALTER TABLE dashboard_notification_activations ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE dashboard_notification_activations ALTER COLUMN origin_recorded_at SET NOT NULL;
ALTER TABLE dashboard_notification_activations ALTER COLUMN payload DROP NOT NULL;
ALTER TABLE dashboard_notification_activations ADD CONSTRAINT dashboard_notification_activation_retired CHECK((payload IS NULL)=(retired_at IS NOT NULL));
-- PostgreSQL truncates automatically generated multi-column FK names.
DO $$ DECLARE constraint_name name; BEGIN
 SELECT conname INTO STRICT constraint_name FROM pg_constraint
 WHERE conrelid='dashboard_notification_activations'::regclass AND confrelid='dashboard_notification_rule_versions'::regclass AND contype='f';
 EXECUTE format('ALTER TABLE dashboard_notification_activations DROP CONSTRAINT %I',constraint_name);
END $$;
CREATE INDEX dashboard_notification_activation_retention ON dashboard_notification_activations(account_id,origin_recorded_at,id) WHERE retired_at IS NULL;
CREATE FUNCTION dashboard_notification_activation_origin() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 SELECT account_id,recorded_at INTO STRICT NEW.account_id,NEW.origin_recorded_at FROM dashboard_notification_rule_versions WHERE rule_id=NEW.rule_id AND revision=NEW.created_revision;
 IF NEW.payload IS NULL OR NEW.retired_at IS NOT NULL THEN RAISE EXCEPTION 'new activation cannot be retired'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER dashboard_notification_activation_origin BEFORE INSERT ON dashboard_notification_activations FOR EACH ROW EXECUTE FUNCTION dashboard_notification_activation_origin();

CREATE TABLE dashboard_notification_version_activations (
 rule_id uuid NOT NULL,
 revision bigint NOT NULL,
 activation_id uuid NOT NULL REFERENCES dashboard_notification_activations(id),
 PRIMARY KEY(rule_id,revision,activation_id),
 FOREIGN KEY(rule_id,revision) REFERENCES dashboard_notification_rule_versions(rule_id,revision) ON DELETE CASCADE
);
INSERT INTO dashboard_notification_version_activations(rule_id,revision,activation_id)
 SELECT v.rule_id,v.revision,(a->>'id')::uuid FROM dashboard_notification_rule_versions v CROSS JOIN LATERAL jsonb_array_elements(convert_from(v.payload,'UTF8')::jsonb->'activation') a;
CREATE INDEX dashboard_notification_version_activations_id ON dashboard_notification_version_activations(activation_id);
CREATE INDEX dashboard_notification_evaluation_matches_activation ON dashboard_notification_evaluation_matches(activation_id);
CREATE INDEX dashboard_notification_inbox_matches_activation ON dashboard_notification_inbox_matches(activation_id);
CREATE INDEX dashboard_notification_scopes_activation ON dashboard_notification_rule_scopes(activation_id);
CREATE INDEX dashboard_notification_evaluation_matches_version ON dashboard_notification_evaluation_matches(rule_id,rule_revision);
CREATE INDEX dashboard_notification_inbox_matches_version ON dashboard_notification_inbox_matches(rule_id,rule_revision);

CREATE FUNCTION dashboard_notification_activation_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'activation identity is permanent'; END IF;
 IF OLD.retired_at IS NOT NULL OR NEW.payload IS NOT NULL OR NEW.retired_at IS NULL
  OR (to_jsonb(NEW)-'payload'-'retired_at')<>(to_jsonb(OLD)-'payload'-'retired_at')
  OR OLD.origin_recorded_at>statement_timestamp()-dashboard_notification_retention_seconds()*interval '1 second'
  OR EXISTS(SELECT 1 FROM dashboard_notification_version_activations WHERE activation_id=OLD.id)
  OR EXISTS(SELECT 1 FROM dashboard_notification_rule_scopes WHERE activation_id=OLD.id)
  OR EXISTS(SELECT 1 FROM dashboard_notification_evaluation_matches WHERE activation_id=OLD.id)
  OR EXISTS(SELECT 1 FROM dashboard_notification_inbox_matches WHERE activation_id=OLD.id)
 THEN RAISE EXCEPTION 'activation history remains required'; END IF;
 NEW.retired_at:=statement_timestamp();
 RETURN NEW;
END;
$$;
CREATE TRIGGER dashboard_notification_activations_immutable BEFORE UPDATE OR DELETE ON dashboard_notification_activations FOR EACH ROW EXECUTE FUNCTION dashboard_notification_activation_retirement();
CREATE FUNCTION dashboard_notification_retired_activation_reference() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM dashboard_notification_activations WHERE id=NEW.activation_id AND retired_at IS NULL) THEN RAISE EXCEPTION 'retired activation cannot be referenced'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER dashboard_notification_version_activation_live BEFORE INSERT OR UPDATE ON dashboard_notification_version_activations FOR EACH ROW EXECUTE FUNCTION dashboard_notification_retired_activation_reference();

DROP TRIGGER dashboard_notification_revocations_immutable ON dashboard_notification_activation_revocations;
CREATE FUNCTION dashboard_notification_revocation_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' AND EXISTS(SELECT 1 FROM dashboard_notification_activations WHERE id=OLD.activation_id AND payload IS NULL AND retired_at IS NOT NULL) THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'notification revocation is immutable';
END;
$$;
CREATE TRIGGER dashboard_notification_revocations_immutable BEFORE UPDATE OR DELETE ON dashboard_notification_activation_revocations FOR EACH ROW EXECUTE FUNCTION dashboard_notification_revocation_retirement();

-- Round-robin account admission prevents old live snapshots from starving later
-- accounts. Advancing this maintenance cursor cannot change delivery authority.
CREATE TABLE dashboard_notification_retention_progress (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 after_account_id uuid
);
INSERT INTO dashboard_notification_retention_progress(singleton) VALUES(true);
