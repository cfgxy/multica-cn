# 华越桥通用协作提示词体系迁移记录

日期：2026年11月03日

## 结论

如意工坊现行“静态人物底色 + 原子动态条款 + 场景化 Skill”协作模式已迁入华越桥。迁移只对齐通用协作合同，保留了华越桥的项目、环境、授权、角色拓扑和专属 Skill 差异。

生产数据库变更已完成 `ROLLBACK` 演练、版本/SHA 基线断言、单事务提交和新连接回读；Leader claim、普通 Agent claim 和全新 Skill session 均有真实运行证据。

## 授权与目标

Owner 在当前会话中明确要求将如意工坊提示词、动态条款和 Skill 协作模式迁入华越桥，并选择授权通过 hp-server 生产数据库执行受控迁移。

- 源 workspace：如意工坊 `42f61469-c332-41f3-a2b4-018667473993`
- 目标 workspace：华越桥 `17afd325-a43c-4dd3-9280-4da3341f7ba8`
- 目标 RUYI squad：`5448cfb6-f35e-4d64-832e-9cc084ce0f9c`
- 目标 Leader 蔡小星：`14043839-8a2a-485a-9371-a544b2081a95`

## 静态提示词

华越桥 workspace、RUYI squad 和七个核心 Agent 的业务列均与各自最新 `prompt_version` 哈希一致。静态人物层未整体覆盖：

- 金小欣与如意工坊逐字一致。
- 蔡小星、顾小鱼、谢小婷、黄小云、陆小发、白小巡保留华越桥 Skill、WPS、发布、环境和项目边界扩展。
- 白小巡、陆小发仍是 workspace Agent，不因如意工坊七人 squad 拓扑被强制加入华越桥 squad。
- 数小析及 builder 等空间专属 Agent 未修改。

因此静态层本轮为经核验的 no-op，而不是遗漏。

## 动态条款迁移

### 原子化

退役以下混合资产的 active binding，历史正文和审计保留：

- `cls_10000002`：mention + 决策卡
- `cls_10000004`：证据 + 中文文本 + P0/P1/P2 + 交接
- `cls_10000015`：路由 + 知识 + 防拆单 + 阶段交付 + 连续 PR
- `cls_10000018`：状态 + 轮次经济 + 接管 + 完成判据
- `cls_antiloop`：并入 mention 与等待规则

新增 `cls_10000064`～`cls_10000078` 共 15 条原子 Clause：

| 范围 | Clause |
|---|---|
| workspace / issue | `64` mention、`65` 决策卡、`66` 证据标识、`67` 中文交付、`68` P0/P1/P2、`69` 跨角色交付、`78` 连续 PR |
| 蔡小星 agent / issue | `70` 华越桥职责路由、`71` 知识复用与沉淀、`72` 防拆单、`73` 阶段交付、`74` 阶段状态、`75` 轮次经济、`76` 接管阈值、`77` 完成判据 |

`cls_10000070` 保留华越桥拓扑：核心 squad 为蔡小星、谢小婷、顾小鱼、黄小云、金小欣；白小巡和陆小发仅在 Owner、Project Instructions 或具体任务明确授权时定向接入。

`cls_10000021《等待期调度》` 正文未改，binding 从 squad 迁到蔡小星 agent，使普通和 Leader Issue Run 都能获得。

### 生命周期收敛

8 个已 withdrawn、但资产状态仍为 active 的历史 Clause 已标记 `deprecated`：

`cls_10000006/08/09/11/12/33/34/36`。

### 保留的华越桥差异

以下资产保持 active，未按如意工坊覆盖：

- `cls_10000020`：华越桥共享现场、主检出清洁、owner 锁和隔离 fail-closed 边界。
- `cls_p1o5jctr`：机器凭据的主体/资源授权匹配和受控交付。
- `cls_zxvkuaub`：大制品位置、可读取性、SHA256、期限、清理责任和禁止拆片上传。
- 54 条 `cls_sh*` 项目 Clause：FDE 10 条、Sub2API 9 条、像素工坊 9 条、越南商会 SaaS 26 条。
- 如意工坊的 Multica 项目 Clause `cls_10000022`～`25` 未迁入。

### 迁移后选择集

- active Clause：85
- deprecated Clause：13
- workspace quick-create：3
- workspace chat：5
- workspace issue：14
- 蔡小星 agent issue：10
- RUYI squad issue：1（仅 `cls_10000020`）
- project active bindings：54，未变化

## 项目静态残留修复

没有使用历史 `prompt_version` 覆盖当前业务列。仅从当前业务列删除与现行动态条款冲突的旧通用治理引用：

- FDE演练场：移除“prompt 仓 snapshot → restore”发布入口，将已退出的“通用环境交接流程”引用改为 workspace active「运行连续性与资源生命周期」+ 本项目 active clauses + 当前交接手册。
- 越南商会SaaS：将“通用环境交接流程”引用改为 `shanghui-handbook〈env-ops〉`、项目 active clauses 与实际脚本。

两项目均追加 `prompt_version` v3，业务列与 v3 全文/哈希一致；FDE revision 6，HCM revision 9。Sub2API、像素工坊和 DeepSeek 项目业务列未修改。

## Skill 迁移

### Workspace Skill

七项通用 Skill 的正文和附件本来已经一致，无重复写版本：

- `android-avd-qa`
- `guxy-adr`
- `guxy-backend-debug`
- `guxy-bug-analysis`
- `guxy-evidence-bugfix`
- `resource-pool-qa`
- `vm-avd-e2e`

`guxy-harness-update-prompt` 已从华越桥 v1 更新为 v2，与如意工坊当前业务内容一致：

- description SHA256：`d7d46d1cc6ad...`
- body SHA256：`27b08bdf107a...`
- `defect-patterns.md`：`0bac74eebd8b...`
- `repo-ops.md`：`9f427c1d916d...`

白小巡的 Android/VM/resource-pool 与金小欣的实现型 Bugfix 绑定补为显式 `enabled=false`。华越桥陆小发的 `resource-pool-qa`、全员 `multica-runtime-skill-slimming`、WPS 和部署/数据专属 Skill 均保留。

### hp-server runtime-local Skill

真实 Run 发现 hp-server 的用户级同名 Skill 优先于 workspace Skill，且仍使用旧 Git `snapshot/restore` 协议。经 Owner 单独授权后：

- 旧目录备份至 `/home/guxy/.claude/skills/.backups/guxy-harness-update-prompt-20261103-085408`
- 原子替换三份文件
- 机器级 SHA256 与当前版本一致
- 华越桥蔡小星保留对该 runtime-local 条目的禁用策略，workspace Skill 仍是版本化协作资产

## 真实运行验证

### SHAN-495：Squad Leader claim

Task `01a19ed1-441c-7c26-9ae0-5fa9d3d5bb16`：

- `is_leader_task=true`
- agent Clause 10 条、squad Clause 1 条、workspace Clause 14 条
- 所有 injection `degraded=false`
- 未出现旧混合 Clause

### SHAN-495 第二轮：普通 Agent claim

Task `01a19eda-2e40-715d-bfc4-27892663f7fe`：

- `is_leader_task=false`
- 获得完整蔡小星 agent-scope 原子条款
- 未误注入 squad Clause

该轮复用首轮相同 Claude session，因此仍继承首轮已加载的旧 runtime-local Skill，不能用于判断禁用策略是否生效。

### SHAN-496 / SHAN-497：缓存与同名 Skill 定位

两个全新 session 仍确认实际优先读取 hp-server 用户级 Skill。Agent 数据层禁用项写入后，正式 Agent update API 用同值更新触发状态广播，证明问题不是 workspace Skill 数据或 Server 缓存，而是用户级同名文件仍可被 Claude 读取。

### SHAN-498：最终 Skill E2E

Task `01a19ee6-d02f-7ced-8ea9-9db637b920a8` 使用全新 session，终验通过：

- 实际加载 Skill 声明 Multica 当前 prompt/Clause 为唯一权威
- 包含“受控数据库维护例外”
- 不含把 `snapshot.sh` / `restore.sh` 作为维护、发布或恢复入口的旧流程

SHAN-495～498 均为只读验证单，已统一取消；SHAN-495 留有完整收束评论。CLI 默认 workspace 已恢复为如意工坊。

## 已知边界

- 本次证明配置、注入和 Skill 加载链路正确，不替代后续真实业务中的防拆单、接管、P0/P1/P2 和决策卡质量行为回归。
- FDE `cls_sh000009` 对数小析的演练数据库职责，与数小析静态“只操作 hcm”仍有项目级授权冲突；因不属于通用协作迁移，本次未擅自改写，需在实际启用 FDE 该角色前单独裁定。
- 四个有正文的项目在迁移前业务列与旧 prompt_version 不一致；本次仅对实际修改的 FDE/HCM 追加 v3，不用旧账本覆盖当前项目事实。
