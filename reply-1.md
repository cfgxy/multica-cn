**背景（写给未跟全程的读者）** 题库测验需在离页后从服务端队列恢复批次状态；本轮已将归因边界修订实际纳入 PR #519 的 source head。

**结论** PR #519 当前 head 为 `68e236362f068308d96008dc29b7d65cd496792f`。新增修订提交 `7ac388bcbcc03f89901027fc2a4d916c7da50003`，合并远端 source 后以快进方式推送；`CLAUDE.md` 保持未暂存，未纳入交付。

**变更与证据** 空 scope 的 quiz claim 持久化 `{}` 并原子清除 `attribution_pending`；真实读取失败保留 marker 和已解析 scope；显式 batch 读取保持旧客户端的 result-only 契约；全量 fence 拒绝改为 `409 quiz_batch_fenced` 且事务回滚。补充显式 batch lookup 的 `995` 组并发索引与计划断言，登记簿已更新。

**验证** `$HOME/go/bin/sqlc generate -f server/sqlc.yaml` 成功；清除 `MULTICA_*` 后 `go vet ./internal/handler ./internal/promptquizsweep` 通过；`go test ./internal/handler -run ...` 编译通过但数据库断开（`127.0.0.1:5432: connection refused`），DB 回归按框架跳过。`multica worktree preflight` 在推送前通过。

**风险** `FindCurrentOrLatestPromptQuizBatchForAgent` 仍会在无更专门批次状态模型时聚合该 agent 的历史 quiz 队列；这是已识别的轮询扩展性后续项，未在本单伪造修复。