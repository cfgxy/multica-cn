# 自进化模块 UI/UX 设计规格 v1.0

- 作者：黄小云（设计）｜日期：2026-09-25｜目标 Issue：RUYI-179（对应阶段一 RUYI-183 交付骨架，阶段二 RUYI-184 看板，阶段三 RUYI-185 测验，阶段四 RUYI-186 提案/知识/Skill）
- 适用端：**Web 为主**（桌面端 Electron 同页复用，仅路由注册；移动端本期不做）
- 现状基线：worktree SHA `a0a58f5df3ae8e073035efc60f7fe153fb5fa09e`（只读核查，未改代码）
- 输入：PM 规格 v1 / v1.1、ADR-001 / ADR-002、Owner 决策（Q10/Q13/Q17/Q18/Q19，出处评论 2026-09-25 01:23）与 Leader 派单验收标准 ①–⑤
- 设计原则：**全部复用 Multica 现有设计体系（shadcn/Base UI + 语义 token + recharts），零新组件库、零新色值**；凡规格有机械纪律（样本不足不出结论、分账、降级显式化、审计只追加）的，一律做成 UI 硬约束而非提示文案。

---

## 1. 信息架构与页面地图

### 1.1 页面地图

```
/[workspaceSlug]/self-evolve          ← 模块根，默认落「总览」tab
  ├─ Tab 总览   Overview            阶段一
  ├─ Tab 版本   Versions            阶段一（四级 Prompt 版本管理）
  ├─ Tab 质量   Quality             阶段二（七维度看板 + 困惑度下钻）
  ├─ Tab 测验   Quiz                阶段三（题库 + 测验批次与结果）
  ├─ Tab 提案   Proposals           阶段四（五类提案池，打勾修订）
  ├─ Tab 知识   Knowledge           阶段四（bd memories 统一收集查看）
  └─ Tab Skill  Skills              阶段四（skill 版本与使用评估）
```

- Tab 次序即阶段上线次序：**阶段未上线的 tab 不渲染**（渐进披露，不做 disabled 占位——避免用户点进空壳）。
- 阶段三/四的测验、知识、Skill 三个 tab 本单只定 IA 落位与页面骨架，细化交互设计在该阶段开工前另行交付（见 §7 设计假设）。
- 页面骨架统一为：页头（模块名 + 面包屑省略，模块内不重复空间名）+ 横向 Tabs（复用 `packages/ui/components/ui/tabs.tsx`，用法参照 `packages/views/issues/components/issue-detail.tsx`）+ Tab 内容区。

### 1.2 左侧栏入口（验收标准 ①）

| 项 | 规格 |
| --- | --- |
| 位置 | `packages/views/layout/app-sidebar.tsx:154` 的 `workspaceNav` 数组，在 `agents` 之后插入 `{ key: "selfEvolve", labelKey: "selfEvolve" }`。最终次序：issues → projects → autopilots → agents → **自进化** → squads → usage |
| 路由 | `packages/core/paths/paths.ts` 新增 `selfEvolve: () => \`${ws}/self-evolve\``；页面 wiring 于 `apps/web/app/[workspaceSlug]/(dashboard)/self-evolve/page.tsx` |
| 图标 | `packages/core/paths/route-icons.ts` 注册 `{ segment: "self-evolve", icon: "Sprout", navKey: "selfEvolve" }`；该文件 53–56 / 69–72 两处 `NavKey` union 同步加 `"selfEvolve"`。备选图标 `TrendingUp`。lucide-react 已是依赖，不新增包 |
| 文案 | zh-CN「自进化」；en "Self-Evolve"；i18n key `nav.selfEvolve`，en/zh locale 词表同步新增 |
| 视觉态 | 完全复用现有 `SidebarMenuButton` 的 data-active / hover 样式（`app-sidebar.tsx:887` 的 className 原样套用），不写新样式 |
| 桌面端 | tab bar 图标与侧栏自动一致（`app-sidebar.tsx:141` 注释声明的既有机制），零额外工作 |
| 可见性 | 全部 workspace 成员可见可进入（浏览只读）；模块内一切写操作按 §2.5 权限态禁用，与规格 v1 §3.6 一致 |

---

## 2. Tab「版本」——四级 Prompt 版本管理（阶段一）

### 2.1 页面布局

三区布局（desktop ≥1024px）：

```
┌─────────────┬──────────────────────────────┬───────────────────┐
│ 实体树 240px │ 版本列表（时间线，flex-1）      │ 详情面板 400px     │
│             │                              │ (选中版本时出现)    │
└─────────────┴──────────────────────────────┴───────────────────┘
```

- **实体树**（左）：四级分组，组内按名称排序。
  - 组 1「空间」：当前 workspace context（恒 1 项）
  - 组 2「项目」：各绑定项目 instructions
  - 组 3「小队」：各小队 instructions
  - 组 4「智能体」：各智能体提示词
  - 每项显示：实体名 + 当前版本徽标（`v12`，Badge variant secondary）。选中态：`bg-sidebar-accent`（沿用侧栏语义）。
  - 组件：`collapsible.tsx` 做分组折叠，普通列表项，**不**嵌套 Sidebar 组件本体。
- **版本列表**（中）：该实体版本线，默认倒序（最新在上），每行：

  | 列 | 内容 |
  | --- | --- |
  | 版本号 | `v16` monospace；当前生效行左侧 3px brand 色竖条 + Badge「当前生效」（variant default） |
  | 变更说明 | 一句话，超长截断 + Tooltip 全文 |
  | 来源 | Badge：编辑 / 恢复（hover 注明源版本）/ 导入 / 提案（hover 注明提案号）——对应 `prompt_version.source` 四枚举 |
  | 操作者 | ActorAvatar + 名字（复用 `packages/ui/components/common/actor-avatar.tsx`） |
  | 生效时间 | 相对时间 + Tooltip 绝对时间 |
  | 门闸 | 通过 ✓（muted）/ 不通过 ✗（destructive）；点开看 findings |
  | 质量摘要 | 阶段二起显示：注入 token 数 + 七维 mini 堆叠条（32×8px，纯陈列，不判方向）；阶段一此列为「—」 |
  | 标签 | 阶段三起：存在回归（destructive）/ 未测验（outline）——标签制不是闸门（Q10） |

  组件：`table.tsx`；行 hover `bg-muted/50` 显行内快捷操作（对比 / 恢复此版本）；版本数 >50 分页（每页 50，不虚拟滚动——量级可控）。
- **详情面板**（右）：点版本行滑出（Sheet，桌面右侧 400px；<768px 转全屏 Dialog）。内容分区：
  1. 元数据（版本号 / 生效时间 / 操作者 / 来源 / 来源版本号 / 来源提案号——提案号可点跳转提案 tab，SC4 双向可达）
  2. 门闸结果（findings：category / rule / line；不含匹配文本，与 marketplace 同语义）
  3. 正文全文（`font-mono text-caption`，内滚动，右上复制按钮）
  4. 操作：「与上一版对比」「编辑」「恢复此版本」（写操作，见 §2.3）
  5. 审计块：时间 / 操作者 / 来源 / 变更说明 / diff 入口；**无任何删除入口**（W4）

### 2.2 版本对比（diff）

- 入口：① 详情面板「与上一版对比」；② 列表勾选任意两个版本 → 浮动工具条「对比」（勾 0 或 1 个时按钮 disabled，Tooltip 说明「需选择两个版本」）。
- 组件：**直接复用 `packages/views/market/prompt-diff-view.tsx` + `prompt-diff.ts` 的 `diffPromptText`**，不新写 diff。该组件已是行内式表格：added 行 `bg-success/10`、removed 行 `bg-destructive/10`、每行带 `+`/`−` 前缀与行号（色盲/黑白打印安全，注释原文已声明）、顶部 `+N −M` 摘要、超长 truncated 提示。
- 方向语义固定：**基线（旧版）为 current，目标（新版）为 incoming**；对比标题写「v12 → v16」。
- 容器：max-h-[60vh] 内滚动；diff 显示于 Dialog（宽 min(960px, 92vw)）。

### 2.3 切换 / 回滚 / 编辑保存——统一「生成新版本」确认流（验收标准 ②）

v1 §3.3/§3.4 定稿：一切生效变更 = 生成新版本（回滚是 copy-forward）。**UI 措辞统一为「恢复此版本」，不用「切换」**，避免误解为指针移动。

**确认流（AlertDialog，三段式，不可跳过）**：

1. 标题：`恢复 v12 为当前版本？`（编辑保存则为 `保存为新版本 v16？`）
2. 正文必须同时含三件：
   - **作用范围句**（按层级模板）：空间 →「该空间全部新启动的 run 将立即使用此内容；进行中的 run 不受影响」；项目/小队/智能体 → 对应作用域句（v1 §3.3 作用范围）。
   - **完整 diff**：PromptDiffView（当前生效版 → 目标内容）。
   - **新版本号预告**：「将生成 v16（内容与 v12 一致），v12 原记录保留不变」。
3. 按钮：主按钮「确认恢复」（diff 含删除行时用 destructive variant 提示有删减）+「取消」。主按钮 loading 态 = spinner +「门闸校验中…」（校验与写入同一事务，前端只表现一个等待态）。

**反馈**：

- 成功：sonner success toast「已生成 v16 并生效」；列表顶部插入新行、「当前生效」竖条与 Badge 移动；新行展开即见审计（来源 = 恢复，源版本 = v12）。
- 门闸 FAIL：确认框内 inline 错误面板，列违规明细（结构 / 文风 / 凭据模式三类，逐条 category+rule+line，W2）；不产生版本、列表不变；主按钮变「重新校验」。
- 无权限：写操作按钮 disabled + Tooltip「仅空间 Owner 可执行」（W1；提案采纳确认步同权限，SC6）。

### 2.4 编辑态

详情面板「编辑」→ 正文变 `textarea.tsx` 全文编辑（monospace，auto 高度 max-h 内滚动）+ **变更说明必填**输入（`field.tsx` + `input.tsx`，1–200 字，空值时保存 disabled）→「保存为新版本」走 §2.3 确认流。取消编辑恢复只读态，弹窗关闭前有未保存内容时 AlertDialog 二次确认「放弃修改？」。

### 2.5 状态清单（本 tab 全量）

| 状态 | 规格 |
| --- | --- |
| loading | 首载：实体树骨架 4 组 + 列表 8 行 skeleton；详情面板局部 spinner |
| empty | 实体无任何版本（导入基线前）：Empty 组件「尚未建立版本基线」+ Owner 可见「导入 v1 基线」按钮（W3），成员只见说明文案 |
| error | 区块级错误态 +「重试」按钮；写失败 sonner error toast，界面状态回滚 |
| disabled | 无权限写操作（Tooltip 说明）；对比按钮勾选不足两个版本时 |
| hover | 列表行 `bg-muted/50` + 行内快捷操作显形；实体树项 hover 同侧栏惯例 |
| focus | 全部可交互元素 focus-visible ring（ui kit 默认）；确认框焦点圈闭 + Esc 关闭 + 焦点归还触发元素 |
| 成功反馈 | sonner toast + 列表就地更新（React Query invalidate），关键立法动作附审计入口 |

---

## 3. Tab「质量」——七维度看板（阶段二，验收标准 ③④）

### 3.1 页面结构（三层信息架构）

```
┌ 控制条：实体选择器（复用 §2.1 实体树数据，此处为 Select）│ 时间范围 30d/90d（Segmented，参照 usage-trend-card 的 DimSegmented 模式）
├ 数据起点声明 banner（常驻）
├ 版本时间轴带
├ 七维度卡片区（grid）
└ 页脚：数据源状态（Langfuse 导出已关闭 / jsonl 证据可用性）
```

1. **数据起点声明 banner**（D8 验收）：muted 底横幅常驻：「质量数据自 YYYY-MM-DD（埋点上线）起可信；此前 run 无版本归属，不纳入统计。」时间轴与图表对起点前区间一律渲染空白+置灰，**不是 0**。
2. **版本时间轴带**：所选实体的版本线横向时间轴，每版本一段（段宽 ∝ 生效时长，最短保底 48px），段内标 `v12` + 生效区间；点段联动下方卡片区（卡片的「当前版本」与「对比基线」随之切换）。段间分隔线即版本变更事件（看板问题 1 的锚点）。**段内不放综合色/综合分**——ADR-002 定稿不合成单一质量分，避免暗示权重。
3. **七维度卡片区**：响应式 grid（xl 4 列 / lg 3 列 / md 2 列 / sm 1 列）。每卡固定骨架：

```
┌───────────────────────────────┐
│ D5 重试次数            [高置信] │  ← 维度名 + 置信度徽章（§3.3）
│                               │
│   2.4 次        [↑ 0.6 持平线] │  ← 绝对值大数字（number-flow）+ delta Badge
│   中位数 · n=23 runs           │  ← 口径短语 + 样本数
│   ▁▂▃▅▃▂ sparkline（recharts）│
│ [下钻 ›]                      │
└───────────────────────────────┘
```

### 3.2 绝对分与相对变化同卡表达（验收标准 ③）

- **绝对值**：卡左侧大数字（`number-flow.tsx`，变化时数字滚动动画）。
- **delta Badge**（右上）：「较上一版本」——
  - 双侧样本各 ≥10 完结 run 且变化 >±10%：方向性 Badge（`↑ +12%` success / `↓ −8%` destructive），Tooltip 标注「方向性参考：n=23 vs n=18」；
  - 变化在 ±10% 内：灰色 Badge「持平」（v1 §5.2 噪声判据）；
  - 任一侧 <10 样本：**灰色 Badge「样本不足」**，不渲染箭头、无方向性文案（接口返回「样本不足」标记，前端不得兜底渲染 0，ADR-002 T7）。
- 任务构成不可比（两侧类型分布差异 >30%）：卡角落加「任务构成不可比」outline Badge + Tooltip（v1 §5.2）。
- delta 的比较基线 = 时间轴带当前选中段的上一段（即上一版本生效期），随联动切换。

### 3.3 七维度卡细则（口径以 ADR-002 §2.2 / RUYI-184 为准，此处只定呈现）

| 卡 | 主数值 | 置信徽章 | 特殊呈现 |
| --- | --- | --- | --- |
| D1 注入成本 | 该版本注入 token 数（前置）+ run 实耗中位数（后置，双行） | 高 | token 千分位 / k 格式化；双值并排，前置值标注「静态」后置「实测」 |
| D2 纪律性 | 0–100 分 | 中 | 卡脚固定一行 micro 文案「覆盖可机械判定条款；零扣分 ≠ 完全守纪」（ADR-002 覆盖率元指标要求）；扣分明细入下钻 |
| D3 规则困惑度 | 0–100 分（Owner Q17 定稿：按注入运行态七子维度加权聚合） | 低 | 「下钻证据」是本卡第一入口（§3.4）；分数旁风险档位 Badge：低 / 中 / 高 |
| D4 轮次失败率 | is_error 工具结果占比 % | 中 | 埋点上线日前显示「自 YYYY-MM-DD 起有数据」，**不显示 0%**（T5） |
| D5 重试次数 | attempt 中位数 + P90（双值） | 高 | 分布 mini 直方图入下钻 |
| D6 错误根因 | 可归因 Prompt 的失败占比 | 高 | **卡内固定两栏分账条**：「可归因 Prompt」vs「环境/供应商」横向堆叠条，分母只用前者（T4）；环境占比独立数字展示但**不入 delta 方向判定**；Top Reason 明细入下钻 |
| D7 一次过率 | %（in_review→in_progress 回退粗口径） | 低 | Tooltip 固定口径说明「低置信度：仅检测 Issue 状态回退」 |

### 3.4 困惑度下钻（验收标准 ④）

D3 卡「查看逐项证据」→ Sheet（480px，<768 全屏）：

1. **顶部聚合区**：聚合分 0–100 大数字 + 风险档位 Badge（低/中/高，阈值以实现配置为准，Badge Tooltip 声明当前阈值）+ 评分时间 + **可复现性声明**：「同版本重复评分波动 ±X（声明区间）」+ 评分模型标识。同版本重复评分的多次结果并列可查（RUYI-184 验收「可复现」的呈现面）。
2. **运行态切换**（Owner Q17 决策「以注入运行态为评分单元」）：Sub-tabs「普通成员态 / Leader task 态」——两态各自独立评分，切换不合并；未覆盖的运行态显示「该运行态暂无评分」。
3. **七子维度列表**（①注入层级正确性 … ⑦角色规则完整性）：每行 = 子维度名 + 0–1 得分进度条（`progress.tsx`）+ 权重百分比 + justification 摘要（2 行截断）+「证据」展开手风琴：
   - 证据条目 = 规则片段引用（定位：哪一级 prompt / 哪一节）+ 冲突/重复描述（与哪条冲突）；
   - 证据引用优先元数据（SHA / 路径 / 行号），摘录经脱敏管道（v1.1 §16）；
   - jsonl 类证据不可用时显式条目「本条无 jsonl 证据（runtime 离线）」，**不静默留白**（ADR-002 §3.3）。

### 3.5 跨版本质量对比

时间轴带支持选两段 → 「对比」→ Dialog 内七维双列对照表：维度 | vA | vB | Δ（delta 纪律同 §3.2）。表格复用 `table.tsx`。

### 3.6 降级与空态（硬约束，非提示文案）

| 情形 | 呈现 |
| --- | --- |
| Langfuse 未配置/关闭 | 页脚常驻低调文字「Langfuse 导出已关闭」+ Tooltip「看板数据不受影响」；任何卡不因此 disabled（T3） |
| 维度无数据 | 卡内「无数据」+ 原因短语（无完结 run / 指标未上线），不渲染 0（T5/T7） |
| 样本不足 | §3.2 灰 Badge，禁方向性文案 |
| jsonl 证据缺失 | §3.4 显式条目 |
| loading | 卡骨架（数字位 + sparkline 位）；error 卡内错误态 + 重试 |

---

## 4. Tab「提案」——每日提案打勾修订（阶段四，验收标准 ⑤）

### 4.1 页面结构

```
┌ 控制条：批次选择器（默认今日）│ 类型 chips ①–⑤ │ 状态 filter │ 搜索 │ [提交提案]
├ 批次心跳行：今日扫描 HH:MM · 扫描 42 个对象 · 产出 6 · 丢弃 1 · 零产出也显示心跳
├ 提案卡片流（单列，max-w-3xl 居中）
└ 分页
```

### 4.2 提案卡片

```
┌ [①Prompt修订] [多例证据] 批次 #2026-09-25 · 08:00        ┌────────┐
│ 摘要（1–3 句）                                            │ ☐ 采纳  │
│ 建议：为 workspace context §成本纪律 补充「零叙述轮」条款    │ [驳回]  │
│ 证据：[RUYI-176](issue) · [run 8f3a](run) · jsonl 摘录 2 条 │ [详情]  │
└──────────────────────────────────────────────────────────┴────────┘
```

- 类型 Badge ①–⑤ 五色区分（沿用现有 Badge 色板，不新增色值）：①Prompt 修订 ②项目认知 ③经验教训 ④踩坑记录 ⑤Skill。
- 置信标记 Badge：「多例证据」default /「单例推断」outline。
- 证据行：≥1 条 issue/run 链接（SC1 无证据的草稿不会出现——服务端已丢弃，前端无需兜底空证据态）；jsonl 证据标注「摘录已脱敏」；不可用时「本条无 jsonl 证据（runtime 离线）」。
- 类型①卡额外区：目标实体（层级 + 实体名）、修订意图一句话、「diff 预览」入口（PromptDiffView）。
- 过时警示：归档/待处理提案关联版本已变更 → 卡顶警示条「建议内容基于 v12，当前已 v15」（v1.1 §10.4）。

### 4.3 打勾交互——操作成本设计（验收标准 ⑤ 的核心）

目标：Owner 每日 N 条提案的处理 = **N 次单击勾选 + 少数几次 diff 确认**，全程无模态打断。

1. **勾选即采纳，不打断**：卡片 checkbox 勾选 → 不弹窗；卡片就地转「已采纳」态（边框 success/40 + 降透明度），并出现**行内确认条**：
   `已采纳 · 将生成新版本草稿　[预览 diff 并确认]　[撤销]`
   ——连续打勾不被 modal 阻断，批量勾完后统一走确认队列。
2. **确认步（Q11=A 保留）**：点「预览 diff 并确认」→ Dialog：目标实体新版本全文 diff（PromptDiffView）+ 门闸结果（通过 ✓ / 违规明细 ✗）→「确认生成 v16」→ 同事务生效 → success toast + 卡片转「已生效」态并出现 `→ v16` 链接（点跳版本审计；版本审计含来源提案号，SC4 双向可达）。
3. **门闸 FAIL**：卡片转「需修订」态（warning 边框）+ 违规明细展开 +「重新生成」按钮（重跑草案过门闸，T6：不产生版本行）。
4. **撤销**：已采纳未确认前可撤销回「待处理」，行内一键，无确认弹窗。
5. **驳回**：单击即驳（无确认弹窗）+ 5s 内 toast 可撤销；toast 上附「填写原因」入口（选填）。
6. **批量**：卡片流顶部全选框 +「批量采纳选中」→ 逐条过门闸后进入**确认队列页**（逐条 diff 确认，逐条生效），不存在「一键全部生效」。
7. **键盘流（P1 增强，本期标注不做）**：j/k 移动、x 勾选、d 驳回、Enter 详情。
8. **权限**：非 Owner 身份勾选/驳回/确认均 disabled + Tooltip「仅空间 Owner 可执行」（SC6）；「提交提案」按钮全员可用（成员直提入口保留，v1.1 §10.2）。
9. **状态机到 UI 的映射**：待处理（草稿）→ 已采纳 → 已生效（挂版本号链接）/已转化（②–⑤ 转知识条目或 skill 后显示转化结果链接）/需修订 → 已归档（30 天自动，状态 filter 单独一档，可检索、可「恢复重提」，SC5）。

### 4.4 状态清单

- loading：卡片骨架 3 张；批次心跳行 skeleton。
- empty：今日零产出 → Empty「今日扫描零产出」+「查看批次心跳记录」链接（零产出日也有心跳，v1.1 §10.1）；筛选无结果 → Empty + 清除筛选。
- error：区块级错误 + 重试；写失败 toast + 卡片状态回滚。
- focus/hover/disabled/成功反馈：同 §2.5 全局规范。

---

## 5. 全局规范（模块级，三个已细化 tab 共用）

- **Design token**：只用现有语义 token——色（`bg-brand`、`bg-success/10`、`bg-destructive/10`、`bg-muted/20`、`text-muted-foreground`、`text-faint-foreground`、`border` 等）；字号（`text-caption`、`text-micro` + 默认 body）；diff/正文区 `font-mono`。深色模式随 token 自动适配，不做单独设计稿。
- **间距**：卡片内 `space-y-1.5 / gap-2` 级，区块间 `gap-4 / space-y-6`，与现有 views 一致；不新增间距常量。
- **响应式断点**：`xl 1280 / lg 1024 / md 768 / sm 640`。版本页三栏 → md 起实体树折叠为 Select（保留当前选中态）→ sm 单列栈；质量卡网格见 §3.1；Sheet 在 <768 转 Dialog 全屏（现有 sheet 组件惯例）。
- **长文案 / 极端数据**：变更说明、摘要截断 + Tooltip；diff 超大走 truncated 通道（PromptDiffView 已有）；版本列表分页 50/页；提案卡片流 max-w-3xl 限宽保证可读行长。
- **i18n**：全部文案走 i18n key（`nav.selfEvolve` + 模块词表），en/zh 同步；日期相对时间格式复用现有工具。
- **可访问性**：diff 的 ± 前缀 + 行号（组件自带，色彩非唯一信号）；「方向性 Badge」同时带文字符号（↑/↓/持平/样本不足），不单靠颜色；对话框焦点管理沿用 ui kit；图表附文本数值（卡内大数字即图表的无障碍等价物，sparkline 纯装饰 aria-hidden）。
- **审计可见性不变项**：任何写动作的审计块只追加、无删除入口；提案 ↔ 版本双向链接（SC4）。

## 6. 变更点 / 不变项 / 实现注意事项（给 @ 顾小鱼）

**变更点（全部可定位）**：

1. `packages/views/layout/app-sidebar.tsx:154` workspaceNav 插入 1 项；`packages/core/paths/route-icons.ts:53/69` 两处 NavKey union + `:97` 注册表 1 行；`packages/core/paths/paths.ts` 新增 selfEvolve。
2. 新增 `apps/web/app/[workspaceSlug]/(dashboard)/self-evolve/page.tsx`（web wiring）与桌面端路由注册；新增 `packages/views/self-evolve/` 与 `packages/core/self-evolve/`。
3. i18n locale 文件 en/zh 各加 nav 键与模块词表。

**不变项**：现有 6 个导航项次序、图标、样式零改动；注入链路零改动（ADR-001 §4.2，业务列直读）；不引入任何新组件库/图表库/diff 库（recharts 3.8.0 与 diffPromptText 已在依赖内）；不新增色值与间距常量。

**实现注意事项**：

1. PromptDiffView 复用时注意其 i18n 命名空间是 `prompt-market`——跨模块复用需把 diff 相关 key 上提为公共词表（改动仅 locale 文件，组件不动），不要在 self-evolve 里复制组件。
2. 样本不足、无数据、降级三类接口语义（「样本不足」标记、NULL ≠ 0、显式降级文案）必须由 `packages/core/self-evolve/` 的 zod schema 显式建模（discriminated union），**禁止前端拿 0 兜底**（ADR-002 T5/T7）。
3. delta 判定（±10%、≥10 样本）由服务端计算并返回结论档位，前端只渲染档位，不本地重算。
4. 写操作按钮的权限 disabled 需要 zod schema 里的 `canWrite` 字段支撑（Owner 判定在服务端），不要前端猜角色。
5. 包边界红线：core 零 react-dom/localStorage/process.env；views 零 `next/*`，路由走 NavigationAdapter。

## 7. 设计假设与未覆盖项

| # | 假设/未覆盖 | 处理 |
| --- | --- | --- |
| 1 | 入口图标选型 `Sprout`（备选 `TrendingUp`）为设计推荐 | Leader/Owner 可改一行注册表生效，不阻塞 |
| 2 | 风险档位阈值（困惑度低/中/高分界）、±10%、≥10 样本等数值 | 以规格与实现配置为准，本设计只定呈现纪律 |
| 3 | 阶段三测验、阶段四知识库/Skill 的细化交互 | 本单只定 IA 落位（§1.1），细化设计在对应阶段开工前交付，避免设计随口径过期 |
| 4 | 版本「质量摘要」mini 条在阶段一的占位 | 阶段一该列显示「—」，不留空壳交互 |
| 5 | 桌面端窄窗口与 Web 共用断点 | Electron 窗口 <1024 时同样折叠，按 Web 断点处理 |

## 8. 逐项走查清单（交付后照单核验）

| # | 对应验收 | 走查项 |
| --- | --- | --- |
| 1 | ① | 左栏次序 issues/projects/autopilots/agents/**自进化**/squads/usage；active、hover、图标、en/zh 文案；桌面 tab bar 同步 |
| 2 | ① | 点击入口达 `/[ws]/self-evolve` 总览；成员可进、写操作 disabled+Tooltip |
| 3 | ② | 「恢复此版本」确认框含范围句+diff+新版本号预告；确认后列表新增行、审计含来源版本；无删除入口；门闸 FAIL 显示明细且无新行 |
| 4 | ② | 编辑保存必填变更说明；空说明时保存 disabled |
| 5 | ③ | 七卡均有绝对值大数字 + delta Badge；±10% 显持平；<10 样本显「样本不足」且无箭头 |
| 6 | ③ | D6 两栏分账条可见，环境/供应商占比不入方向判定 |
| 7 | ④ | D3 下钻：运行态切换、七子维度得分+权重+证据展开、可复现性声明；jsonl 缺失显式文案 |
| 8 | ④ | D4 起点前「自 YYYY-MM-DD 起有数据」非 0%；数据起点 banner 常驻 |
| 9 | ⑤ | 提案勾选无弹窗、可连续操作；确认步 diff+门闸；生效后提案↔版本双向链接可达 |
| 10 | ⑤ | 驳回单击+可撤销；批量采纳走逐条确认队列；非 Owner 全部 disabled |
| 11 | — | Langfuse 关闭页脚标识；空态/loading/error 全量过一遍（§2.5/§3.6/§4.4） |
| 12 | — | md/sm 断点布局；长文案截断；100+ 版本分页 |
