## OAuth 迁移编号与既有环境恢复

RUYI-221 的合并态 CI 暴露主线 OAuth 与 prompt-quiz 的 `929/930` 编号冲突。
以最新主线和共享开发库账本的最大编号 `938` 为依据，仅把 OAuth 迁移调整为
`939_oauth_client`、`940_oauth_client_client_id_index`。表结构与索引定义不变，
prompt-quiz 的文件不变；并发索引失败清理注册同步使用新编号。

### 全新数据库

直接按项目环境管理流程执行 `migrate up`，按新文件名记账，不运行恢复脚本。

### 已应用旧编号的数据库

在每个已应用旧编号的环境分别执行。先停止该环境的新旧迁移执行者，备份
`schema_migrations` 中旧、新四个词干的记录；运行中的旧版本仍依赖旧账本，
因此仅在切换到本版本时恢复，禁止提前改写其他在用环境。

使用该环境已授权的连接配置执行：

```bash
PGDATABASE="$DATABASE_URL" psql -X -v ON_ERROR_STOP=1 \
  -f server/cmd/migrate/repair_oauth_migration_ledger.sql
```

脚本在一个事务内仅改写迁移账本：新记录不存在时 `UPDATE` 旧词干，保留
`applied_at`；新记录已存在时只删除冗余旧记录；重复执行不改变结果。旧记录
对应的 OAuth 表不存在或索引无效时整笔回滚，先处理该环境原有 schema 漂移。
不删除客户端，不重建索引，不执行 `migrate down`，不重放已应用的 DDL。

完成后核对账本只含新词干、原 OAuth 客户端行仍在、索引有效，再按环境管理
流程启动新版本。原有环境中其他历史失配记录不属于本次修复范围。

### 回退

若需要恢复旧版本，先停止迁移执行者，在事务内按同样规则反向把 `939`、`940`
映射回 `929`、`930`：旧目标记录已存在时只删冗余新记录，否则 `UPDATE` 新记录
词干。表结构和客户端数据不变；不使用 down/up 重放。执行前保存账本快照。

### 验证

编号唯一性由 `internal/migrations` 的既有 lint 覆盖；恢复脚本由
`TestRepairOAuthMigrationLedger` 在隔离 schema 中覆盖新库、旧账本、新账本、
双账本、混合账本和重复执行，断言客户端与原始应用时间不变，并断言缺表、
缺索引时账本保持原状。
