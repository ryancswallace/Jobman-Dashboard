-- Operator state shared by every evaluator/provider worker. Restore cutoffs are
-- monotonic and survive release of the delivery hold.
CREATE TABLE dashboard_notification_delivery_control (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 generation bigint NOT NULL DEFAULT 1 CHECK(generation>0),
 held boolean NOT NULL DEFAULT false,
 restore_recorded_through timestamptz,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO dashboard_notification_delivery_control(singleton) VALUES(true);
CREATE FUNCTION dashboard_notification_delivery_control_monotonic() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR NEW.generation<=OLD.generation OR
    (OLD.restore_recorded_through IS NOT NULL AND (NEW.restore_recorded_through IS NULL OR NEW.restore_recorded_through<OLD.restore_recorded_through)) THEN
   RAISE EXCEPTION 'delivery control must advance without erasing restore uncertainty' USING ERRCODE='check_violation';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER dashboard_notification_delivery_control_monotonic BEFORE UPDATE OR DELETE ON dashboard_notification_delivery_control
 FOR EACH ROW EXECUTE FUNCTION dashboard_notification_delivery_control_monotonic();
