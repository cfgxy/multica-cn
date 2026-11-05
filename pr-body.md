## 摘要
- 完全无标记的旧载体继续按活动条款集重建 Diff。
- 部分、畸形或版本漂移的快照，以及含非活动条款的快照改为明确阻断。
- 增加 marker 契约与预览 API 回归覆盖。

## 验证
- `env -u MULTICA_TOKEN -u MULTICA_AGENT_ID -u MULTICA_ISSUE_ID -u MULTICA_WORKSPACE_ID -u MULTICA_TASK_ID -u MULTICA_RUN_ID -u MULTICA_DAEMON_ID go -C server test ./pkg/clausemark -count=1 -v`
- `env -u MULTICA_TOKEN -u MULTICA_AGENT_ID -u MULTICA_ISSUE_ID -u MULTICA_WORKSPACE_ID -u MULTICA_TASK_ID -u MULTICA_RUN_ID -u MULTICA_DAEMON_ID go -C server vet ./pkg/clausemark ./internal/handler`
- `env -u MULTICA_TOKEN -u MULTICA_AGENT_ID -u MULTICA_ISSUE_ID -u MULTICA_WORKSPACE_ID -u MULTICA_TASK_ID -u MULTICA_RUN_ID -u MULTICA_DAEMON_ID go -C server test ./internal/handler -run '^(TestClauseReviseKeepsCarrierStaticWithoutMarker|TestClauseRevisePreviewRejectsInvalidLegacyCarrierSnapshot)$' -count=1 -v`

最后一项因本机 `127.0.0.1:5432` PostgreSQL 不可达而按既有 `TestMain` 契约跳过，未计为通过；未进行真实 `CLS10000002` 数据或 Electron 操作验证。

Closes RUYI-892
