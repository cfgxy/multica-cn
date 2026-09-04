# Upstream Diff

本文件记录 cfgxy/multica（本 fork）当前版本相对上游官方版本 [multica-ai/multica](https://github.com/multica-ai/multica) 的优化点与差异，供使用本 fork 的团队了解上游之外的新增能力与修复。

- 上游（upstream）：`multica-ai/multica`
- 对照基准：`upstream/main` @ `d4a712abf3880dfbd3daeac5daac1bd4bfb39b6f`（2026-09-02 同步）
- 初次整理时本 fork `main` @ `425e0cb9369071213d73d0e58ad96e71fbe1f59b`：领先上游 48 个提交（非 merge 39 个 + merge 9 个）

## 维护约定

- **人工维护**：改动合入本 fork `main` 后，由开发者在对应日期小节追加条目。
- **每条目 = 短 SHA + 原样 commit subject**；SHA 必须真实存在且与描述一致，同一功能多次提交可分列，不得合并省略 SHA。
- **唯一事实来源是 git**：核对命令 `git log --no-merges upstream/main..main`；不得写入 git 历史中不存在的条目。
- 同步上游时用 merge（保留本 fork 提交历史），同步后更新顶部「对照基准」一行。
- CI 自动生成暂未启用；如后续引入，以 workflow 输出为准并修订本约定。

## 2026-09（2026-08-30 ~ 2026-09-04）

### 发布与分发（Release APK 流水线与应用内更新）

- `53441b09a` chore(mobile): publish release APK to rolling GitHub Release on push to main (RUYI-34)
- `b26d959ef` ci(mobile-android): carry app version in versioned release tag
- `9fca6dea0` feat(mobile): serverless app-update check via GitHub Releases (RUYI-36)
- `ec90eda0f` refactor(mobile): 废除分环境包名，全环境统一 ai.multica.mobile（RUYI-33 / Owner 裁决）

### 移动端 Run 详情页（单次 run 时间线 / 工具调用日志 / 失败取消原因）

- `3227bf4d6` feat(mobile): run 详情页——单次 run 时间线/工具调用日志/失败取消原因（RUYI-33）
- `e1b524363` fix(mobile): run 详情失败/取消面板 error 文本经脱敏出口（RUYI-33 review 返工）
- `d511527b4` fix(mobile): Android 端 run 详情改全屏 modal，规避嵌套 formSheet 交互缺陷（RUYI-33 缺陷 D）
- `9ec2aa13d` fix(web): 移除评论 sticky 头部外溢渐隐叠绘，消除接缝文字叠压（RUYI-33）
- `48dbada5a` fix(mobile): 评论高亮叠层动画中断时复位，消除接缝残留色框（RUYI-33）
- `d5d694bea` fix(mobile): 升级 @shopify/flash-list 2.0.2 → 2.3.2，修复时间线长评论接缝叠压（RUYI-33）

### 移动端系统通知（mention / 失败 / 阻塞）

- `109f94088` feat(mobile): system notifications for mentions, agent failures, and blocked issues (RUYI-37)
- `a5ca0e979` fix(mobile): staging env injection into bundle + notification permission re-arm + cold-start tap navigation (RUYI-37 rework)
- `a2db7b6fc` fix(mobile): readiness-driven cold-start notification tap probe (RUYI-37 D3 round 2)

### 服务端任务队列与派发（排队任务消费）

- `72fe1ae13` feat(queue): 运行中任务可消费排队任务——自动解除排队并防重复执行（RUYI-48）
- `8552de766` feat(daemon): teach running agents the consume-queued adoption loop (RUYI-53)

### Agent Webhook 触发（绑定固定 prompt 的触发 URL）

- `326db5a6a` feat(agents): webhook trigger URLs bound to a fixed prompt (RUYI-52)
- `b5872142c` fix(agents): renumber migration to 450 and adapt readiness seam after main merge (RUYI-52)
- `d661c8f7b` fix(agents): register agent_webhook in the workspace deletion manifest; unbreak CRUD tests (RUYI-52)
- `493ac00bb` fix(agents): redact agent webhook tokens in access logs; close delete dialog; fix narrow-viewport URL row (RUYI-52)

### 实例级超级管理员与审计（全局用户/空间管理 + 身份切换）

- `6bdb0359b` feat(admin): 实例级超级管理员——全局用户/空间管理 + 身份切换（RUYI-47）
- `ce8d3a9e3` fix(admin): impersonate 响应携带 impersonator_id + 强制终止补写审计（QA P2-1/P2-2）
- `dd19e384e` fix(RUYI-47): admin workspace list 500 on ownerless workspaces; add stop-impersonation exit
- `a7502ca83` fix(RUYI-56): classify admin_audit_log as Settle, not Keep

### 项目级 Agent 指令

- `8d37bfb5b` feat(projects): per-project agent instructions injected into task briefs (RUYI-46)

### 移动端功能与交互修复

- `3b7c9e7f5` fix(mobile): SecureStore 会话键冒号非法字符导致启动永久卡 spinner（RUYI-38）
- `6aa2113ef` feat(mobile): issue-creation attachments + multi-select pickers (RUYI-42)
- `8ead61914` feat(mobile): issue 详情更多菜单新增「关联 PR」入口（RUYI-43）
- `0a28c5948` feat(mobile): workspace unread dot in switch-workspace sheet (RUYI-44)
- `77cf7a689` feat(mobile): chat header ⋯ becomes dropdown menu; unify More placement; add session rename/pin/archive (RUYI-51)
- `46e79b6cb` fix(mobile): 保留已归档聊天会话可达性 (RUYI-51)

### 本地开发环境（worktree 共享栈适配）

- `6ff28b473` fix(worktree): .env.worktree 数据库口令继承主检出, 不再写死弱口令 multica
- `410d775b2` fix(worktree): worktree 实例直接使用主库 multica——不按 worktree 隔离数据库名（Owner 2026-08-31 裁决：共享栈上修，同一数据库）

### 文档

- `08d0a95a6` docs: add fork CHANGELOG and link it from both READMEs

## 2026-08（2026-08-20 ~ 2026-08-29，初次整理）

### Android 平台支持与构建发布（上游未提供 Android）

- `cbab54a5c` feat(mobile): 启用 Android 支持
- `e9a53fba4` feat(mobile): Android release 签名与构建文档
- `dceb8fff2` fix(mobile): 无发布凭据时显式回退 debug 签名
- `d538b017c` test(mobile): 加签名配置的 Gradle 层验证脚本,并修正 ABI 排查文档
- `f6e3908af` docs(mobile): 补 Android ABI 裁剪的坑

### Android CI（GitHub Actions 自动构建上传 Release APK）

- `5b978d7c5` ci(mobile): 构建并上传 Android Release APK
- `fb785aa40` ci(mobile): 修复 Android 构建时 Gradle 缓存时序错误

### 应用内 API 服务器切换（自建服务器场景）

- `9b1cfc919` feat(mobile): 应用内切换 API 服务器地址
- `6f3c38489` fix(mobile): 修复服务器切换的三个 P0(QA 评审 RUYI-13)
- `c65bb9241` fix(mobile): 允许用户确认后的 Android HTTP 自建地址
- `44c4e9f6e` feat(mobile): per-server session snapshots across server switches

### 移动端功能与交互修复

- `480214db0` feat(mobile): add comment timeline navigation
- `124ed0a75` fix(mobile): close comments directory on fresh-session first select
- `a523e75f4` fix(mobile): Android 汉化 + Tab 图标 + 「…」菜单跨端修复
- `71cf99f0d` fix(mobile): project / lead 两类 picker 补 native header
- `dabcab477` fix(mobile): label picker 补 native header，兜底标题与搜索占位符走 i18n
- `d2e13f64c` fix(mobile): More 下拉标签空白 / 编辑页标题错用「新建」/ Due date 硬编码 / create_label 引号
- `7783fc37e` fix(mobile): server-settings header 不再压状态栏；3 条 zh-Hans 术语违规按契约改正
- `5dd1d5f33` fix(mobile): keyboard avoidance under Android edge-to-edge (RUYI-30)
- `afae3feff` fix(mobile): SecureStore 会话键冒号非法字符导致全新启动永久卡 spinner（RUYI-31）

### 移动端与 Web 国际化（zh-Hans 全量汉化 + i18n 质量防线）

- `fe2c30311` fix(mobile): P0 未登录首屏全量汉化 + i18n 覆盖率防线
- `668363a8a` fix(mobile): 补齐 i18n 覆盖率扫描器的结构性漏洞并重建 baseline
- `d260326ac` fix(mobile): i18n-keys 测试采集覆盖修复 + project picker 标签接线 (Review P1-5 + P1-4 残留)
- `0d3268e14` feat(mobile): P1 首批汉化 —— 三条 P2 + chat/issue/project 表单与按钮态
- `7a029d05f` feat(mobile): P1 批次 3 —— composer 汉化 + P2-1 绑定名白名单 + dispatch-reason
- `e2e84ce12` fix(mobile/i18n): 重写绑定名提取，汉化 chat composer 禁用原因
- `a813c843a` feat(mobile): P1 批次 5 —— settings 与 comment-card 文案汉化
- `006324315` feat(mobile): P1 批次 6 —— settings 首页/编辑器工具栏/任务详情/工作区选择/聊天空状态汉化
- `88bcce1b7` feat(mobile): P1 批次 7 —— 提及/指派 picker、收件箱、我的任务汉化 + 三项遗留修复
- `fe46c920d` feat(mobile): P1 批次 8 —— 常量文案表接 i18n + actor 名兜底收口
- `076d88cf1` feat(mobile): P1 批次 9 —— baseline 剩余 101 条清零 + 动态 key 前缀锁 + 判据收紧
- `cf9436f24` fix(mobile-i18n): 收紧 norm 换行判据、前缀锁 ns 从源码读回、cancel_title 打磨
- `89c2e5f1e` feat(mobile): P1 批次 11 之一 —— 活动流 24 条文案接 issues:activity.*
- `774ef258d` feat(mobile): P1 批次 11 之二 —— 5 个日期 locale 调用点收敛到 displayLocale()
- `41d14dc2c` feat(mobile): P1 批次 11 之三 —— 剩余遗留文案接线（金小欣 51 条清单）
- `4c9f19252` fix(mobile-i18n): 采集器不再整条丢弃带插值的模板串；前缀锁覆盖重命名解构的 t 别名
- `039c1e753` fix(i18n): 活动流 task 完成/失败文案语义写反，四语一并纠正
- `0dcfde78c` fix(i18n): parity.test.ts 尾随空格守卫改用显式结构类型消除 TS18048

### Web 测试

- `0a475640f` test(web): add invite resources to join page fixture
