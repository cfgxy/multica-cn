**背景（写给未跟全程的读者）** 本 Issue 修复题库批次恢复与评分归因，交付需要确保 PR #519 的实际分支包含本轮修订。

**结论** 已安全合并 PR source 的并行改动，解决四个 locale 的无引用 `refresh` 键冲突并推送至 PR #519：`68e236362f068308d96008dc29b7d65cd496792f`。

**范围** 相对 `origin/main`，本单包括队列恢复、评分状态展示、归因 marker 收敛、兼容旧 batch 读取，以及 `995` 批次索引组。`CLAUDE.md` 未包含在提交中。

**验证与限制** sqlc 生成、Go vet 通过；定向 handler 测试因本机 PostgreSQL 不可达而只完成编译并跳过 DB 用例。推送前 `multica worktree preflight` 通过。