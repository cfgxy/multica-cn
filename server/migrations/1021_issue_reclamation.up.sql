-- Retain finished tokens until Issue deletion: a delayed replay must never
-- turn a finished reservation into a fresh authorization. The Issue row is
-- the lock protecting all reservations for that Issue; there is no expiry.
CREATE TABLE issue_reclamation (
    issue_id uuid NOT NULL,
    daemon_id text NOT NULL,
    owner_id uuid NOT NULL,
    token_hash bytea NOT NULL,
    finished_at timestamptz
);

CREATE FUNCTION reserve_issue_reclamation(
    p_workspace_id uuid, p_issue_id uuid, p_daemon_id text,
    p_owner_id uuid, p_token_hash bytea, p_finish boolean
) RETURNS text LANGUAGE plpgsql AS $$
DECLARE
    v_status text;
    v_category text;
    v_finished_at timestamptz;
BEGIN
    -- A fresh snapshot after acquiring the Issue lock is required to see
    -- a Run whose writer already held a compatible-with-updates SHARE lock.
    IF current_setting('transaction_isolation') <> 'read committed' THEN
        RAISE EXCEPTION 'issue reclamation requires read committed isolation';
    END IF;

    SELECT status INTO v_status FROM issue
    WHERE workspace_id = p_workspace_id AND id = p_issue_id FOR UPDATE;
    IF NOT FOUND THEN
        RETURN 'not_found';
    END IF;

    SELECT finished_at INTO v_finished_at FROM issue_reclamation
    WHERE issue_id = p_issue_id AND daemon_id = p_daemon_id
      AND owner_id = p_owner_id AND token_hash = p_token_hash;
    IF FOUND THEN
        IF p_finish THEN
            IF v_finished_at IS NULL THEN
                UPDATE issue_reclamation SET finished_at = now()
                WHERE issue_id = p_issue_id AND daemon_id = p_daemon_id
                  AND owner_id = p_owner_id AND token_hash = p_token_hash;
            END IF;
            RETURN 'ok';
        END IF;
        RETURN CASE WHEN v_finished_at IS NULL THEN 'ok' ELSE 'conflict' END;
    END IF;

    IF p_finish THEN
        -- Cancel even a begin request that has not arrived yet.
        INSERT INTO issue_reclamation (issue_id, daemon_id, owner_id, token_hash, finished_at)
        VALUES (p_issue_id, p_daemon_id, p_owner_id, p_token_hash, now());
        RETURN 'ok';
    END IF;

    IF EXISTS (
        SELECT 1 FROM issue_reclamation WHERE issue_id = p_issue_id AND finished_at IS NULL
    ) THEN
        RETURN 'conflict';
    END IF;

    IF v_status IN ('backlog', 'todo', 'in_progress', 'in_review', 'done', 'blocked', 'cancelled') THEN
        v_category := v_status;
    ELSE
        -- Archived keys keep their category. Lock against catalog removal,
        -- including before the active reservation row exists.
        SELECT category INTO v_category FROM issue_status
        WHERE workspace_id = p_workspace_id AND key = v_status FOR SHARE;
    END IF;
    IF v_category IS NULL OR v_category NOT IN ('done', 'cancelled') THEN
        RETURN 'conflict';
    END IF;
    IF EXISTS (
        SELECT 1 FROM agent_task_queue
        WHERE issue_id = p_issue_id AND status NOT IN ('completed', 'failed', 'cancelled')
    ) THEN
        RETURN 'conflict';
    END IF;

    INSERT INTO issue_reclamation (issue_id, daemon_id, owner_id, token_hash)
    VALUES (p_issue_id, p_daemon_id, p_owner_id, p_token_hash);
    -- Force a new tuple version without changing user-visible timestamps or
    -- revision. A repeatable-read writer whose snapshot predates the fence
    -- now gets a serialization error rather than missing the reservation.
    UPDATE issue SET updated_at = updated_at WHERE id = p_issue_id;
    RETURN 'ok';
END;
$$;

CREATE FUNCTION guard_issue_reclamation_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM issue_reclamation WHERE issue_id = OLD.id AND finished_at IS NULL) THEN
        RAISE EXCEPTION 'issue worktree reclamation is in progress' USING ERRCODE = 'MC001';
    END IF;
    IF TG_OP = 'DELETE' THEN
        DELETE FROM issue_reclamation WHERE issue_id = OLD.id;
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_issue_reclamation_status
BEFORE UPDATE OF status, workspace_id, id ON issue
FOR EACH ROW WHEN (OLD.status IS DISTINCT FROM NEW.status
                OR OLD.workspace_id IS DISTINCT FROM NEW.workspace_id
                OR OLD.id IS DISTINCT FROM NEW.id)
EXECUTE FUNCTION guard_issue_reclamation_mutation();

CREATE TRIGGER trg_issue_reclamation_delete
BEFORE DELETE ON issue FOR EACH ROW EXECUTE FUNCTION guard_issue_reclamation_mutation();

CREATE FUNCTION guard_issue_reclamation_run() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- Serialize every potentially executable Run write with begin, even if
    -- its caller used an older API binary or bypassed the enqueue helper.
    PERFORM 1 FROM issue WHERE id = NEW.issue_id FOR SHARE;
    IF EXISTS (SELECT 1 FROM issue_reclamation WHERE issue_id = NEW.issue_id AND finished_at IS NULL) THEN
        RAISE EXCEPTION 'issue worktree reclamation is in progress' USING ERRCODE = 'MC001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_issue_reclamation_run
BEFORE INSERT OR UPDATE OF issue_id, status, dispatched_at, runtime_id ON agent_task_queue
FOR EACH ROW WHEN (NEW.issue_id IS NOT NULL AND NEW.status NOT IN ('completed', 'failed', 'cancelled'))
EXECUTE FUNCTION guard_issue_reclamation_run();

CREATE FUNCTION guard_issue_reclamation_catalog() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- Issue status identity/category is already immutable in the API. Guard
    -- direct catalog writes too, without interfering with archive/reorder.
    IF EXISTS (
        SELECT 1 FROM issue i JOIN issue_reclamation r ON r.issue_id = i.id
        WHERE i.workspace_id = OLD.workspace_id AND i.status = OLD.key AND r.finished_at IS NULL
    ) THEN
        RAISE EXCEPTION 'issue worktree reclamation is in progress' USING ERRCODE = 'MC001';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_issue_reclamation_catalog_update
BEFORE UPDATE OF key, category, workspace_id ON issue_status
FOR EACH ROW WHEN (OLD.key IS DISTINCT FROM NEW.key OR OLD.category IS DISTINCT FROM NEW.category
                OR OLD.workspace_id IS DISTINCT FROM NEW.workspace_id)
EXECUTE FUNCTION guard_issue_reclamation_catalog();

CREATE TRIGGER trg_issue_reclamation_catalog_delete
BEFORE DELETE ON issue_status FOR EACH ROW EXECUTE FUNCTION guard_issue_reclamation_catalog();
