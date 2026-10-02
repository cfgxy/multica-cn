# RUYI-359 Phase 1 审计附件：9xx Migration 全量审计、规整映射与 ledger 同步方案

审计基线：`origin/main` @ `fb42ab58b46de24dcae64459647f5671d88d57be`（2026-10-02 11:28:15 +0800）。
本附件为只读审计产物，无任何代码或环境变更。

## 1. 执行机制事实（设计前提，来自源码核验）

- 迁移发现与排序：`server/internal/migrations/migrations.go` 按文件系统 glob `*.up.sql` / `*.down.sql` 词法排序执行；ledger 表 `schema_migrations(version, applied_at)`，version 记录**完整 stem**（如 `916_prompt_version`），执行器以 `INSERT INTO schema_migrations (version)` 记账。
- readiness 校验要求**全部** up 版本都在账本内（防乱序迁移漏报），因此账本改写后任何一个库只要将要跑新树，就必须词干齐全。
- lint（`migrations_lint_test.go`）：stem 必须成对 up/down；数字前缀唯一（冻结的历史重复前缀清单除外）；新迁移前缀必须 > 148。**无连续性/缺口 lint**——规整后留缺口、留退役号均可通过。
- 仓库既有硬规则：每条 `CREATE INDEX CONCURRENTLY` 必须独立单语句迁移文件；例外先例是 914——**同一文件内新建表的索引允许非并发内联构建**（表在迁移事务提交前对外不可见）。
- 重编号先例：RUYI-221（`server/cmd/migrate/oauth-migration-renumber.md` + `repair_oauth_migration_ledger.sql`），事务内改写词干、保留 `applied_at`、schema 对象存在性守卫、幂等、禁 down/up。

## 2. 900–973 全量审计清单（72 stem）

类型缩写：T=建表/表组，C=加列，I=索引（CONCURRENTLY），D=数据回填/清理，X=表删除/词法收缩。

| stem | 类型 | schema 语义 | 判定 |
| --- | --- | --- | --- |
| 900_agent_webhooks | T | agent_webhook 表 + 内联索引（自包含） | 保留 |
| 901_project_instructions | C | project.instructions 列 | 保留 |
| 902_user_admin_state | T+C | user 管理列 + admin_audit_log 表 | 合并：吸收 903 |
| 903_admin_audit_log_actor_index | I | admin_audit_log actor 索引 | 并入 902（表在本组新建） |
| 904_execution_profile | T+C | execution_profile + entry 两表 + workspace.active_execution_profile_id 列 | 合并：吸收 905/906 |
| 905_execution_profile_name_index | I | profile (workspace_id,name) 唯一索引 | 并入 904 |
| 906_execution_profile_entry_index | I | entry (profile_id,agent_id) 唯一索引 | 并入 904 |
| 907_agent_session_context_gate | C | agent 会话门限两列 | 保留（与 908 不同表，非同一原子变更） |
| 908_task_usage_context_tokens | C | task_usage.context_tokens | 保留 |
| 909 | — | 缺口（见 §3） | 封存 |
| 910_marketplace_listing | T | marketplace_listing 表 | 合并：吸收 911–913 |
| 911_marketplace_listing_name_index | I | (kind,name_key) 全局唯一索引 | 并入 910 |
| 912_marketplace_listing_discovery_index | I | 发现列表部分索引 | 并入 910 |
| 913_marketplace_listing_source_index | I | 来源工作区索引 | 并入 910 |
| 914_marketplace_prompt | T+C | marketplace_prompt_version + workspace_prompt_install 两表 + agent/squad 列 + 5 内联索引 | 保留（发布时已自包含） |
| 915_task_usage_run_stats | C | task_usage turns/compactions/max_context_tokens | 保留 |
| 916_prompt_version | T | prompt_version 表（索引显式后置） | 合并：吸收 917/919–921/922/937 |
| 917_agent_task_queue_prompt_versions | C | agent_task_queue.prompt_versions 列 | 并入 916 |
| 919_prompt_version_scope_version_index | I | (scope,scope_id,version) 唯一索引 | 并入 916 |
| 920_prompt_version_scope_created_index | I | 历史列表索引 | 并入 916 |
| 921_prompt_version_workspace_index | I | 工作区审计索引 | 并入 916 |
| 922_prompt_version_v1_backfill | D | 四类实体 v1 基线回填（幂等 NOT EXISTS 守卫） | 并入 916 |
| 923_runtime_profile_add_deerflow_zcode | C+D | protocol_family 白名单扩展 + 数据迁移（含 down 备份表机制） | 保留（高风险数据迁移，不动） |
| 924_task_message_is_error | C | task_message.is_error | 保留 |
| 925_prompt_quality_rollup | T | 质量日汇总 + 困惑度评分 + 水印三表 | 合并：吸收 926–928 |
| 926_prompt_quality_daily_scope_day_index | I | 仪表盘读形索引 | 并入 925 |
| 927_prompt_quality_daily_workspace_index | I | 工作区清理索引 | 并入 925 |
| 928_prompt_perplexity_score_workspace_index | I | 评分表工作区索引 | 并入 925 |
| 929_prompt_quiz | T | quiz 题库/结果/水印三表 | 合并：吸收 930–936/938/956 |
| 930_prompt_quiz_result_baseline_index | I | 基线对比索引 | 并入 929 |
| 931_prompt_quiz_result_batch_index | I | 批次进度索引 | 并入 929 |
| 932_prompt_quiz_item_workspace_index | I | 题库列表索引 | 并入 929 |
| 933_prompt_quiz_outcome_answered | X+D | outcome 词法收窄 passed→answered | 并入 929（文件内空表 UPDATE 空转，结果约束等价） |
| 934_prompt_quiz_result_runtime | C | 结果表 runtime_id/run_model 列 | 并入 929 |
| 935_prompt_quiz_item_rubric | C | 题库 rubric 列 | 并入 929 |
| 936_prompt_quiz_result_item_index | I | 题目维度索引 | 并入 929 |
| 937_prompt_version_v1_gap_backfill | D | v1 基线缺口回填（幂等） | 并入 916 |
| 938_prompt_quiz_orphan_cleanup | D | 孤儿 quiz 行清理（DELETE，down 本为 no-op） | 并入 929（文件内空表 DELETE 空转） |
| 939_oauth_client | T | oauth_client 表（曾用号 924/925、929/930） | 合并：吸收 940 |
| 940_oauth_client_client_id_index | I | client_id 唯一索引 | 并入 939 |
| 941_skill_version | T+D | skill_version 表 + v1 回填 | 合并：吸收 942 |
| 942_skill_version_identity_index | I | (skill_id,version) 唯一索引 | 并入 941 |
| 943_proposal | T | proposal 表（预言池，后被 957 删除） | 合并：吸收 944/955 |
| 944_proposal_workspace_status | I | proposal 列表索引 | 并入 943 |
| 945_knowledge | T | knowledge_dir/entry/scan_batch 三表 | 合并：吸收 946–948/951–953 |
| 946_knowledge_dir_workspace | I | dir 工作区索引 | 并入 945 |
| 947_knowledge_entry_identity | I | entry 身份唯一索引 | 并入 945 |
| 948_knowledge_scan_batch_dir | I | 批次目录索引 | 并入 945 |
| 949_issue_run_suppressed | C | issue.run_suppressed 两列 | 保留 |
| 950_knowledge_daemon_execution | C | knowledge_dir daemon 两列 + proposal.transfer_state | 保留（受 943 建表→957 删表生存期约束，不可前移） |
| 951_knowledge_dir_ws_path_unique | I | (workspace_id,path) 部分唯一索引 | 并入 945 |
| 952_knowledge_dir_ultimate_active_unique | I | 单 ultimate 部分唯一索引 | 并入 945 |
| 953_knowledge_dir_daemon_idx | I | daemon 认领索引 | 并入 945 |
| 954_project_resource_local_dir_daemon_idx | I | 存量表 project_resource 表达式部分索引 | 保留（表非本段新建，必须独立 CONCURRENTLY） |
| 955_proposal_system_dir_dedupe | I | proposal 系统提案去重表达式唯一索引 | 并入 943 |
| 956_prompt_quiz_grading | C | quiz 评分列（题库 3 列 + 结果 3 列） | 并入 929 |
| 957_legislation_e1_removal | X | 删 proposal 表 + skill_version 词法收缩 | 保留（历史删除步，须位于 950 之后） |
| 958_prompt_proposal | T | prompt_proposal 立法池表 | 合并：吸收 959 |
| 959_prompt_proposal_workspace_index | I | 列表索引 | 并入 958 |
| 960_prompt_structure_baseline | T | 结构基线表 | 合并：吸收 961 |
| 961_prompt_structure_baseline_unique | I | carrier 唯一索引 | 并入 960 |
| 962_retrospective | T | retrospective config/run/watermark 三表 | 合并：吸收 963/964 |
| 963_retrospective_run_index | I | run 列表索引 | 并入 962 |
| 964_retrospective_watermark_unique | I | 水印唯一索引 | 并入 962 |
| 965_channel_chat_run_intent | T | channel_chat_run_intent 表（PK 后置三步附加） | 合并：吸收 966–969 |
| 966_channel_chat_run_intent_id_uidx | I | id 唯一索引 | 并入 965 |
| 967_channel_chat_run_intent_pkey | C | USING INDEX 附加主键 | 并入 965 |
| 968_channel_chat_run_intent_pending_uidx | I | pending 部分唯一索引 | 并入 965 |
| 969_channel_chat_run_intent_claim_idx | I | 认领扫描索引 | 并入 965 |
| 970_runtime_skill_discovery | T | runtime_skill_discovery 表 | 合并：吸收 971 |
| 971_runtime_skill_discovery_identity_uidx | I | 身份唯一索引 | 并入 970 |
| 972_agent_task_cancel_requested | X | agent_task_queue status 词法扩展 | 合并：吸收 973 |
| 973_agent_task_cancel_attribution | C | cancel_requested_by/at 两列 | 并入 972 |

## 3. 编号缺口归因（均附 git 证据）

- **909**：废弃分支提交 `181bbbc4a`（2026-09-07，"将 fork 独有 migration 重编号至 900-909"）曾把 marketplace_plugin_listing 索引占为 `909_marketplace_plugin_listing_version_index`。该分支未合入主线；`git log origin/main -- server/migrations/909_*` 为空（主线历史上从未存在 909 文件），plugin_listing 特性本身也从未落地主线。性质：**废弃分支幽灵占号**。
- **918**：废弃分支提交 `ff4151da8`（2026-09-25 08:58，RUYI-183 阶段一）曾添加 `918_prompt_version_v1_backfill`；合入主线的提交 `0bf1dc495`（2026-09-25 09:59）直接以 `922_prompt_version_v1_backfill` 落地（两文件内容逐字节相同，已 diff 验证；让位于 919–921 索引三连）。性质：**开发期改号、合入前已跳过**。
- **勘误**：Leader 分派卡所列"956–964 缺口"系旧快照（本地 tip `cbb02639b`）误判——基线 `fb42ab58b` 上 956–964 全部存在。基线真实缺口仅 909、918 两个。
- 两缺口在任何真实环境账本中均为 0 行（见 §5 盘点），封存不复用即可，无需任何修复动作。

## 4. 规整映射表（old → new）与容量核算

### 4.1 合并映射（16 组，组内按原编号顺序纯拼接，lead 保留原名）

| new stem | 吸收成员（删除） | 释放数 |
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
| 945_knowledge | 946, 947, 948, 951, 952, 953 | 6 |
| 958_prompt_proposal | 959 | 1 |
| 960_prompt_structure_baseline | 961 | 1 |
| 962_retrospective | 963, 964 | 2 |
| 965_channel_chat_run_intent | 966, 967, 968, 969 | 4 |
| 970_runtime_skill_discovery | 971 | 1 |
| 972_agent_task_cancel_requested | 973 | 1 |

保留不动（12 个单 stem）：900、901、907、908、914、915、923、924、949、950、954、957。

### 4.2 合并方法论与等价性论证

- **纯拼接**：合并文件 = 成员 up 内容按原编号顺序拼接，不修改任何成员 DDL 语句本体；唯一机械变换是把成员内的 `CREATE [UNIQUE] INDEX CONCURRENTLY` 去掉 `CONCURRENTLY` 关键字——仅当索引所属表在同一文件内新建时允许（914 已有先例；16 组全部满足，含 941 组内回填行先于索引构建的情形：建表事务内表对外不可见，非并发构建安全）。
- **down 文件**：成员 down 按原编号**逆序**拼接，不改语句本体。
- **组间顺序不变**：合并组以 lead 编号落位，全部 lead 号 = 组内最小编号，故全树 DDL 相对顺序与原树一致。
- **跨位置成员等价性**（仅两组涉及）：916 组吸收 937——916 与 937 之间没有任何 migration 写 prompt_version 或四类业务列（逐文件核验），前移等价；929 组吸收 956——938 与 956 之间没有 migration 触及 quiz 表，前移等价。
- **数据类成员**：922/937（幂等回填）、933（空表 UPDATE 空转）、938（空表 DELETE 空转）在新库上并入后执行结果与原序列逐行等价；已应用环境不重跑（账本改写），无影响。
- **新库终极等价验证**（Phase 2 验收）：旧树与新树各从零建库，`pg_dump --schema-only` diff 必须为空。

### 4.3 被否决的替代方案

- **密集重编号**（28 个幸存 stem 重排 900–927，尾部容量可从 27 增至 72）：否决。理由：破坏 stem 编号与 Issue/历史评论的对应关系；全部 up/down 文件内部大量"see 919/920/921"、"migration 933"式注释引用失效，改动面从 32 个改写文件膨胀为全量改写+全量注释校正；账本改写从 44 行/库扩大到 72 行/库。收益仅为名义尾部容量，而 27 个尾部号按规整后"每特性 1 号"消耗速率足够长期使用。
- **合并 {950,957}**（跨特性拼接可再省 1 号）：否决。两者非同一原子变更（daemon 执行列 vs 立法池拆除），合并违背本次要固化的"同原子才合并"规则本身。

### 4.4 容量核算

- 规整前：占用 72 号（900–973，缺 909/918），最大号 973，尾部可用 974–999 = **26**。
- 规整后：占用 28 号，最大号 972，尾部可用 973–999 = **27**。
- 释放 44 个低位号：**退役封存，永不分配**（防账本歧义）；909/918 同为封存缺口。
- 实际收益排序：① 消除"每特性 2–10 号"的拆分消耗模式（quiz 系列曾耗 10 号、knowledge 系列耗 8 号），制度化后每特性 1–2 号；② 44 个退役号封存后号段占用一目了然；③ 尾部净余量 +1（26→27）——诚实说明：本次治理的主要价值在消耗速率与可审计性，不在尾部余量。

## 5. HP Server 9xx 账本只读盘点

实例：hp-server 上唯一 multica postgres 容器 `multica-postgres-1`（pgvector/pgvector:pg17），内含 34 个业务库。查询语句（只读，逐库执行）：

```sql
SELECT version, count(*) FROM schema_migrations WHERE version LIKE '9%' GROUP BY version ORDER BY version;
```

结果汇总（15 库含 9xx 行，全部无重复词干行；其余 19 库为 900 之前老纪元 QA 库，9xx 零行，不约束本次规整）：

- **multica**（共享开发主库，唯一活跃）：**72/72 全量**，与基线完全一致。
- **oauth 词干分叉库（RUYI-221 修复未覆盖的存量债）**：
  - `multica_ruyi216base` / `multica_ruyi216qa` / `multica_ruyi216qa2` / `multica_ruyi218qa`：仍记账旧词干 `929_oauth_client` + `930_oauth_client_client_id_index`（各停在此纪元，共 30 行）；
  - `ruyi209_manifest`：记账更早的初版词干 `924_oauth_clients` + `925_oauth_clients_client_id_index`（24 行）。
- **半程库（停在 973 之前，缺尾部成员）**：`multica_qa185_mig`（至 936）、`multica_qa_ruyi285`（至 949）、`multica_ruyi287_test`（至 949）、`ruyi276_handler`（至 942）、`ruyi277_replay`（至 948）、`multica_ruyi200`/`ruyi200_dev`/`ruyi200_test`（至 923）、`multica_ruyi99_test`（至 913，且缺 907/908）。
- **孤儿卷**：`ruyi289qa_pgdata`（ruyi-289 QA 环境容器已删、卷仍在）。Postgres 崩溃恢复需写数据目录，Phase 1 只读约束下无法盘点，留 Phase 3 处置。
- **dev env 目录**（`~/.multica/dev/envs/` 下 5 个）：对应容器与数据卷均已回收，仅剩 manifest/日志，无账本态。
- 覆盖边界：hp-server 上无其他 multica postgres 实例；其他容器（shanghui/sub2api）与本项目无关。

## 6. HP Server ledger 同步方案（Phase 3 执行设计，本阶段未执行）

复用并扩展 `repair_oauth_migration_ledger.sql` 模式，新增 N:1 合并语义：

- **语句骨架**：`BEGIN; SET LOCAL lock_timeout='5s'; LOCK TABLE schema_migrations IN SHARE ROW EXCLUSIVE MODE;` → 守卫 → 改写 → `COMMIT;`
- **守卫**：
  1. 对象守卫：每个合并组校验成员对应的表（`to_regclass`）与索引（`pg_index.indisvalid`）存在，缺失即 RAISE 整体回滚（先修漂移）；
  2. 完整性守卫：**组成员行不全的库拒绝改写该组**（防半组合并造成新词干标记已应用而 schema 缺件）——半程库必须先在旧树 `migrate up` 补齐至 973 再改写。
- **改写语义**：每组 = `INSERT` 新词干（`applied_at` 取组成员 `MAX(applied_at)`，语义为"schema 达到该合并状态的时刻"）+ `DELETE` 全部成员行；新词干已存在时只删成员行；重复执行零变化。
- **oauth 遗留词干两跳映射**：`929/930_oauth_* → 939/940`（现有脚本已覆盖）与 `924/925_oauth_clients → 939/940`（需扩展），对象守卫同现有脚本。
- **单库流水线**（顺序固定）：
  1. 快照：`CREATE TABLE schema_migrations_bak_<issue> AS SELECT * FROM schema_migrations;`（或 pg_dump 该表）；
  2. oauth 遗留词干修复；
  3. 旧树（当前 main）`migrate up` 补齐至 973；
  4. 执行合并改写脚本（§4.1 映射为唯一依据）；
  5. 核验：28 个新词干齐、44 成员词干零残留、对象抽查、`SELECT count(*) FROM schema_migrations` 符合预期；
  6. 新树 `migrate up` 必须**零应用**（readiness 全命中即通过）；
  7. 快照留存，回退 = 快照恢复或反向映射脚本（同事务规则）。
- **单测**：仿 `TestRepairOAuthMigrationLedger`，隔离 schema 覆盖：新库、全量 72 行、部分应用（必须拒绝）、oauth 旧词干、重复执行、缺对象六类用例。

## 7. 9xx 编号治理规则草案（供目标 2 立法固化）

1. 900–999 是有限自维护号段；新 migration 一律从「当前最大占用号 + 1」尾部分配，选号取「磁盘现存最大 9xx」与「目标库 `schema_migrations` 已记账最大 9xx」两者更大者加一；禁止低位插号，禁止复用退役号与缺口号。
2. 同一原子 schema 变更（含其全部索引）合并为一个 migration；同文件新建表的索引用普通构建内联（914 先例）；只有给存量已承载流量的表加索引才拆独立 CONCURRENTLY 单语句文件。
3. 建 migration 前撞号三查：`git ls-files server/migrations` 磁盘占用、并行在途分支占号登记、上游 main 邻接号段；fork 独有内容不得占用上游已用或可能使用的前缀。
4. 重编号必须：维护 old→new 完整映射（`server/cmd/migrate/` 下 md+repair SQL 模式留档）、事务内改写词干、保留 applied_at、每个受影响环境逐一执行并留快照；禁止以 down/up 重放适配重编号。
5. 纯编号规整与 schema 实质变更严格分离：规整 PR 不得含任何 DDL 语义变化；等价性验收 = 新旧树从零建库 `pg_dump --schema-only` diff 为空。
6. 数据回填/清理类 migration 必须幂等（NOT EXISTS 守卫或可识别标记），其编号消耗与 DDL 同价，纳入号段预算。
7. 分支废弃时其占号不回收：缺口一律封存，不做补占。

## 8. Phase 2 工作量与风险预告（供实现分派卡参考）

- 文件面：新增/改写 32 个文件（16 组 × up/down），删除 88 个文件（44 成员 × 2），全部在 `server/migrations/` 边界内；另需扩展 `server/cmd/migrate/` repair SQL 与测试。
- 代码引用面：11 个文件引用 9xx stem（`server/cmd/migrate/` 下 5 个测试/文档、`internal/handler/` 3 个测试、`internal/testutil/migrationguard_test.go`、`pkg/agent/claude_transcript_test.go`），Phase 2 需同步更新。
- 风险：① 账本改写前必须先统一 oauth 分叉词干，否则旧树补齐步骤在 4+1 个库上失败；② 半程库"先补齐再改写"顺序不可颠倒；③ 免疫验证必须包含"新树 migrate up 零应用"断言，不能只看改写后行数。
