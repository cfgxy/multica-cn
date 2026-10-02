# Upstream Diff

本文件记录 cfgxy/multica（本 fork）当前版本相对上游官方版本 [multica-ai/multica](https://github.com/multica-ai/multica) 的优化点与差异，供使用本 fork 的团队了解上游之外的新增能力与修复。

- 上游（upstream）：`multica-ai/multica`
- 对照基准：`upstream/main` @ `d4a712abf3880dfbd3daeac5daac1bd4bfb39b6f`（2026-09-02 同步）
- 初次整理时本 fork `main` @ `425e0cb9369071213d73d0e58ad96e71fbe1f59b`：领先上游 48 个提交（非 merge 39 个 + merge 9 个）
- 2026-10-02 增补时本 fork `main` @ `4c20efc35a48ee3e09e845f540d2bac15bef0816`：领先上游 587 个提交（非 merge 380 个 + merge 207 个）；上游已前移至 `2ea01ae4ef55de4310b99af192d2dbd367832883`（2026-10-01），对照基准行不变，待下次同步时更新

## 维护约定

- **人工维护**：改动合入本 fork `main` 后，由开发者在对应日期小节追加条目。
- **每条目 = 短 SHA + 原样 commit subject**；SHA 必须真实存在且与描述一致，同一功能多次提交可分列，不得合并省略 SHA。
- **唯一事实来源是 git**：核对命令 `git log --no-merges upstream/main..main`；不得写入 git 历史中不存在的条目。
- 同步上游时用 merge（保留本 fork 提交历史），同步后更新顶部「对照基准」一行。
- CI 自动生成暂未启用；如后续引入，以 workflow 输出为准并修订本约定。
- **平台过程提交不单列**：平台运行收尾的自动提交（`chore(agent): uncommitted changes from task`、`chore(agent): baseline — uncommitted work from the local directory` 及其 Revert）与平台载体清理提交（`wip(ruyi-…)`、`chore(ruyi-…): 移除平台…`）不作为能力条目收录，其承载的实质变更以对应功能提交为准。



## 2026-09 下旬 ~ 2026-10（2026-09-23 ~ 2026-10-02，2026-10-02 增补）


### Run 生命周期管理闭环（RUYI-292：服务端 cancel/retry 血缘、MCP 四工具、UI 执行日志六态；部分服务端实现存在于平台自动提交快照，以正式提交为准）

- `c9e34ebda` feat(runs): Run 生命周期 UI 全量与取消确认修复（RUYI-292）
- `279c68511` fix(views): 合并集成修复——对齐 main 侧字号规格与 lint 门禁（RUYI-292）
- `08cab1207` fix(runs): MCP list_issue_runs 契约对齐服务端裸数组，limit 在默认路径生效（RUYI-292 QA 返工）

### MCP 工具与自托管管理

- `8ee2954ba` feat(mcp): assign_issue 工具——已有 Issue 的指派/改派/取消指派（RUYI-282）
- `79982f840` feat(mcp): add mcp-update/mcp-status/mcp-uninstall alongside mcp-install
- `57d9d56ab` feat(mcp): add mcp-http-install/status/update/uninstall systemd targets
- `cef2e725d` feat(mcp): 将 mcp-http-* 改造为 systemd --user 单元，零 sudo

### Prompt 治理：四级版本体系与质量看板（RUYI-183/184/285/287）

- `0bf1dc495` feat(prompt-version): 四级 prompt 版本历史/diff/切换回滚(RUYI-183 阶段一)
- `fd78774d2` feat(prompt-version): 任务领取时写入 prompt_versions 归因(RUYI-183 T2)
- `b9f23e6f3` feat(self-evolution): 新增自进化导航骨架(RUYI-183 T3)
- `cc03bc5d8` fix(self-evolution): 补齐 selfEvolution 路由在诊断桶与测试 mock 中的登记(RUYI-183)
- `8136b908e` fix(migrate): 补登 919/920/921 并发索引清理注册表(RUYI-183)
- `2ffe75827` feat(prompt-quality): 七维度取数管道、看板接口与 D3 评分编排(RUYI-184)
- `147e16081` feat(self-evolution): Prompt 质量看板「质量」Tab 与七维度卡片(RUYI-184)
- `529b534f8` fix(prompt-quality): 单位随测量值下发，修正 D2/D6 显示与零样本标签(RUYI-184)
- `9cff03fd8` fix(self-evolution): 质量 Tab 测试改用 I18nProvider 的 locale 属性(RUYI-184)
- `62b1dc27b` feat(task-message): 记录每条 tool_result 的 is_error 三态(RUYI-184)
- `e7025b648` feat(prompt-quality): D3 可复现性实测与 pkg/llm 第三消费方披露(RUYI-184)
- `f55999b7d` feat(views): Prompt 版本生命周期入口（新建/激活回滚/对比/评测绑定展示）（RUYI-285）
- `7572de39b` fix(views): versions-tab 测试补 PromptQualityDashboard type import（修复 typecheck，RUYI-285 QA P1）
- `068bbcd6e` fix(prompt-quality): scope perplexity backlog by workspace and surface D2 deduction detail (RUYI-287)
- `deb660e2d` fix(views): self-evolution 组件技术值 placeholder 豁免与版本前缀模板化（修复 CI lint）

### Prompt 治理：题库测验与防回归（RUYI-185/286）

- `ae3f5f82f` feat(prompt-quiz): 题库测验与周期性防回归 (RUYI-185)
- `74e048f9f` fix(prompt-quiz): 测量尺子隔离与 outcome 语义收窄 (RUYI-185)
- `f84aa1c86` fix(prompt-quiz): workspace 删除清理生成物重生成与孤儿行清理迁移 (RUYI-185)
- `871b65028` fix(self-evolution): quiz 累积文案改为可用读数口径
- `6889ba345` feat(quiz): RUYI-286 首批基准题库与可解释评分链路
- `34203f3e9` feat(quiz): RUYI-286 评分侧前端对接（owner 面板、批量 run、样本证据）
- `b776a5f66` fix(quiz): RUYI-286 quiz 题面接入 claim 与渲染链
- `e9c70e6a4` fix(quiz): RUYI-286 quiz 任务接入 daemon 归属解析
- `b18f83614` fix(quiz): 补齐质量页运行追溯与异步评分刷新
- `d213b5f13` fix(quiz): 同步集成态 SQL 查询生成代码
- `544431481` fix(quiz): 质量页 task_id 标签接入 i18n 修复 lint 字面量错误

### 自进化：知识库、提案池与总览（RUYI-186/265/284/289/298）

- `868215abd` feat(self-evolution): record skill versions and restore history
- `09f601891` feat(self-evolution): 记录 skill 显式调用并展示版本台账 (RUYI-186)
- `8abf15645` fix(self-evolution): 区分 Skill 统计异常与零调用 (RUYI-186)
- `fdd541aed` fix(migrate): 补齐 Skill 版本索引重试清理 (RUYI-186)
- `93fb190e0` feat(self-evolution): 每日提案池、bd memories 知识镜像与工作区删除覆盖 (RUYI-265)
- `0da25c6d3` fix(self-evolution): 容忍 bd memories 数值信封并修复零版本 effect 视图 (RUYI-265)
- `f70662d4b` feat(self-evolution): 总览 tab 接入六组真实指标聚合 API（RUYI-284）
- `11df14295` refactor(self-evolution): 总览提案指标撤下现有模型数据接入，改挂起态（RUYI-284）
- `8a156e90d` feat(knowledge): 扫描执行迁移 daemon 侧 + 采纳异步化 + 系统提案接通提案池（RUYI-289 服务端）
- `8e0dd2963` feat(self-evolution): 知识库入口重做与提案池来源标识（RUYI-289）
- `a581fd669` fix(knowledge): plan 与 results 双侧排除 ultimate 目录，阻断已采纳内容回灌候选池（RUYI-289 QA P1）
- `f47229dcd` docs: 新建自进化（Prompt 治理）四语文档章节（RUYI-298）

### Prompt 立法管线（RUYI-305）

- `aebc2c462` feat(legislation): Prompt 立法管线 E1–E4（RUYI-305）

### 技能系统：输入框选择器与引用方言（RUYI-233/288）

- `1743b2879` feat(skills): 建立技能引用方言与可见性基础（RUYI-233）
- `f50cd98ee` feat(skills): wire issue skill references across clients and daemon (RUYI-233)
- `1c8a14bf4` fix(skills): 补齐技能选择器类型与评论测试（RUYI-233）
- `d4d8dca00` fix(editor): 修复任务技能菜单指派 Agent 高亮（RUYI-233）
- `59920e771` fix(skills): 全部 Agent 支持档改为头像组展示（RUYI-233）
- `8e1b94b8a` fix(views): chat / 技能选择器接入工作区全量清单并标注挂载状态
- `908c4504c` fix(skills): 技能目录统一发现/筛选/作用域与同步链路（RUYI-288）
- `dca79fc7c` fix(views): 技能来源徽标字号回归 text-micro 规格档（RUYI-288 CI type-scale 门禁）
- `60972504b` fix(migrations): 971 并发唯一索引补登记 up 清理钩子（RUYI-288 CI migrate 门禁）

### DeerFlow / zcode runtime（RUYI-283/307/321 及 runtime 身份建立）

- `a9669d113` feat(agent): 建立 deerflow 与 zcode 独立 runtime 身份
- `156bf5e46` fix(runtime): 修正 DeerFlow resume 参数契约并迁移套壳 profile 身份
- `70cacb5d0` fix(daemon): 为 zcode 与 deerflow 运行时补齐本地 skill 根目录映射
- `3d8b0cc8a` feat(deerflow): 接入会话级模型选择——下拉列表、发现与逐轮应用
- `9a33238a2` fix(deerflow): 标准部署形态下进程 cwd 接线闭环（RUYI-283 QA P1）
- `078831b53` feat(agent): zcode thinking-level catalog + claude out-of-list model effort fallback (RUYI-321 stage 1)
- `5823cb758` feat(agent): deerflow thinking switch wiring via ACP config option (RUYI-321 stage 2)
- `cfe470b61` docs(builtin_skills): deerflow 模型选择口径随支持位翻转同步（RUYI-307）

### 飞书集成：过程卡片与附件（RUYI-219/220/221/296）

- `6c8fac9df` feat(lark): 飞书过程卡片实时呈现 Agent 处理过程 (RUYI-221)
- `dd4f52552` fix(lark): 过程卡片会话身份改由 task 投递路径解析 (RUYI-221)
- `9631a4431` fix(lark): 过程卡片终帧异步收尾，退避债务下仍落地 (RUYI-221)
- `6801bcd7f` fix(lark): 终帧收敛补漏——非限流失败同样重试，hasCard 复核移入写锁后 (RUYI-221)
- `1d830101c` feat(lark): 出站文件附件投递 (RUYI-220)
- `ffca9ac2c` fix(lark): 补齐群历史文件附件读取和权限错误分类 (RUYI-219)
- `50d68c95b` fix(lark): 进度卡片补齐工具调用说明标题与结果摘要两层信息

### 任务调度与 daemon / 渠道健壮性（RUYI-224/225/229/251/259/261/275/276/277/304）

- `41b725aff` fix(tasks): claim fence keys on agent's current runtime; rebind migrates queued rows (RUYI-224)
- `5cf8909a0` fix(daemon): 探测式孤儿恢复，修 daemon 死后 in-flight 任务永久 running (RUYI-225)
- `1da343c6d` fix(daemon): 首注盲调兜底收窄为仅无 work_dir 任务，不再覆盖探测结论 (RUYI-225)
- `774756daf` fix(execenv): 清洁 worktree 起点不生成空 baseline 提交 (RUYI-229)
- `8cb3f0e72` fix(handler): agent 的 suppress_run 必须以已存在 run 为锚 (RUYI-251)
- `c77e4ce76` feat(server): 被尊重的 suppress 记 WARN 与 issue_run_suppressed 指标
- `2b0b2f4f1` feat(issues): run_suppressed 审计事件、读侧快照与暂缓徽标（RUYI-275）
- `bf221e95b` fix(issues): 补两条列表读路的 run_suppressed 快照供数（RUYI-275 返工）
- `5d4bc6805` fix(issues): run_suppressed 活动写入补 dbid.NewV7() 主键（RUYI-275 遗漏）
- `159039a7c` fix(db): 固定 channel 媒体认领 due 选行单次求值语义（RUYI-277）
- `3d5c4203f` fix(db): source context 清理认领固定单次求值防多行租约级联（RUYI-276）
- `e21d80d51` feat(channel): 普通消息 run 触发持久化（RUYI-304）
- `908a9b77b` fix(daemon): 保留仅含 context 读数的 usage 上报（RUYI-261）
- `ee1c78bcb` fix(agent): 预算停机把轮询终读数回填 usage.ContextTokens（RUYI-259）

### 迁移治理 / OAuth / 备份 / 自部署（RUYI-209/213/216/218/221/236/237/239/260）

- `cab8a109a` feat(migrations): 迁移编号唯一性门闸与重编号规程（RUYI-213）
- `28ffbe5d5` revert(migrations): 撤除迁移编号对账代码，治理改由 proj prompt 规则承载（RUYI-213）
- `942e41426` fix(migrations): 使 V1 插件批次的 down 在 344 重置后幂等
- `ddce642fc` fix(migrations): 消除 OAuth 编号冲突并提供账本恢复 (RUYI-221)
- `1b0205f13` fix(oauth): 迁移重编号至 929/930 并把 OAuth access token 归类为机器凭据(RUYI-209)
- `2cb30ef80` docs(adr): ADR 表名与实现对齐为单数 oauth_client (RUYI-209)
- `ac4e77cbc` feat(oauth): 收口 MCP OAuth 交付——发现文档代理、降级形态测试与客户端注册入口
- `e1336e5e0` feat(deploy): MCP OAuth 部署素材入库（Dockerfile.mcp / compose override / dockerignore）(RUYI-216)
- `f1a461400` fix(server): PEM 私钥环境变量支持单行 \n 转义写法（RUYI-216）
- `ed7938624` fix(make): 让 include 的 env 文件容纳多行值(RUYI-218)
- `fb7f1c3b8` fix(migrate): 迁移 907 撞号改 923，execution_profile 审计写入补铸 v7 ID
- `4c87c87a6` feat(migrate): down 执行前二次确认并打印回滚清单，迁移禁令入开发规范(RUYI-236)
- `6791cbdfb` feat(backup): 每日定时数据库 dump 备份与 7 天滚动保留（RUYI-237）
- `f5b81d18e` fix(backup): 补齐发布镜像客户端与持久备份卷（RUYI-237）
- `cc06edb48` fix(migrate): 仅在 V1 表存在时回滚 312 索引（RUYI-239）
- `4e3d6a866` fix(selfhost): selfhost* target 自动叠加 selfhost.local.yml (RUYI-260)

### VCS / GitLab（RUYI-247/248/249/264）

- `bd5543b98` feat(gitlab): 实现授权仓库分页浏览与工作区导入（RUYI-248）
- `16dbd6c76` fix(gitlab): 校验失败时不返回半截仓库列表（RUYI-248）
- `c9a5ca993` style(gitlab): 新增注释改用英文以符合仓库约定（RUYI-248）
- `003e0311f` fix(gitlab): 兼容不返回 archived/visibility 的 GitLab 实例
- `c24bdd3a0` fix(vcs): 按真实关联显示 GitLab MR 侧栏（RUYI-247）
- `ec0e71a5a` feat(vcs): 支持通用 Git 项目资源和私库错误分类（RUYI-249）
- `103763e33` fix(settings): 修复非 git 用户 SCP 仓库导入去重（RUYI-264）
- `8a2f03154` test(settings): 保留 GitLab HTTPS 与 SCP 导入去重回归（RUYI-264）

### CLI 与 backlog 语义（RUYI-253/254/255）

- `01324a0b1` fix(cli): issue status --no-start 拒绝把单静默提升出 backlog (RUYI-254)
- `b5192da86` fix(cli): 校正 no-start 发起者与受派者说明 (RUYI-254)
- `3b7576722` revert(cli): 撤销与服务端入队规则冲突的旧守卫 (RUYI-254)
- `01ed8a8e3` docs(brief): backlog 出离即交接，禁止 --no-start (RUYI-253)
- `cda664b1b` docs(skill): 删除无用的 status --no-start 示例行 (RUYI-253)
- `fd1a18e8e` fix(issue): 清洗并集成 backlog 触发规则（RUYI-255）

### 移动端（RUYI-232/235/246/314）

- `dfdb290ea` fix(mobile): invalidate unread-summary cache on inbox realtime events
- `f9d059312` fix(mobile): 待我推进列表按状态分类做服务端过滤
- `dcd34212a` feat(mobile): @ 选择单选化 + 打字输入 @ 触发选择框（RUYI-232）
- `07090df40` fix(mobile): 对齐 fbjni 版本避免 Android 原生库冲突(RUYI-235)
- `285719006` fix(mobile): 评论段内单换行升级为硬换行，对齐 web remark-breaks 行为(RUYI-235)
- `5b2da6c37` feat(servers): 桌面端与移动端启动时支持选服（RUYI-246）
- `005943806` feat(inbox): 收件箱列表响应与手机端行显示 Issue 编号（RUYI-314）
- `fdd941f97` chore(release): mobile 应用版本 0.1.0 → 0.2.0
- `92fe5c7b8` docs(mobile): Build & release 段落对齐现行 workflow（Android 发布为 push-to-main 双 Release，iOS 走 mobile-ios tag）

### 测试与 CI 基建（RUYI-228/266/271/306/310/312/316/317/322/331）

- `e0b6636d9` feat(execenv): embed gitlink children as real trees in worktree snapshots
- `2deb03cb3` fix(execenv): close gitlink path-traversal gap and harden test coverage
- `77bce2fb8` fix(repocache): gitEnv 固定 C locale，防止本地化 git 消息使分支冲突分类失效
- `0bec54cac` fix(dev-env): make up 在主检出拒绝执行，避免重写 .env 端口
- `14b64f497` fix(scripts): 修复 make list 的 OFFSET 未绑定与 web 进程归属误判
- `dba31141d` fix(scripts): dev-env destroy 放行 checkout 已丢失环境的数据库 drop（RUYI-316）
- `0a85d6461` fix(scripts): dev-env 参数校验测试设置主检出 gate 变量（修复 CI frontend-build）
- `bfb77b819` fix(test): 隔离两组环境依赖导致的单测失败(RUYI-228)
- `6f7f2144e` fix(handler): -count>=2 时按轮次重建套件夹具（RUYI-266）
- `00f273d74` test(handler): 套件运行期间保持 fixture runtime 在线
- `62014ac85` test(server): 修正未知代理提及的集成测试夹具 (RUYI-271)
- `f858c8e56` fix(server): execenv HOME 锚定测试补清 MULTICA_TASK_CONFIG_ROOT（RUYI-310）
- `e5d4e3ee0` fix(server): HOME 锚定测试补清 MULTICA_TASK_CONFIG_ROOT（RUYI-306）
- `bbbbf72f0` fix(test): isolate handler fixture emails (RUYI-312)
- `0e583d8e3` test(handler): 测试载体防再发——fixture 进程唯一命名与迁移账本前置校验（RUYI-322）
- `8daaf0156` fix(ci): 补齐 sqlc 生成产物与并发索引登记（RUYI-317）
- `f0ca375ca` test(server): 本地测试路径装配 Redis 并让门控用例可证明（RUYI-331）
- `6b6a424f4` test(server): add inbox archive realtime/persistence regression coverage

### 杂项修复与代码卫生（RUYI-256/267）

- `22c2ab3f3` fix(agent): 网关别名下发前剥离 [1m] 上下文标签（RUYI-256）
- `a06932532` refactor(agent): 模型上下文标签口径收敛至 pkg/modeltag（RUYI-267）
- `9882ced01` fix(comments): reject invalid agent mentions before writes
- `ca30a5b2e` fix(agent): hermes ACP tool_result 按状态上报 IsError 补齐三态圆点
- `f9792e073` fix（subject 原样缺失；web 布局重构与依赖整理）
- `1664de5b7` fix（subject 原样缺失；Makefile 调整）

## 2026-09 上中旬（2026-09-04 ~ 2026-09-18，2026-10-02 增补）


### 文档与 README（RUYI-61）

- `b0280d654` docs(RUYI-61): 中文 README 就位仓库主页，CHANGELOG 重定位为 UPSTREAM_DIFF 并增补近期差异
- `3fc399582` docs(RUYI-61): 根 README.md 改为指向 README.zh.md 的符号链接（Owner 拍板根软链）
- `f1261a318` docs(RUYI-61): 中文主页导航补上游差异入口链接

### Multica MCP Server（RUYI-82，stdio + streamable HTTP）

- `657f30b07` feat(mcp): add Multica MCP server with stdio + streamable HTTP (RUYI-82)
- `612ce5b17` feat(mcp): 新增 make mcp-install 六客户端一键安装

### 统一应用市场（RUYI-62/68 与发布链路安全收敛）

- `dcb9401c6` feat(marketplace): 新增统一应用市场，接入 Skill 与 MCP 动态扩展
- `7fcde1bcc` RUYI-62 为应用市场补齐 marketplace_v1 独立开关
- `8fd080112` 修正市场 Skill catalog 的上游来源路径并补齐来源守卫
- `d801ea70a` feat(marketplace): 提示词市场资产的发布、安装与应用闭环
- `d1847112b` feat(marketplace): 支持 Skill/MCP 发布、更新与撤回
- `edcccc7f9` fix(featureflags): 应用市场两个 feature flag 默认改为开启
- `4790aee9d` fix(marketplace): 闭合发布链路的 secret、入口与模板保真六项 Review 阻断
- `135d3bfe1` fix(marketplace): 修复提示词幂等分支越权读取与限定词密码字段漏检
- `22dd4d1d2` fix(marketplace): 拒绝模板中声明字段为 JSON null
- `0736ddd2f` fix(marketplace): 发布期校验 MCP 模板结构并停止回显发布者输入

### 插件平台与秘密扫描（RUYI-100 多轮返工）

- `55c88d179` feat(plugin): 补齐实例内公共目录、Market Plugin 分类与发布秘密扫描
- `af25505a1` 补齐插件平台的开发者文档、示例与密钥只写测试
- `ef54409ee` feat(plugin-sdk): 交付 manifest JSON Schema 与错误样例套件
- `35e2a5074` feat(plugin): 安装时应用同意屏配置并提供单条密钥清理入口
- `7e39cee29` feat(plugins): 补齐安装授权信息面与生命周期清理界面
- `a39cbf6d5` 修复插件跨 workspace 安装闭环、目录成员可见性与密钥边界
- `b73ce6bae` fix(plugin): 修复凭据回显与三出口过滤，补齐升级必填校验与 Schema 契约一致性
- `ea2df1f51` fix(plugins): 解析失败不回显凭据路径，事件读权限规则进 Schema，安装 fixture 补必填配置
- `392079b67` 修复 RUYI-100 Review 提出的六项阻断问题
- `7ca6de9fc` 修复秘密扫描的两处取值判定绕过（RUYI-100 第三轮返工）
- `800abbf78` 修复秘密扫描对多行 JSON 与转义 JSON 的绕过
- `0d3eec20e` 修复 quoted value 把值内转义引号当作结束定界符（RUYI-100 第五轮返工）
- `463487791` 修复可放行字段吞掉同一行后续 credential（RUYI-100 第四轮返工）
- `01e31c024` fix(promptscan): 值的结束定界符必须匹配开启引号的字符与转义层级
- `f6d631743` fix(promptscan): 多段限定词密码字段 fail-closed

### 会话压缩闸与 Run 内上下文预算（RUYI-107/148/150/151/152/154）

- `5192d9dac` RUYI-107 会话压缩闸：超阈值自动切换新会话并注入前情
- `3dbb42b2b` fix(session-gate): 按冻结规格收敛阈值范围并修复上下文快照与前情装配
- `cce59f959` fix(session-gate): stream 读数为零时从会话 transcript 恢复上下文读数
- `e479de7bf` fix(session-gate): 折叠上下文读数时按规范化模型名匹配
- `3a2460b56` fix(server): 会话前情摘要按线程级解决状态排除 reply-resolved 线程
- `359f5eca0` feat(daemon): run 内上下文预算闸——转录轮询 + 软线收敛注入 + 硬线受控分段（RUYI-148）
- `cfcab7e71` feat(daemon): run 内上下文预算闸——软线收敛注入 + 硬线受控分段（RUYI-148）
- `de38c77e5` fix(daemon): 移除软线注入死代码——实测 -p 模式排队 user 帧一律不处理
- `7089c0626` feat(daemon): max_context_tokens → CLAUDE_CODE_AUTO_COMPACT_WINDOW——原生 in-place compact
- `41b592c44` feat(daemon): 会话闸设置直通 CLI 原生 auto-compact——claim 透传 + env 注入
- `824e785f0` refactor(daemon): max_context_tokens 去写死默认——模型相关，由用户选型后自配
- `cdbd3db35` fix(agent): claude 后端接线 CompactWindowTokens/Pct env 消费点（RUYI-148）
- `78ff9654c` feat(daemon): zcode 后端接入 in-run 上下文预算（对齐 RUYI-148）
- `3ac9700a6` feat(daemon): kimi 后端 in-run 上下文预算——usage_update {used,size} 驱动硬线停机（RUYI-151）
- `f68ad29b7` feat(agent): codebuddy 后端接入原生 auto-compact env（RUYI-152）
- `98903e0d8` feat(daemon): codex 后端 in-run 上下文预算接入（RUYI-150，对齐 RUYI-148）
- `0321dc2fb` feat(daemon): claude 后端采集 run 级统计——轮次/compact次数/最大context（RUYI-154 后端部分）
- `66d5e075b` feat(observation): run 统计新增 4 字段——轮次/compact次数/最大context/结束context
- `1702abf8e` fix(daemon): run 级统计不再跨 resume 累积——max_context_tokens/compactions 限定本 run
- `e183db51e` refactor(agent): ACP 上下文占用回调去重——zcode 复用 kimi 的 onContextOccupancy
- `8029ff0a4` test(agent): claude 后端 auto-compact env 补齐进程级取证测试
- `bf959cc56` feat(daemon): runtime_config.max_turns 单 run 轮数硬限
- `c3b7eb218` fix(daemon): error_max_turns 识别为轮数预算停——路由 timeout 可重试，自动续作

### 子代理控制

- `d2ce5c051` feat(agent): 子代理工具按智能体硬禁——runtime_config.allow_subagents 开关
- `872c8f159` refactor(agent): 子代理开关挪到 设置→执行配置 卡片（并发之后）
- `7f5dccbeb` feat(daemon): 运行时策略覆盖扩展至 reasonix，未支持 provider 显式告警

### 执行配置 Profile

- `2a660ccb8` feat(execution-profile): 团队成员页新增执行配置 Profile 与一键全队切换
- `3978cd05b` fix(execution-profile): 修复三态 thinking level、思考能力校验与激活期权限复检

### 桌面端服务器切换与自动更新（RUYI-58/59/65/69）

- `4b431be7d` feat(desktop): server switcher in workspace dropdown with manage-servers CRUD (RUYI-59)
- `8098ccfdd` fix(RUYI-59): allow switching back to the built-in server
- `6b3e61d22` fix(desktop): expose server switcher on login (RUYI-65)
- `a1027f464` fix(desktop): point auto-update feed at cfgxy/multica (RUYI-69)
- `7114f15d9` feat(agents): show model column next to Runtime by default; one-time override of legacy column prefs (RUYI-58)

### 部署与发布（daemon 一键安装 / Desktop Release / iOS 镜像）

- `1ebc74fe2` feat(make): daemon 一键安装/更新目标 + systemd 单元与 OOM 守护脚本入库
- `ee0e83338` fix(deploy): daemon-install 参数化运行用户与资源规格，适配任意目标节点
- `65e35dd1f` feat(deploy): OOM 守护前置条件自检——安装后提示需用户自行配置的内核参数
- `55cee4d04` fix(deploy): 修复 Group 渲染为字面量 $(id -gn) 的引号陷阱
- `94e46a36b` feat(deploy): daemon systemd 模板实例化，make daemon-install 支持多 profile
- `e744b44f6` fix(deploy): selfhost web 构建补丁文件缺失——dockerignore 放行 patches/ 并在 deps 阶段 COPY
- `3e452303e` feat(ci): 补齐 Desktop Release 打包发版链路（RUYI-109）
- `f3252aa8d` feat(release): 补齐 iOS 与镜像发布入口
- `e45c40a9c` CI performence fix

### execenv / worktree / dev-env 健壮性（RUYI-66/75/90 等）

- `d9b12808a` fix(scripts): guard every database drop path and pin worktrees to the shared main database (RUYI-66)
- `9eb5243c3` fix(dev-env): 修正共享主库销毁提示（RUYI-66）
- `f3504f125` fix(dev-env): protect shared database fallbacks (RUYI-66, RUYI-90)
- `002fc31bb` fix(ci): 修复 main CI 迁移前缀冲突与 selfhost 测试 CI 环境失败 (RUYI-75)
- `6b4138e51` fix(execenv): 修复中文 locale 与重试下 worktree prepare 失败
- `13b37d694` fix(execenv): 交付后的 worktree 清理失败不再判死任务
- `c4284ebe6` fix(daemon): 任务凭据注入不完整时 fail-fast，不再拉起哑巴 agent
- `0f16905b7` fix(daemon): exclude agent runtime state dirs from worktree staging
- `10dbe75c6` fix(scripts): 清理工具按真实任务状态判定，未证明完成的分支不删
- `f7be446d1` fix(scripts): 无任务 ID 的旧版记录归入 UNPROVEN，可逐项确认删除

### 迁移编号治理（900 段建立）

- `ea506a8c0` chore(migrate): 将 fork 独有 migration 重编号至 900-906
- `0f0eabacf` chore(migrate): 将 RUYI-107 两个 migration 重编号至 907/908
- `e5bbc8c54` refactor(migrations): 提示词市场 7 组 migration 合并为单个 914
- `d57dc99dc` fix(migrate): 907 按跨平台 basename 迁移套壳 profile 并可逆恢复身份

### 评论时间线与评论锚点

- `9bf73a01a` feat(comment-anchor): 三端支持 mention://comment 评论锚点跳转
- `34cb44789` fix(views,mobile): 补齐手机端评论锚点 chip 并统一三端高亮时长
- `94762e782` fix(mobile): 统一评论锚点定位坐标系、保留增量回复几何、高亮改从定位成功起算
- `95ca23076` fix(mobile): 评论锚点引用回复时定位到目标本身而非所属根评论
- `66e5f1e36` fix(mobile): 评论几何上报器跟随 FlashList cell 跨 root 回收重绑
- `bb72c7e5a` fix(comments): 统一评论时间线排序
- `d9bb91314` fix(issues): 评论时间线默认按创建时间排序（Web/Desktop/Mobile 三端）
- `22aaaa1bf` fix(issues): 同步评论排序默认值的提示文案与文档说明
- `c2d28504a` fix(comments): order thread blocks by latest activity
- `9586b75b7` test(comments): 补全评论时间线 v2.2 覆盖并修复缓存边界

### 移动端功能与修复批次（RUYI-33/67/68/72/73/76/78/79/80/81 等）

- `e566f12d5` feat(mobile): agent perspective, actionable my-issues scope, inbox run badge (RUYI-76)
- `f46d78b93` feat(mobile): add scroll-to-top chip to issue timeline (RUYI-81)
- `359215965` fix(locales): restore issue timeline parity (RUYI-81)
- `451c95d66` fix(mobile): deterministic comment re-entry collapse via persisted read state (RUYI-78)
- `e6d652c6c` fix(views,mobile): render mermaid diagrams on mobile and stop cropping them on web/desktop (RUYI-80)
- `389250a54` style(views): fix Mermaid component formatting (RUYI-80)
- `86ab6dc03` fix(mobile): align mobile types with upstream Project/User additions (RUYI-67)
- `39d4c2df2` fix(mobile): remove white matte ring from Android adaptive launcher icon (RUYI-67)
- `b4859b4b1` fix(RUYI-73): mobile attachments open with minted credential-free URLs
- `f1e8a22b7` feat(mobile): 新增智能模式快速建单（RUYI-68）
- `7e0251cb1` fix(mobile): remember last submitted assignee in new-issue form (RUYI-79)
- `e69a3760b` fix(mobile): preserve post-open assignee selection (RUYI-79)
- `ecd10a2ca` fix(mobile): guard assignee memory context (RUYI-79)
- `2df3b5c0e` feat(mobile): 手机端宽表格支持点击行查看全部字段 (RUYI-72)
- `27288e228` 修复手机端运行详情句子被切碎（RUYI-33）
- `d9bb3511c` feat(mobile): 优化 Issue 时间线快速跳转按钮的位置、显隐与透明度
- `50c707894` feat(mobile): 增加输入框附件区
- `a03c301a9` feat(mobile): Issue 更多菜单支持快捷设置指派/状态/项目/优先级
- `7b91f2fba` fix(mobile): 收件箱中无关联 Issue 的通知可点击进入详情
- `0e5a613db` fix(mobile): 通知跨服务器/空间跳转先确认再切换，消除 404
- `fe946b654` fix(mobile): 修复跨服务器空间通知跳转
- `937598c57` fix(mobile): 修正通知来源工作区解析
- `804b8e220` fix(mobile): 防止通知切换失败误跳转
- `b420d769e` fix(mobile): 旧通知缺失服务器身份时安全失败
- `58ccf7d09` fix(mobile): 修复 GitHub Markdown 容器高度回传
- `5d4c6386c` fix(mobile): 修复评论富文本异步高度回传
- `17803a27b` fix(mobile): 修复同进程重开创建人恢复
- `f584cfb7f` fix(mobile): 修复创建人偏好异步竞态
- `80b0720ee` fix(mobile): 智能创建记忆的落盘完成语义与 hydration 失败状态
- `e6a9d8ce0` fix(mobile): 修复智能创建界面错误恢复上次指派对象
- `6be747601` fix(mobile): 保持服务器回滚期间的路由保护
- `8fd49f54a` fix(mobile): 修复未配置签名器时评论图片附件 401 灰框
- `8f5c73123` fix(mobile): 将组件测试移出路由目录
- `faf52a3a0` test(mobile): 补齐附件状态回归覆盖
- `3a0fe2084` test(mobile): 修复 new-issue 测试与附件区合并后的接缝失败
- `8a4cbc3f5` test(mobile): 显式释放 QueryClient 避免测试进程残留
- `eb2f1ecdb` fix(mobile): jest 命令加 --forceExit 避免 CI 挂起

### Issue 图片预览修复

- `ed504ddb3` 修复 Issue 图片预览的对象一致性、提示收敛与长停留可用性
- `59df4b938` 修复图片预览翻页时"新序号旧画面"并补齐重新签名与拒签测试

### 杂项修复

- `b0754afcc` fix(projects): disambiguate description vs instructions copy; keep create-dialog hint visible (RUYI-46 round 2)

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
