# 9xx Migration 域合并与连续重编号（RUYI-359 Phase 2R）

本文档是 900–999 号段规整与 `schema_migrations` 账本改写的唯一权威映射。
它取代本文档的全部 Phase 2 版本（16 组合并、29 stem、释放封存口径）：
Phase 2 的 29-stem 树从未进入任何真实环境（PR 未合并、repair 未执行），
真实世界的起点只有「原始 72 stem 旧树」与「全新库」两种。

- 审计与实现基线：`fb42ab58b46de24dcae64459647f5671d88d57be`（origin/main）
- 规整前：72 个 9xx stem（900–973，缺口 909/918，最大 973）
- 规整后：**17 个域迁移，900 起连续无洞，最大 916，尾部可用 917–999 = 83**
- 等价性验收唯一判据（Owner 指令）：最终 schema 一致——新树与原始基线树
  各从零建库后 `pg_dump --schema-only` diff 为空。DDL 允许重写。

## 1. 单跳映射（原始 72 → 最终 17）

按最终文件执行序排列；`←` 右侧为该文件 embody 的全部原始词干（up 按原编号
升序拼接，down 逆序）。

| 最终 stem（域） | 吸收的原始词干 | 数 |
| --- | --- | --- |
| `900_agent_webhooks`（直通） | — | — |
| `901_project_instructions`（直通） | — | — |
| `902_user_admin_state`（用户管理） | 903_admin_audit_log_actor_index | 1 |
| `903_execution_profile`（执行档案） | 904_execution_profile, 905_execution_profile_name_index, 906_execution_profile_entry_index | 3 |
| `904_runtime_profile_add_deerflow_zcode`（运行时档案数据迁移） | 923_runtime_profile_add_deerflow_zcode | 1 |
| `905_agent_session_context_gate`（会话上下文门） | 907_agent_session_context_gate | 1 |
| `906_task_usage`（任务用量） | 908_task_usage_context_tokens, 915_task_usage_run_stats, 924_task_message_is_error | 3 |
| `907_marketplace`（市场） | 910_marketplace_listing, 911_marketplace_listing_name_index, 912_marketplace_listing_discovery_index, 913_marketplace_listing_source_index, 914_marketplace_prompt | 5 |
| `908_prompt`（提示词域：版本/质量/quiz/提案/结构基线） | 916_prompt_version, 917_agent_task_queue_prompt_versions, 919_prompt_version_scope_version_index, 920_prompt_version_scope_created_index, 921_prompt_version_workspace_index, 922_prompt_version_v1_backfill, 925_prompt_quality_rollup, 926_prompt_quality_daily_scope_day_index, 927_prompt_quality_daily_workspace_index, 928_prompt_perplexity_score_workspace_index, 929_prompt_quiz, 930_prompt_quiz_result_baseline_index, 931_prompt_quiz_result_batch_index, 932_prompt_quiz_item_workspace_index, 933_prompt_quiz_outcome_answered, 934_prompt_quiz_result_runtime, 935_prompt_quiz_item_rubric, 936_prompt_quiz_result_item_index, 937_prompt_version_v1_gap_backfill, 938_prompt_quiz_orphan_cleanup, 956_prompt_quiz_grading, 958_prompt_proposal, 959_prompt_proposal_workspace_index, 960_prompt_structure_baseline, 961_prompt_structure_baseline_unique | 25 |
| `909_oauth_client`（OAuth 客户端） | 939_oauth_client, 940_oauth_client_client_id_index | 2 |
| `910_proposal`（提案） | 943_proposal, 944_proposal_workspace_status, 955_proposal_system_dir_dedupe | 3 |
| `911_knowledge`（知识域：目录/条目/扫描批次/守护执行） | 945_knowledge, 946_knowledge_dir_workspace, 947_knowledge_entry_identity, 948_knowledge_scan_batch_dir, 950_knowledge_daemon_execution, 951_knowledge_dir_ws_path_unique, 952_knowledge_dir_ultimate_active_unique, 953_knowledge_dir_daemon_idx, 954_project_resource_local_dir_daemon_idx | 9 |
| `912_issue_run_suppressed`（Issue 运行抑制） | 949_issue_run_suppressed | 1 |
| `913_skill`（技能域：版本/legislation 下线/运行时发现） | 941_skill_version, 942_skill_version_identity_index, 957_legislation_e1_removal, 970_runtime_skill_discovery, 971_runtime_skill_discovery_identity_uidx | 5 |
| `914_retrospective`（复盘） | 962_retrospective, 963_retrospective_run_index, 964_retrospective_watermark_unique | 3 |
| `915_channel_chat_run_intent`（频道运行意图） | 965_channel_chat_run_intent, 966_channel_chat_run_intent_id_uidx, 967_channel_chat_run_intent_pkey, 968_channel_chat_run_intent_pending_uidx, 969_channel_chat_run_intent_claim_idx | 5 |
| `916_agent_task_queue`（任务队列取消归属） | 972_agent_task_cancel_requested, 973_agent_task_cancel_attribution | 2 |

覆盖核算：69 个改写来源 + 3 个直通 stem（900/901/902）= 72，无遗漏、无重复。
另有 4 个 fork 期 oauth 账本变体（`929_oauth_client`、`930_oauth_client_client_id_index`、
`924_oauth_clients`、`925_oauth_clients_client_id_index`）从未存在于主线磁盘，
由 repair 脚本 Stage 1 一并单跳收敛到 `909_oauth_client`（见 §5.2）。

## 2. 文件改写机械规则

1. **纯拼接**：不改任何成员 DDL 语句本体；唯一机械变换是
   `CREATE [UNIQUE] INDEX CONCURRENTLY` → 去掉 `CONCURRENTLY`、
   `DROP INDEX CONCURRENTLY` → 去掉 `CONCURRENTLY`。
   - up 侧：仅当索引目标表（或列）在同一合并文件内先建时允许；表在迁移的
     隐式事务提交前对外不可见，非并发构建安全（914 先例）。
   - down 侧：多语句文件以单次 Exec 执行，处于隐式事务块内，无法运行
     `DROP INDEX CONCURRENTLY`；且随后同文件即 DROP TABLE，索引随表消亡。
2. **执行机制前提**（`server/internal/migrations/migrations.go` +
   `server/cmd/migrate/main.go`）：按 glob 词法排序、整文件单次 Exec（多语句 =
   一个隐式事务）、ledger 以完整 stem 记账、readiness 要求磁盘全部 up 版本都在账本内。
3. **组内顺序** = 原编号升序（up）/ 降序（down），Phase 2 已验证的特殊顺序保持：
   966（唯一索引）→ 967（`ADD CONSTRAINT ... PRIMARY KEY USING INDEX`）；
   953 在 950 之后（`idx_knowledge_dir_daemon` 依赖 950 添加的 `daemon_id` 列——
   该依赖随同文件化从跨文件约束变为文件内顺序，Phase 1 Review 阻断项就此消除）。
4. **数据类成员**在新库上的行为不变：922/937（幂等 NOT EXISTS 回填）、933/938
   （空表空转）并入后与原序列逐行等价；已应用环境不重跑（账本改写），无影响。
5. **957 down 重生成（RUYI-360 缺陷随重生成消灭）**：原 957 down 的 best-effort
   重建漏掉 `proposal.transfer_state`（950 添加）与两个 proposal 索引（943/944/955
   建立）。回滚梯级现按 913 → 911 → 910 下降，重生成的 down 按「刚过 911 之后」
   的 proposal 形状重建（含 `transfer_state` 与 `idx_proposal_workspace_status`、
   `uidx_proposal_system_dir`），供后续梯级正常消费。原 957 up 删除的行仍按设计
   不可恢复（dev 期实验数据）。
6. **923 → 904 改名不动语义**：备份表 `runtime_profile_family_923_backup` 的名字
   与全部备份/恢复逻辑原样保留——它是库内数据工件而非 stem，改名会破坏已存在
   该备份表的库的恢复路径。
7. **注释订正**：合并文件头注明来源词干映射与本文档位置；指向已删除 stem 的
   「migration NNN」式注释改指本文件。

## 3. 等价性论证与验收

- **内容层**：每个新文件的有效行（非空非注释）多重集 = 其来源文件在改写前树
  （`350d20e`）的多重集，经且仅经 §2 声明的两种变换（957 down 例外，为重生成件）。
- **顺序层**：文件内相对顺序保持原状；跨文件前移逐项核验依赖——
  923→904、907→905、908/915/924→906、oauth 939/940→909、949→912、962→914、
  965→915、972→916 均只依赖基线表（<900 已建）；957 的 DROP proposal 现位于
  910（建表）与 911（加列）之后，净效果与原序列一致（终态无 proposal 表）。
- **Schema 层（唯一验收判据）**：新树从零建库 `migrate up` 一次通过（拓扑依赖
  保持的机械证明），`pg_dump --schema-only` 与原始基线 `fb42ab5` 树从零建库
  逐行 diff 为空。

## 4. 容量核算

- 规整前：占用 72 号（900–973，缺口 909/918），最大 973，尾部可用 974–999 = 26。
- 规整后：占用 **17 号（900–916 连续无洞）**，最大 **916**，尾部可用 917–999 = **83**。
- 文件面：17 个 stem × up/down；相对原始基线删除 55 个 stem × 2 = 110 个文件
  （72 − 17 = 55）。

## 5. 账本改写（`repair_9xx_consolidation_ledger.sql`，本 Phase 只实现不执行）

单事务脚本，真实环境执行属 Phase 3。改写只动「身份记录」，不执行任何 schema DDL。

### 5.1 单跳状态机（每组独立判定，`fold` 标志区分两类组）

- **改名组**（fold=false，目标 stem 是新 stem，共 14 组）：
  - 目标行在账且 0 个来源行在 → 已改写（或新树已应用），跳过（幂等重入）。
  - 目标行缺且全部 k 个来源行在 → 改写：`UPDATE sources[1] → target`
    （保留最小原编号来源的 `applied_at`），`DELETE` 其余来源行。
  - 目标行缺且 0 个来源行在 → 全新库，跳过。
  - 其余任何形态（来源部分在、目标与来源并存）→ RAISE 整体回滚。
- **折叠组**（fold=true，仅 `902_user_admin_state`：目标 stem 即旧树原词干，
  行本就在账，来源行是冗余身份）：目标行在账 → `DELETE` 来源行；
  目标行缺 → RAISE（902 缺而 903 在属破碎历史，不得改写）。

### 5.2 oauth fork 期变体（Stage 1，先于 Stage 2）

§1 末列出的 4 个变体统一到 oauth 组主线来源 `939/940`（1:1 改名、保留
`applied_at`、对象守卫、主线行已在时删变体行），随后由 §5.1 的
`909_oauth_client` 改名组一并单跳收敛。变体词干从未存在于主线磁盘，
不受 §7 零残留闸门约束。

### 5.3 对象守卫

对任一「目标行在账」的组（改写前在账或本轮改写后在账）：组内涉及的表必须
`to_regclass` 存在，组内索引必须 `pg_index.indisvalid`，缺失或 INVALID 即
RAISE（先修漂移再改写）。守卫清单随组内表/索引面升级为域级全量。

### 5.4 支持的起始状态

- 旧树补齐至 973 的库（HP Server、自托管存量）——单跳直达，无中跳。
- 已按 17-stem 新树应用的库 / 全新库——幂等无操作。
- Phase 2 的 29-stem 状态无真实环境，不作为输入。

### 5.5 单库流水线（顺序固定，Phase 3 执行）

1. 快照：`CREATE TABLE schema_migrations_bak_ruyi359 AS SELECT * FROM schema_migrations;`
2. 执行 `repair_9xx_consolidation_ledger.sql`；前置条件 = 该库已在旧树补齐至 973。
3. 核验：17 个权威 stem 齐、72 个原始词干零残留、行数符合预期（核验 SQL 见脚本尾部注释）。
4. 新树 `migrate up` 必须**零应用**（readiness 全命中即通过）。
5. 回退 = 快照恢复，或按反向映射（INSERT 来源行 / DELETE 目标行）构造对偶脚本。

注意（与 Phase 2 口径相反，顺序不可倒置）：改写前的旧账本库直接跑新树
`migrate up` 时，readiness 发现磁盘 17 个 stem 大多不在账本内，会把它们当
missing 逐个**重放 DDL**（撞已存在的表/索引而失败，或对可重建对象造成重复
执行）。Phase 2 方案里 lead 行与旧账本同名故可零应用；Phase 2R 的目标 stem
几乎全是新 stem，**必须先改写账本、再运行新树**。

## 6. 代码引用面（Phase 2R 已同步）

- `server/cmd/migrate/main.go`：`concurrentIndexCleanups` 键收敛为 17-stem
  口径（域组键携带该域全部索引），钩子语义不变（重试前清掉 INVALID 残留索引）。
- `server/internal/testutil/migrationguard_test.go`：夹具 stem 换为新 stem。
- RUYI-221 oauth 三件（`repair_oauth_migration_ledger.sql`、其测试、
  `oauth-migration-renumber.md`）删除：其变体收敛语义由 §5.2 吸收，避免留下
  把 `939/940` 当现役 stem 的过期入口；历史操作记录以 RUYI-221 Issue 为准。

## 7. 零残留闸门

对 §1 表中 72 个原始词干逐个 `git grep` 全仓，唯一允许命中位置：
`server/cmd/migrate/9xx-consolidation.md`（本文件的映射表）、
`server/cmd/migrate/repair_9xx_consolidation_ledger.sql`（改写脚本的映射输入）、
`server/cmd/migrate/migrate_9xx_consolidation_ledger_test.go`(改写测试的夹具数据）。
其余任何文件命中即 FAIL（Phase 2R 已核，见 Issue 回报）。

## 8. 验证方案（Phase 2R 交付时执行，结果见 Issue 回报）

1. 新树从零建库 `migrate up` 一次通过；`pg_dump --schema-only` 与 `fb42ab5`
   树从零建库 diff 为空（隔离库双库执行）。
2. 新树全量 `migrate down --yes` 至 `001_init`、账本清零——**957 down 缺陷
   已随重生成消灭，全程无需手工补列**；down 后 `migrate up` 全量重放成功。
3. `make sqlc` 生成物 diff 干净（本单不触碰 queries/schema）。
4. Go 测试：`server/cmd/migrate`、`server/internal/testutil` 全绿
   （隔离库执行，`env -u` 清 `MULTICA_*`）。
5. §7 零残留闸门 grep 输出留证。
