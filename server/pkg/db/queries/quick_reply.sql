-- RUYI-435: workspace quick replies.
--
-- Read path is open to any workspace member (the composer menu needs it);
-- write authorization (owner/admin) is enforced in the handler, mirroring
-- the issue status catalog. Every read is scoped by workspace_id, which is
-- also the isolation boundary between workspaces.

-- name: ListQuickReplies :many
SELECT * FROM quick_reply
WHERE workspace_id = $1
ORDER BY position ASC, created_at ASC;

-- name: GetQuickReply :one
SELECT * FROM quick_reply
WHERE id = $1 AND workspace_id = $2;

-- name: CreateQuickReply :one
INSERT INTO quick_reply (workspace_id, name, content, position)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateQuickReply :one
UPDATE quick_reply SET
    name = COALESCE(sqlc.narg('name'), name),
    content = COALESCE(sqlc.narg('content'), content),
    position = COALESCE(sqlc.narg('position'), position),
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- :one RETURNING id so the handler distinguishes pgx.ErrNoRows (→ 404) from
-- infrastructure errors (→ 500), and avoids a TOCTOU precheck.
-- name: DeleteQuickReply :one
DELETE FROM quick_reply
WHERE id = $1 AND workspace_id = $2
RETURNING id;

-- No foreign keys by project rule, so workspace teardown cleans up here.
-- name: DeleteQuickRepliesForWorkspace :exec
DELETE FROM quick_reply WHERE workspace_id = sqlc.arg('workspace_id')::uuid;

-- name: GetMaxQuickReplyPosition :one
SELECT COALESCE(MAX(position), -1)::float8 AS max_position FROM quick_reply
WHERE workspace_id = $1;

-- Single-row position write for ReorderQuickReplies: the handler assigns
-- 0..n-1 in payload order inside one transaction. Scoped by (id,
-- workspace_id) so a stale id from another workspace can never move.
-- :execrows so the handler can verify the payload covered exactly the
-- workspace's rows (a stale id leaves the count short → 400).
-- name: MoveQuickReply :execrows
UPDATE quick_reply SET position = $2, updated_at = now()
WHERE id = $1 AND workspace_id = $3;

-- Idempotent seed of the default quick replies (RUYI-435/RUYI-586 owner
-- specs: names and bodies are canonical — they must match the issue
-- descriptions verbatim; new defaults append after the existing ones).
-- Safe to call concurrently during a rolling deploy: the unique
-- (workspace_id, name) index makes a losing racer a no-op rather than an
-- error, and an admin-edited row (name kept, content changed) is never
-- overwritten because the whole INSERT is skipped on conflict.
-- name: SeedQuickReplies :exec
INSERT INTO quick_reply (workspace_id, name, content, position)
VALUES
    (sqlc.arg('workspace_id')::uuid, '解决 PR 冲突', '请基于最新 main 处理当前 PR 冲突，保留有效改动；处理完成后重新执行必要验证并更新 PR。', 0),
    (sqlc.arg('workspace_id')::uuid, '继续未完成工作', '请基于当前已有成果继续推进，仅完成尚未完成的部分，不要重复已经完成的工作；完成后给出结果和验证证据。', 1),
    (sqlc.arg('workspace_id')::uuid, '补测试 / QA', '请补齐当前 Issue 所需的实际测试 / QA 验证，并附上可核验的测试结果或证据；确认通过后再进入结单。', 2),
    (sqlc.arg('workspace_id')::uuid, '确认并结单', '请核对当前 Issue 的全部要求是否已经完成且无遗漏；确认满足验收要求后完成结单。', 3),
    (sqlc.arg('workspace_id')::uuid, '检查遗留事项', '请检查当前 Issue 是否还有未完成事项、待我决策事项，以及未提交或未合入的代码；如有请逐项列出，如无请明确确认。', 4),
    (sqlc.arg('workspace_id')::uuid, 'Dry Run + 深度 Review', '执行完整思想实验 / Dry Run，逐条覆盖关键用户路径、状态变化和边界场景；随后进行深度 Code Review，重点检查状态一致性、异常/竞态、回归风险及测试覆盖。发现问题则修复并补测；若 Dry Run + 深度 Review 均无问题，则可按 QA PASS 收口。', 5)
ON CONFLICT DO NOTHING;
