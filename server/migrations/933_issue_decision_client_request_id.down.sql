-- Rollback for 933: drop the idempotency key index before the column that
-- backs it. Replays already recorded in client systems lose their server
-- anchor after this — same-key retries will create fresh cards again, which
-- is the pre-RUYI-514 behavior.
DROP INDEX IF EXISTS uidx_issue_decisions_client_request;
ALTER TABLE issue_decisions DROP COLUMN IF EXISTS client_request_id;
