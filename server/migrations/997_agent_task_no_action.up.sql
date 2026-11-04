ALTER TABLE agent_task_queue
ADD COLUMN IF NOT EXISTS no_action_at TIMESTAMPTZ,
ADD COLUMN IF NOT EXISTS no_action_reason TEXT,
ADD COLUMN IF NOT EXISTS comment_output_at TIMESTAMPTZ;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'agent_task_queue_no_action_reason_check'
          AND conrelid = 'agent_task_queue'::regclass
    ) THEN
        ALTER TABLE agent_task_queue
        ADD CONSTRAINT agent_task_queue_no_action_reason_check
        CHECK (no_action_at IS NOT NULL OR no_action_reason IS NULL);
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'agent_task_queue_no_action_output_check'
          AND conrelid = 'agent_task_queue'::regclass
    ) THEN
        ALTER TABLE agent_task_queue
        ADD CONSTRAINT agent_task_queue_no_action_output_check
        CHECK (no_action_at IS NULL OR comment_output_at IS NULL);
    END IF;
END $$;
