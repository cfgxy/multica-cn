-- Pin the runtime a measurement was taken on (RUYI-185 review, A1).
--
-- A quiz reading is only comparable to another reading taken THE SAME WAY. The
-- question, its wording and the prompt version were already pinned on every
-- row; the runtime was not, so switching an agent from one runtime or model to
-- another would silently continue the same curve with a different measuring
-- instrument, and the version diff would read as a prompt change.
--
-- WHY THE EXECUTED PAIR AND NOT execution_profile_id:
--   Migration 904 records that workspace.active_execution_profile_id is
--   'Display state only: it records which profile last wrote the agents'
--   runtime/model/thinking_level, not a live binding'. A profile therefore
--   cannot answer "what actually ran this"; the runtime row the queue claimed
--   and the model the daemon reported usage for can.
--
-- Both columns are nullable, and neither is a valid measurement input:
--   runtime_id is NULL only for a row written before this migration, because
--   migration 251 made agent_task_queue.runtime_id nullable on terminal rows
--   and the collector now SKIPS such a run — a run whose runtime is unknown
--   cannot be placed in any cohort.
--   run_model is NULL when the daemon reported no usage for the run. Such a run
--   also has no run_tokens, so it never enters a sample either way.
-- The reader treats a NULL in either column as its own cohort key rather than
-- as a wildcard, so an unattributed row can never be folded in with an
-- attributed one.
ALTER TABLE prompt_quiz_result
    ADD COLUMN IF NOT EXISTS runtime_id UUID,
    ADD COLUMN IF NOT EXISTS run_model TEXT;

COMMENT ON COLUMN prompt_quiz_result.runtime_id IS
    'The agent_runtime row the measuring run was claimed with. Part of the cohort key: readings from two runtimes are never merged into one sample group. NULL means unattributed and therefore not comparable.';

COMMENT ON COLUMN prompt_quiz_result.run_model IS
    'The model(s) the daemon reported usage under for the measuring run, comma-joined when a run spanned more than one. Part of the cohort key for the same reason as runtime_id. NULL means the daemon reported no usage, which also leaves run_tokens NULL.';
