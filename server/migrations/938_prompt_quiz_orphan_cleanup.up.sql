-- Remove prompt-quiz rows whose workspace no longer exists (RUYI-185 QA S5).
--
-- The workspace-delete query gained the quiz cleanup CTEs at the source, but
-- the sqlc artifact was committed stale: the delete the server actually runs
-- never included prompt_quiz_result / prompt_quiz_item, so every workspace
-- deleted since the quiz shipped left its bank and measurements behind as
-- orphan rows. The query source is authoritative and already fixed; this
-- statement removes what accumulated in databases where the incomplete delete
-- already ran.
--
-- No foreign keys exist by house rule, so the database never cascaded; the
-- results are removed before the bank for readability only. Idempotent: a
-- second run finds no workspace-less rows.
DELETE FROM prompt_quiz_result AS r
WHERE NOT EXISTS (SELECT 1 FROM workspace w WHERE w.id = r.workspace_id);

DELETE FROM prompt_quiz_item AS i
WHERE NOT EXISTS (SELECT 1 FROM workspace w WHERE w.id = i.workspace_id);
