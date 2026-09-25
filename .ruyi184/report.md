阶段状态：STAGE_COMPLETE

[@蔡小星](mention://agent/3478a149-34cd-49b4-bbc4-1b789005fbc0)

## 结论

RUYI-184 全部代码交付完成，四次本地 commit。七维度取数管道、看板接口、「质量」Tab 前端与 D3 评分编排均已落地并通过本地全量测试。**验收标准第 6 条（D3 可复现性实测）未运行**——本地未配置大模型密钥，无法做真实评分，下方如实写明原因与解锁条件。未 push。

## 背景

本单是自进化第二阶段：把 Prompt 质量评估的七个维度（D1 注入成本、D2 纪律性、D3 规则困惑度、D4 轮次失败率、D5 重试次数、D6 错误根因、D7 一次过率）从口径定义变成可复算的落库结构，并配一个按 Prompt 版本看曲线的看板。Langfuse 是可选源，缺省全部从平台自有数据取。

## 动作与交付物

基线：`62b1dc27b040cdd24c4244c167ef76cca7146516`（本单第一批提交，已在 `origin/main` 之上；其父 `7bcf17fd6` 经 `git merge-base --is-ancestor 7bcf17fd6 origin/main` 校验退出码 0，是 origin 集成分支祖先，无分叉、无未推送 commit）。

四个 commit（完整 40 位 SHA）：

1. `62b1dc27b040cdd24c4244c167ef76cca7146516` — `task_message.is_error` 列与 daemon 写路径。按交付次序硬约束先于一切其他改动提交，因为历史数据不可回填，埋点晚一天就少一天可算的 D4。
2. `2ffe75827f1dcbdb4d42f286e931d75a4f2a733b` — 后端七维度取数管道、看板接口、D3 评分编排。
3. `147e1608154362e201d694ec9d9dbe75f5a8c3ce` — 前端「质量」Tab 与七维度卡片、四语言词表。
4. `9cff03fd8ec343d02b1d71e0fcd01162f893818c` — 修测试里 `I18nProvider` 属性名（`language` → `locale`），typecheck 发现。

分支 tip：`9cff03fd8ec343d02b1d71e0fcd01162f893818c`。

### 数据库

- 迁移 `925` 建三张表：`prompt_quality_daily`（按 scope/版本/UTC 日的六维度计数）、`prompt_perplexity_score`（D3，按运行态一行）、`prompt_quality_rollup_state`（单行水位线）。`926`/`927`/`928` 是三个并发索引，各自单文件单语句，已登记进 `server/cmd/migrate/main.go` 的 `concurrentIndexCleanups`。全部有 down 脚本。无外键。
- 关键设计：**每个维度存计数而非比率**。D4 存 `tool_results_measured` / `tool_results_error` 两列而不是一个 rate，所以「这一天的 run 全部早于 is_error 埋点」读出来是 measured=0 即「无数据」，永远不可能是 0%。D6 把失败 run 拆成 `attributable_failed_runs` 与 `excluded_failed_runs` 两列，环境/供应商类失败在结构上就进不了 Prompt 归因分母。

### 取数与编排

- `pkg/promptquality` 做六维度聚合，`pkg/promptdiscipline` 算 D2 扣分，`pkg/promptperplexity` 做 D3 评分，`pkg/taskfailure/prompt_attribution.go` 判定 failure_reason 是否属于环境/供应商类。
- `internal/promptqualityrollup` 的 Runner 按水位线找脏桶并**整桶重算**（不是增量累加），所以重复扫描收敛而不会双计；每 tick 上限 `BucketLimit = 200` 个桶。
- **本轮新发现并修复的缺口**：`Perplexity.ScoreAgentVersion` 在全仓没有任何调用方——意味着 D3 这一维度永远不会产出数据，看板上的 D3 区会恒为「未评分」。新增 `ScoreBacklog`：从 `prompt_quality_daily` 反查「有 finished_runs 但 profile 评分不全」的 agent 版本，每 tick 限 `ScoreBudget = 3` 个版本（每版本 2 次模型调用），挂在已有的 5 分钟 `rollup_prompt_quality` job 上。评分失败不拖垮 tick——六个计数维度已经落库，评不出的版本保持「未评分」，这是看板本来就会渲染的状态，不是错误。`scheduler.PromptQualityJob` 因此加了一个 `promptperplexity.Generator` 参数，`main.go` 注册时传 `h.LLM`（与 handler 共用同一个 client，未配置时 `Enabled()` 为 false，把评分关掉而不是让 job 失败）。

### D3 口径（Owner Q17）

按注入运行态分别评分：`member` 与 `leader_task` 各自组装自己的完整拼接文档、各自调一次模型、各自落一行，代码里没有任何合并或平均。测试 `TestScoreAgentVersionScoresEachProfileAgainstItsOwnDocument` 断言两个 profile 的 user prompt 不相同，且 squad 层只出现在 leader_task 文档里。

### 前端

- 「质量」做成自进化页面内的 **Tab**，不是同级路由。因此不新增路由键或页面键，`WORKSPACE_ROUTES`、`paths/consistency.test.ts` 清单、测试 mock 三处登记点都无需改动——这是上一阶段红 CI 的位置，本次从方案上绕开而非补登记。判断依据写进了 `self-evolution-page.tsx` 的文档注释。
- 三态渲染（有数据 / 无数据 / 样本不足）在 `quality-measure-card.tsx` 中显式分支，全链路无 `?? 0`。样本不足时显示「还需多少样本」而不是 0%。
- D3 下钻 Sheet 按运行态切换，两个 profile 各读各的区间与证据。
- 可选源（Langfuse）不可用时只在页脚出降级标识，七张卡不受影响。
- 四语言词表（en / zh-Hans / ja / ko）已同步，`parity.test.ts` 在 views 全量中通过。

## 证据

所有测试命令均先 `set -a && . ./.env && set +a` 加载数据库连接，再用 `env -u` 清空 `MULTICA_*`：

```
env -u MULTICA_TOKEN -u MULTICA_API_URL -u MULTICA_WORKSPACE_ID -u MULTICA_AGENT_ID \
    -u MULTICA_ISSUE_ID -u MULTICA_TASK_ID -u MULTICA_RUNTIME_ID -u MULTICA_SPACE \
    -u MULTICA_DAEMON_ID -u MULTICA_SESSION_ID <命令>
```

后端（`cd server` 后执行）：

```
go test ./internal/promptqualityrollup/... ./pkg/promptquality/... ./pkg/promptperplexity/... \
        ./pkg/promptdiscipline/... ./internal/scheduler/... ./pkg/taskfailure/... -count=1
ok  	.../internal/promptqualityrollup	0.471s
ok  	.../pkg/promptquality	0.024s
ok  	.../pkg/promptperplexity	0.024s
ok  	.../pkg/promptdiscipline	0.018s
ok  	.../internal/scheduler	0.882s
ok  	.../pkg/taskfailure	0.009s
```

`go build ./...` 退出码 0，`gofmt -l` 对本单改动文件无输出。

前端：

```
pnpm typecheck
 Tasks:    10 successful, 10 total
  Time:    2m34.908s

pnpm --filter @multica/core exec vitest run
 Test Files  160 passed (160)
      Tests  1972 passed (1972)
   Duration  30.61s

pnpm --filter @multica/views exec vitest run
 Test Files  427 passed (427)
      Tests  5099 passed (5099)
   Duration  449.11s
```

### 验收标准逐条对应的断言

- **T4 指标分账**：`TestRollupExcludesRuntimeOfflineFromPromptAttribution`，样本为 `ReasonRuntimeOffline`。
- **T5 缺数据可见**：`TestRollupReportsNoDataWhenIsErrorWasNeverRecorded`（后端）+ 前端用例断言 `is_error` 为 NULL 的维度卡片文本 `not.toMatch(/\b0%/)`。
- **T7 样本不足不编数**：前端用例断言显示「Sample too small」与「3 of 10 needed」，且卡片不含 0%。
- **T3 外部依赖非阻断**：前端用例断言 langfuse 降级时 `degraded-langfuse` 可见**且七张卡照常渲染**。
- **Q17 不合并**：前端用例断言 member 的区间 "5% — 15%" 可见、leader_task 的 "60% — 75%" 同屏不可见。

### 鉴权断言有效性核验

`ScoreBacklog` 的五条新测试做了移除条件的反证（改代码让条件失效 → 测试必须红）：

- 移除 disabled-generator 守卫 → `TestScoreBacklogIsANoOpWhenTheModelIsOff` 红（报 `llm client not configured`）
- 把 `finished_runs > 0` 放宽为 `>= 0` → `TestScoreBacklogIgnoresAVersionWithNoFinishedRuns` 红（考虑了 1 个版本、调了 2 次模型）
- 把 profile 计数上限改大 → `TestScoreBacklogLeavesAFullyScoredVersionAlone` 红（第二遍仍考虑并重评）
- 把「单版本失败继续」改成「直接返回错误」→ `TestScoreBacklogKeepsGoingAfterOneVersionFails` 红

反证跑完后代码已全部还原，`git diff` 对生成物与实现文件均无残留。

### internal/handler 的 73 项失败：pre-existing，与本单无关

`go test ./internal/handler/ -count=1` 有 73 项顶层 FAIL（含子测试共 80 行 FAIL）。用基线做了对照：在 `62b1dc27b` 上另开一个临时 worktree、连同一个数据库跑同一条命令，得到 **73 项顶层 FAIL**；两份失败用例名排序后 `comm` 比对，**仅本单侧新增 0 项，仅基线侧独有 0 项，完全一致**。对照用的临时 worktree 已 `git worktree remove`。

归因 `ENV-FAULT`：共享数据库 `multica` 被其他 worktree 分支的迁移污染——例如 `marketplace_plugin_listing` 表在库里存在但不在本分支的 `server/migrations/` 中。失败面集中在 plugin / marketplace / webhook / squad briefing 等与本单无交集的模块。本单相关用例单独跑全绿（`go test ./internal/handler/ -run 'PromptQuality' -count=1` → ok）。

## 未运行项与缺口（如实声明）

1. **D3 可复现性实测（验收第 6 条）未运行**。本地 `.env` 的 `MULTICA_LLM_API_KEY` 长度为 0（`MULTICA_LLM_BASE_URL`、`MULTICA_LLM_DEFAULT_MODEL` 同样为空），LLM client `Enabled()` 返回 false，D3 走 `ErrNotScored` 分支，无法产生任何真实评分，因此**同版本重复评分的实测波动区间拿不到**。代码层面已经把「声明区间」作为强制字段（`percent_low` / `percent_high`，带 `percent_low <= percent_high` 的 CHECK 约束，单点估计在结构上写不进去），但区间的实测依据必须等有可用模型的环境才能补。解锁条件：在配置了 `MULTICA_LLM_*` 的环境上对同一版本连跑 N 次 `ScoreAgentVersion` 并记录 band 与区间的离散度。这一项需要 Leader 裁定是本单补测还是另行分流。

2. **D3 七个子维度中，中间五项无原始出处**。`tier_placement` 与 `role_rule_completeness` 可直接对应 Q17 措辞；`cross_tier_conflict`、`literal_actionability`、`internal_consistency`、`precedence_clarity`、`scope_boundary` 五项及其权重是我依 Q17 的「分层组合与字面规则理解」「前后逻辑一致性与逻辑冲突」自行拆解的，没有 ADR 或 Owner 原文逐项背书。已在 `quality-perplexity-sheet.tsx` 的常量处标注。若 Owner 对子维度切分有不同意见，改动面是 `pkg/promptperplexity/score.go` 的 `SubDimensions` 与前端权重表两处，不影响落库结构。

3. **Langfuse 仅做降级路径**。本单未接入真实 Langfuse，只实现了「未配置/关闭时全部维度与看板可用 + 页脚降级标识」这一侧并有测试覆盖。接入本身不在本单范围。

## 风险

- `ScoreBudget = 3` 是我定的保守值：一个积累了几个月历史的部署首次开启 D3 时，backlog 会按每 5 分钟 3 个版本的速度慢慢排空，而不是一个 tick 打出几百次模型调用。如果实际版本量大到这个速度不可接受，调这个常量即可，不涉及结构。
- D7 一次过率本阶段用的是 issue 级粗口径（进过 `in_review` 算已评审，从未从 `in_review` 退回 `in_progress` 算一次过），ADR-002 §2.7 本身就标了低置信度。看板上需要有相应的口径说明，这属于文案范围。

## 下一步

本单为全量 QA。需要实机验证的点：「质量」Tab 在运行实例中按层级+版本展示曲线与七卡；版本时间轴联动与跨版本对比可操作；Langfuse 关闭态降级标识可见；D3 下钻 Sheet 的运行态切换与证据列表可见；各降级/空态（无数据、样本不足、jsonl 证据缺失）实机不渲染 0。其中 D3 相关的实机项在未配置模型的环境上只能看到「未评分」空态，需要 QA 环境配好 `MULTICA_LLM_*` 才能验到有数据的路径——这一点建议在派 QA 前先确认环境具备条件。

请 Leader 决定：D3 可复现性实测是本单补做还是分流，以及七子维度切分是否需要走 Owner 确认。
