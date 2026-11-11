-- Maintain the task-level evidence aggregate on every insert, model-key
-- correction, or task cleanup. No foreign key is used: task deletion already
-- owns explicit evidence cleanup and this trigger removes the matching summary.
CREATE OR REPLACE FUNCTION refresh_task_usage_model_call_totals()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP IN ('DELETE', 'UPDATE') THEN
        DELETE FROM task_usage_model_call_totals
        WHERE task_id = OLD.task_id
          AND provider = OLD.provider
          AND model = OLD.model
          AND NOT EXISTS (
              SELECT 1
              FROM task_usage_model_call_evidence e
              WHERE e.task_id = OLD.task_id
                AND e.provider = OLD.provider
                AND e.model = OLD.model
          );
    END IF;

    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        INSERT INTO task_usage_model_call_totals (task_id, provider, model, model_call_count)
        SELECT task_id, provider, model, COUNT(*)::bigint
        FROM task_usage_model_call_evidence
        WHERE task_id = NEW.task_id
          AND provider = NEW.provider
          AND model = NEW.model
        GROUP BY task_id, provider, model
        ON CONFLICT (task_id, provider, model) DO UPDATE
        SET model_call_count = EXCLUDED.model_call_count,
            updated_at = now();
    END IF;

    RETURN COALESCE(NEW, OLD);
END;
$$;

DROP TRIGGER IF EXISTS task_usage_model_call_totals_sync ON task_usage_model_call_evidence;
CREATE TRIGGER task_usage_model_call_totals_sync
AFTER INSERT OR UPDATE OR DELETE ON task_usage_model_call_evidence
FOR EACH ROW EXECUTE FUNCTION refresh_task_usage_model_call_totals();
