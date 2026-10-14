# RUYI-635 设计板读图摘要（读图子代理蒸馏，蔡小星评审轮 2026-10-11）

> 来源附件：board1-mobile-inline.png / board2-mobile-sheet.png / board3-web-spec.png（黄小云 2026-10-11 14:16 交付）。本摘要为图片→文字忠实蒸馏，供后续轮次引用，禁止重复读图。

## Board 1（移动端行内四态）

画布标题：Board 1 · 移动端 — 引用聚合尾部块 · 行内四态（提案 vs 现状）。基准 Android 端 Issue 详情页局部，390dp 理想幅宽。

- **面板「现状 · 已交付 PR #338 基线」**：引用块两行纯文本，无图标无编号（`RUYI-689 …`、`RUYI-692 …` 均单行截断）。红字走查项：与正文同字号（14）信息层级失衡；标题单行截断 Owner 反馈 ✗；无状态图标无编号行距过密；每行独立可点。
- **提案 A · 少量引用（≤2 条全显）**：「引用 · 2」组头 + 右 chevron；行 1 黄色时钟图标（in_progress）+ RUYI-689 标题；行 2 蓝色圆勾（done）+ RUYI-692 标题。标注：分隔线 mt12/pt10 1px `--border`；组头行 22/字 11；图标 14×14（lucide StatusIcon）；编号 12/500 `--muted` · 标题 12/400 `--fg` 最多 2 行；整块热区 ≈90pt ≥44pt，按压 bg `--accent` 圆角 10。Owner 注释逐条：①行内前置实时状态图标（自定义状态按类型映射色）②字号 14→12 caption 档，编号 medium 标题 regular ③行内 ≤2 行换行 line-clamp-2，编号列不压缩 ④行内零跳转，点整块 → Board 2 弹层。
- **提案 B · 多引用（≥3 条折叠）**：「引用 · 5」组头；预览 2 行（Issue 图标+编号+标题）+ 折叠行「还有 3 条引用（含 2 个 PR）」。走查项：行内预览最多 2 条防长列表占屏；折叠行替代被折叠行，弹层看全集；Issue 与 PR 混排计数，Owner ② 的 PR 引用呈现入口。
- **提案 C · 超长标题换行**：行 1 红色禁止圈（blocked）+ RUYI-701 两行换行标题；行 2 紫色 PR 图标 + `cfgxy/multica-cn#338` 标题。走查项：换行上限 2 行；CJK 逐字断行西文 word-break；PR 行 = owner/repo#编号 + 标题（Owner ② 兼容形态）。
- **提案 D · 无引用不占位**：零引用卡片整块（含分隔线组头）不渲染，动态流直接接评论卡片。走查项：零引用完全去渲染；与 PR #338 契约一致；解析中/失败先按纯文本尾段展示，解析完成原地升级为行。
- **规范速查卡（两栏）**：移动端行内块——分隔线 mt12·pt10·1px `--border`；组头「引用 · N」11/500 tracking .06em chevron 14 `--muted`；预览行（≤2）图标 14 · 编号 12/500 · 标题 12/400 · 行高 18 · gap 8 · clamp-2；折叠行 12 `--muted`；整块热区 ≈90pt（≥44pt）按压 bg `--accent` 圆角 10 · 行内零跳转。弹层与 Web——弹层行 min-h 48 · 图标 16 · 编号 13/500 · 标题 14/20 clamp-2 · chevron 16；分组「任务 / Pull Request」混合时出现，分隔线缩进 48；弹层标题 16/700 · 关闭热区 ≥44 · max-h 75% 屏高 · safe-area · 遮罩关闭；Web 行内 13px + clamp-2；popover 420 宽 / max-h 360 · 行 min-h 44 · hover/focus；跳转 Issue 走应用内路由，PR 走 canonical URL（mobile）/ 新标签（web）。

## Board 2（移动端弹层两级交互）

画布标题：Board 2 · 移动端 — 整块点击 → 弹层列表 → 列表项跳转（防误触两级结构）。弹层复用现有 BottomSheet/ActionSheet 组件语言；PR 跳转走 RUYI-634 同款 canonical URL 通道。

- **第 1 屏 · 行内态**：引用块整体红虚线框标注 = 整块点击热区，覆盖组头、预览行、折叠行，任意位置按压弹第 2 屏；按压反馈 bg `--accent`。
- **第 2 屏 · Bottom Sheet**：标题「引用 · 5」+ 右上 ×；分组「任务」3 行（黄时钟 RUYI-689 / 蓝勾 RUYI-692 / 红禁止 RUYI-701，各 + chevron）；分组「Pull Request」2 行（紫 GitMerge `cfgxy/multica-cn#338`、`#341`）。标注：行 min-h 48，图标 16 → 编号 13/500 → 标题 14/20（≤2 行）→ chevron 16，行间 inset 分隔线左缩进 48；行按压 bg `--accent`；分组头 11，混合引用按「任务 / Pull Request」分组、组内保持首次出现顺序，单一类型不显示组头；关闭路径 ×3 = 遮罩点击 / ×（热区 28 圆 + padding ≥44）/ 下滑手势；底部 home indicator 留白，列表 max-h 屏高 75% 超出滚动。
- **底部交互流四条走查**：1) 行内块内任何行均不触发跳转（现状逐行跳转移除），整块一个 Pressable，误触代价=弹出列表不离开当前页；2) 弹层底部滑出「引用 · N」列表，Issue/PR 分组混排，行高 ≥48pt，遮罩点击即关闭；3) 任务行 → 站内 Issue 详情路由（router.push）；PR 行 → RUYI-634 canonical URL 通道（`lib/pull-request-link.ts`，装 GitHub App 进 App 否则浏览器），失败 Alert 提示回现状；4) 未解析引用弹层内不渲染为可点行，纯文本降级槽位展示（与行内契约一致）。

## Board 3（Web/桌面适配 + 状态图标语义对照表）

画布标题：Board 3 · Web / 桌面端适配 — 行内块改版 + Popover 列表 + 状态图标语义对照。Web 现状已有行内 StatusIcon（PR #338），本板增量：标题换行化、PR/MR 并入、交互改「整块 button → Popover」。桌面端（Electron）随 Web 同构。

- **Web 行内块改版（hover 态）**：灰底圆角容器「引用 · 5」+ chevron + 2 预览行 + 折叠行。走查项：行结构沿用现状 DOM（图标 14 · 编号 13/500 · 标题 13），宽度处理 `white-space:nowrap` → 2 行 clamp；整块改 `<button>`，hover bg `--accent`、focus-visible ring、cursor pointer；组头可发现性提示；多引用折叠规则与移动端一致（≤2 全显、更多折叠 + 计数行）。
- **Popover 列表态**：锚定触发块下方，分组「任务」（RUYI-689/692/701，第三行为 hover 高亮示意）+「Pull Request」（#338/#341）。标注：宽 420 / max-h 360 超出内滚动，空间不足自动上翻；行 min-h 44，hover bg `--accent`，圆角 8；跳转任务行=站内路由（当前页），PR 行=新标签（沿用 AppLink newTab 语义）；关闭/焦点 = Esc、点击块外；块可 Tab 聚焦 Enter/Space 打开，列表行可 Tab Enter 跳转，关闭后焦点回块。
- **状态图标语义对照表（两端统一 · 行内 14px / 弹层 16px）**：
  - Issue：todo=空环 muted 灰；backlog=16 点环 muted 灰；in_progress=半圆时钟 warning 黄 #dc6400（末两位像素不清）；in_review=3/4 饼 success 绿 #1c882d（不清），与现有 StatusIcon 完全同构（14 viewBox / r6 环 / r3.5 饼）；done=满环白勾 info 蓝 #0872d5；blocked=环+禁行斜杠 destructive 红；cancelled=环+× muted 灰；自定义状态按所属类型出图标 + 自定义色（MUL-6243 语义）。
  - PR：open=GitPullRequestArrow success 绿（web 现 emerald-600 硬编码 → 建议迁 token）；draft=GitPullRequestDraft 虚线圈 muted 灰（80% 透明度 dimmed）；merged=GitMerge violet 紫 #7c3aed（一致性决策点：web 侧栏现紫、mobile PR 卡现 info 蓝 → 建议两端统一 violet（GitHub 认知），mobile 新增 violet token）；closed=GitPullRequestClosed 红（web rose-600 / mobile destructive 各随其 token）；未知=GitPullRequest 中性图标 + 原始字符串回退（API 兼容契约）。
- **表底说明**：PR 状态→图标/色映射已在三处并存（web 侧栏 `STATE_ICON`、mobile `pullRequestStateVisual`、RUYI-634 信息流卡片），本功能为第四处消费方 → 建议抽到共享层（`packages/core` 导出纯函数，两端渲染器各自消费）；行内 14px、弹层 16px、信息流卡片 16px；暗色全走语义 token 不写死 hex，表中 hex 仅为亮色参考值。
- **混排展示方式（三板一致）**：Issue 行 = 状态图标 + RUYI-xxx + 标题；PR 行 = PR 图标 + owner/repo#编号 + 标题；同一块内混排，折叠行「还有 N 条引用（含 M 个 PR）」；弹层/Popover 按「任务 / Pull Request」分组，组内保持首次出现顺序，单一类型不显示组头。
