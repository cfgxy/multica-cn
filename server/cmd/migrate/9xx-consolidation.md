# 9xx Migration 规整映射与账本改写（RUYI-359 Phase 2）

本文档是 900–999 号段规整（编号合并）与 `schema_migrations` 账本改写的唯一权威映射，
取代 RUYI-359 Phase 1 审计附件中的 §4/§6/§8（Phase 1 附件其余审计结论仍然有效）。
Phase 1 Review 的返工裁定已并入本文：**953 移出 945 组吸收清单，独立保留**。

- 审计与实现基线：`fb42ab58b46de24dcae64459647f5671d88d57be`（origin/main）
- 规整前：72 个 9xx stem（最大 973，缺口 909/918）
- 规整后：**29 个 stem = 16 个合并组 lead + 13 个独立保留 stem**；最大号 972；释放 43 个低位号
- 硬约束：不引入任何 DDL 语义变化；等价性验收 = 旧树与新树各从零建库后
  `pg_dump --schema-only` diff 为空

## 1. 合并映射（16 组）

组内成员按原编号升序纯拼接到 lead 的 up 文件；down 按原编号降序拼接（lead 自身 down
最后）。lead 保留原名与原编号，全部成员文件删除（up/down 各一份，共 86 个文件删除）。

| lead（保留） | 吸收成员（删除） | 释放数 |
| --- | --- | --- |
| 902_user_admin_state | 903 | 1 |
| 904_execution_profile | 905, 906 | 2 |
| 910_marketplace_listing | 911, 912, 913 | 3 |
| 916_prompt_version | 917, 919, 920, 921, 922, 937 | 6 |
| 925_prompt_quality_rollup | 926, 927, 928 | 3 |
| 929_prompt_quiz | 930, 931, 932, 933, 934, 935, 936, 938, 956 | 9 |
| 939_oauth_client | 940 | 1 |
| 941_skill_version | 942 | 1 |
| 943_proposal | 944, 955 | 2 |
| 945_knowledge | 946, 947, 948, 951, 952 | 5 |
| 958_prompt_proposal | 959 | 1 |
| 960_prompt_structure_baseline | 961 | 1 |
| 962_retrospective | 963, 964 | 2 |
| 965_channel_chat_run_intent | 966, 967, 968, 969 | 4 |
| 970_runtime_skill_discovery | 971 | 1 |
| 972_agent_task_cancel_requested | 973 | 1 |

小计：释放 43 个编号。

## 2. 独立保留（13 个单 stem）

`900_agent_webhooks`、`901_project_instructions`、`907_agent_session_context_gate`、
`908_task_usage_context_tokens`、`914_marketplace_prompt`（发布时即自包含）、
`915_task_usage_run_stats`、`923_runtime_profile_add_deerflow_zcode`（高风险数据迁移，不动）、
`924_task_message_is_error`、`949_issue_run_suppressed`、
`950_knowledge_daemon_execution`、**`953_knowledge_dir_daemon_idx`**、
`954_project_resource_local_dir_daemon_idx`、`957_legislation_e1_removal`。

其中 953/954/971 原为「给非本文件新建的表加索引」型独立 `CONCURRENTLY` 文件；
953 建的 `idx_knowledge_dir_daemon ON knowledge_dir (daemon_id)` 依赖 **950** 添加的
`daemon_id` 列，若并入 945 组会先于 950 执行，从零建库 `migrate up` 必报列不存在
（Phase 1 Review 阻断项）。953 因此与 954 同型，独立保留，其账本行不做任何改写。

## 3. 合并机械规则

1. **纯拼接**：不改任何成员 DDL 语句本体；唯一机械变换是
   `CREATE [UNIQUE] INDEX CONCURRENTLY` → 去掉 `CONCURRENTLY`、
   `DROP INDEX CONCURRENTLY` → 去掉 `CONCURRENTLY`。
   - up 侧：仅当索引目标表在同一合并文件内新建（或列在同一文件内先添加）时允许。
     表在迁移的隐式事务提交前对外不可见，非并发构建安全（914 先例）。
   - down 侧：多语句文件以单次 Exec 执行，处于隐式事务块内，`DROP INDEX CONCURRENTLY`
     无法运行；且随后同文件即 DROP TABLE，索引随表消亡，非并发丢弃等价。
2. **文件执行机制前提**（`server/internal/migrations/migrations.go` +
   `server/cmd/migrate/main.go`）：按 glob 词法排序、整文件单次 Exec（多语句 = 一个
   隐式事务）、ledger 以完整 stem 记账、readiness 要求磁盘全部 up 版本都在账本内。
3. **组内特殊顺序**：965 组的 966（建 `channel_chat_run_intent_id_uidx` 唯一索引）→
   967（`ADD CONSTRAINT ... PRIMARY KEY USING INDEX`）顺序必须保持。
4. **数据类成员**在新库上的行为：922/937（幂等 NOT EXISTS 回填）、933（空表 UPDATE
   空转）、938（空表 DELETE 空转）并入后与原序列逐行等价；已应用环境不重跑
   （账本改写），无影响。
5. **注释订正**：合并文件内指向已删除 stem 的「migration NNN」「see NNN」式注释
   随手改指 lead stem 或「本文件」；指向独立保留 stem（如 924/925/950）的注释不动。

## 4. 跨位置吸收的等价性（共三处，均已逐文件核验）

合并组以 lead 编号落位，被吸收成员原编号大于 lead 时发生「前移」。三处：

1. **916 组吸收 937**（跨 917–936 窗口）：窗口内没有任何 migration 写 `prompt_version`
   或四类业务列（923=runtime_profile、924=task_message、925–928=质量表、929–936/938=quiz
   表）；937 自身只读 workspace/project/squad/agent、写 prompt_version。
2. **929 组吸收 956**（跨 939–955 窗口）：窗口内没有 migration 触及 quiz 三表
   （939/940=oauth、941/942=skill_version、943/944/955=proposal、945–948/951–954=knowledge
   与 project_resource、949=issue、950=knowledge 列 + proposal.transfer_state）。
3. **943 组吸收 955**（跨 950，`proposal.transfer_state` 所在文件）：955 的表达式索引
   `uidx_proposal_system_dir` 只用 `generation_snapshot->>'knowledge_dir_id'`，该列由
   943 建表自带；不引用 950 添加的 `transfer_state`，前移等价。

**953 不构成第四处**——它依赖 950 的列，见 §2，已排除。

## 5. 容量核算

- 规整前：占用 72 号（900–973，缺口 909/918），最大 973，尾部可用 974–999 = 26。
- 规整后：占用 29 号，最大 972，尾部可用 973–999 = 27。
- 释放 43 个低位号退役封存，永不复用；909/918 缺口同为封存。
- 文件面：16 组 × up/down 改写 = 32 个文件重写；43 成员 × 2 = **86 个文件删除**。

## 6. 账本改写（`repair_9xx_consolidation_ledger.sql`，本 Phase 只实现不执行）

单事务脚本，真实环境执行属 Phase 3。改写只动「身份记录」，不执行任何 schema DDL。

### 6.1 状态机与幂等（每组独立判定）

设组 = {lead L, 成员 M1..Mk}（L 即合并后 stem，与旧树同名）：

- L 在账且 0 个成员行在 → 该组已改写（或新树已应用），跳过（幂等重入）。
- L 在账且全部 k 个成员行在 → 改写：DELETE 全部成员行（L 的行与其 `applied_at` 原样保留，
  语义 = schema 达到该合并状态的时刻即 L 当初应用时刻）。
- 其余任何形态（lead 缺而成员在、成员部分在）→ RAISE 整体回滚：
  半程库必须先在旧树 `migrate up` 补齐至 973 再改写，禁止半组合并。

### 6.2 oauth 遗留词干两跳映射（先于合并改写执行）

| 旧词干（账本变体） | 统一到 |
| --- | --- |
| `929_oauth_client`、`930_oauth_client_client_id_index` | `939_oauth_client`、`940_oauth_client_client_id_index` |
| `924_oauth_clients`、`925_oauth_clients_client_id_index` | 同上 |

统一语义沿用 `repair_oauth_migration_ledger.sql`：1:1 改名、保留 `applied_at`、
对象存在性守卫（表 + 有效索引）、新旧行并存时删旧行；统一后 940 行作为 939 组成员
被 §6.1 删除。这些旧词干从未存在于主线磁盘，属于 fork 期账本变体，不受第 8 节
零残留闸门约束。

### 6.3 对象守卫

对任一「lead 或成员行在账」的组：lead/成员涉及的表必须 `to_regclass` 存在，成员索引必须
`pg_index.indisvalid`，缺失或 INVALID 即 RAISE（先修漂移再改写）。

### 6.4 单库流水线（顺序固定，写入脚本注释与本文档）

1. 快照：`CREATE TABLE schema_migrations_bak_ruyi359 AS SELECT * FROM schema_migrations;`
2. 执行 `repair_9xx_consolidation_ledger.sql`（内含 oauth 统一 → 合并改写）；
   脚本前置条件 = 该库已在**旧树**补齐至 973。
3. 核验：29 个新口径 stem 齐、43 个成员词干零残留、行数符合预期、对象抽查
   （核验 SQL 见脚本尾部注释）。
4. 新树 `migrate up` 必须**零应用**（readiness 全命中即通过）。
5. 回退 = 快照恢复，或按反向映射（INSERT 成员行 / DELETE lead 行）构造对偶脚本。

注意：改写前的旧账本库直接跑新树 `migrate up` 也只会零应用（29 个 lead/保留 stem
都在旧账本内、readiness 通过），不会重放 DDL；改写的目的是把账本收敛到与新树
一一对应，消除 43 个幽灵行。

## 7. 代码引用面（Phase 2 已同步订正）

- `server/cmd/migrate/main.go`：`concurrentIndexCleanups` 的 33 个已删除索引成员键
  收敛为 15 个 lead 键（值改为该组全部索引；953/954 键保留不动），钩子语义不变
  （重试前清掉 INVALID 残留索引）。
- `server/internal/testutil/migrationguard_test.go`：夹具 stem 换为幸存 stem。
- 全仓旧词干零残留为机械闸门（见 §8），不以任何清单为准。

## 8. 零残留闸门

对 §1 表中 43 个成员 stem 逐个 `git grep` 全仓，唯一允许命中位置：
`server/cmd/migrate/9xx-consolidation.md`（本文件的映射表）、
`server/cmd/migrate/repair_9xx_consolidation_ledger.sql`（改写的 DELETE 输入）、
`server/cmd/migrate/migrate_9xx_consolidation_ledger_test.go`（改写测试的夹具数据）。
其余任何文件命中即 FAIL。oauth 遗留词干（`929_oauth_client` 等）不是成员 stem，
不受本闸门约束。

成员 stem 中的 `940_oauth_client_client_id_index` 有双重身份：既是本单被吸收的
成员，又是更早 oauth 账本统一（`repair_oauth_migration_ledger.sql` 及其测试与
设计文档）的改名目标。后三处命中属 oauth 统一的既有产物，合并改写不得回写那批
文件，豁免于本闸门；除此之外 42 个成员 stem 全仓零残留（Phase 2 已核）。

## 9. 验证方案（Phase 2 交付时执行）

1. 旧树（基线）与新树各从零建库 `migrate up`，`pg_dump --schema-only` diff 为空。
2. 新树全量 `migrate down --yes` 到空账本，再 `migrate up` 全量重放成功（down 路径冒烟）。
3. 并发迁移冒烟：两个 `migrate up` 同时打同一空库，advisory lock 串行化，零错误。
4. `make sqlc` 生成物 diff 干净（本单不触碰 queries/schema）。
5. Go 测试：`server/cmd/migrate`、`server/internal/migrations`、`server/internal/testutil`
   全绿（隔离库执行，`env -u` 清 `MULTICA_*`）。
6. §8 零残留闸门 grep 输出留证。

执行注记（Phase 2 实测）：第 2 项 down 链在 `950_knowledge_daemon_execution.down`
处中断——基线既有缺陷：957.down 的 best-effort 重建漏掉 `transfer_state` 列
（957/950 两文件本单未改动，基线树同位同错已实证）。在验证环境给 `proposal` 补
该列后 down 链跑完至 `001_init`、账本清零，全部合并组的 down 回放验证通过；
修复该缺陷不属本单范围，交 Leader 分流。down 后的 `migrate up` 重放未单独执行：
与第 1 项从零建库等价（已双库验证），不重复消耗。
