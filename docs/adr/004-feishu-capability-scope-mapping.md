# ADR 004：飞书 capability→OAuth scope 映射审计（RUYI-546）

- 日期：2026年10月08日
- 状态：accepted（开发阶段自测闭环；真实飞书 live test 归 QA 阶段）
- 范围：`server/internal/integrations/lark/permission.go` capability catalog、probe、安装面板/补授权文案的权限映射依据；与 `cfgxy/cc-connect` 可用实现的复用/分叉对照。
- 执行原则（Owner 2026-10-08 指令）：以 CC Connect 当前可工作的 Feishu 实现为事实基线；scope 由实际调用的 endpoint 反推并附官方依据，禁止先拍 scope 再让代码适配。

## 1. 映射总表（endpoint → capability → scope → probe → UI 文案）

AND-of-OR 语义：每个内层组满足任一 scope 即该组满足，全部组满足才判 granted（飞书官方权限行「开启其中任意一项权限即可调用」的同构表达）。

| capability ID | 实际调用的 endpoint | scope 要求（组内任一） | probe（合成目标） | UI 文案 key（zh-Hans） |
| --- | --- | --- | --- | --- |
| `receive_messages` | 事件订阅 `im.message.receive_v1`（WS 推送） | GROUP 侧：`im:message.group_at_msg` / `im:message.group_at_msg:readonly` / `im:message.group_msg` / `im:message.group_at_msg.include_bot:readonly` / `im:message.group_msg.include_bot:read` / `im:message.group_bot_msg:readonly` / `im:message.group_msg:readonly`；P2P 侧：`im:message.p2p_msg` / `im:message.p2p_msg:readonly` | 不可 probe（事件推送无请求可挂 authz 失败，判 unknown，以真实消息到达为准） | `lark.capability_receive_messages`「接收消息（@机器人等）」 |
| `send_messages` | `POST /open-apis/im/v1/messages` | `im:message` / `im:message:send_as_bot` / `im:message:send`（历史版本，停止新增申请，保留以免老安装误判缺失） | 对合成 chat_id 发文本；23xxxx 业务码=网关已放行 | `lark.capability_send_messages`「发送消息」 |
| `read_history` | ① `GET /open-apis/im/v1/messages/{message_id}`（引用/转发/merge_forward）② `GET /open-apis/im/v1/messages`（近期群聊上下文窗口） | ①∩②的交集：基座组 `im:message` / `im:message:readonly` **且** 群组组 `im:message.group_msg` | 对合成 message_id 单条读取 | `lark.capability_read_history`「读取历史消息（引用回复、转发展开与近期群聊上下文）」 |
| `media_resources` | `GET /open-apis/im/v1/messages/{message_id}/resources/{file_key}?type=image\|file` | `im:message` / `im:message:readonly` / `im:message.history:readonly` | 对合成 message_id+file_key 下载 | `lark.capability_download_media`「下载消息中的文件」 |
| `contact_lookup` | `GET /open-apis/contact/v3/users/{open_id}?user_id_type=open_id` | 两级：API 门组 `contact:contact.base:readonly` / `contact:contact:access_as_app` / `contact:contact:readonly` / `contact:contact:readonly_as_app`；name 字段组 `contact:user.base:readonly` / 同前三项 contact 全量授权 | 对合成 open_id 单用户查询 | `lark.capability_lookup_contacts`「查询发送者信息」 |

### read_history 为何取交集

两个 endpoint 的官方基座行不同：单条 GET 只认 `im:message` / `im:message:readonly`；历史列表额外认 `im:message.history:readonly`。仅持 `im:message.history:readonly` 的安装能过列表却必然挂掉引用/转发的单条 GET——旧目录把它并入基座组会让这类安装「diff 全绿、引用全挂」。故基座组只留交集；纯列表部署（只跑近期上下文、不做引用富集）所需的替代组留本审计记录，不进 catalog。群组侧两个 endpoint 均额外要求 `im:message.group_msg`，单列一组。

### contact_lookup 为何是两级

单用户查询文档把权限分两层：API 门（四选一，决定能否调用）与字段权限（决定响应带不带该字段；`name` 归 `contact:user.base:readonly`「获取用户基本信息」，或任一 contact 全量授权）。旧目录只写 `contact:user.base:readonly` 一个 scope——仅持它的安装 API 门都过不去，这正是「飞书后台找不到所谓权限 / 姓名回退 User N」的直接成因之一。

## 2. 官方权限依据锚点

| endpoint | 官方文档页（open.feishu.cn） | 权限要求原文要点 | 取证方式 |
| --- | --- | --- | --- |
| `GET /contact/v3/users/{open_id}` | `document/server-docs/contact-v3/user/get` | API 权限四选一（contact:contact.base:readonly / access_as_app / readonly / readonly_as_app）；字段权限列出 `contact:user.base:readonly`（获取用户基本信息）等；`user_id_type` 默认 `open_id` | 本 run 直接抓取正文 |
| `GET /im/v1/messages/{id}` | `document/server-docs/im-v1/message/get` | 「开启其中任意一项权限即可调用」：im:message / im:message:readonly；注记：群聊消息还需 `im:message.group_msg`；merge_forward 消息返回 1 条合并消息 + N 条子消息 | 本 run 直接抓取正文 |
| `GET /im/v1/messages` | `document/server-docs/im-v1/message/list` | im:message / im:message:readonly / im:message.history:readonly 三选一；群聊场景另需 `im:message.group_msg` | 本 run 直接抓取正文 |
| `POST /im/v1/messages` | `document/server-docs/im-v1/message/create` | im:message / im:message:send_as_bot / im:message:send 三选一（应用身份） | 本 run 直接抓取正文 |
| `GET /im/v1/messages/{id}/resources/{file_key}` | `document/server-docs/im-v1/message-message-resource/get` | im:message / im:message:readonly / im:message.history:readonly 三选一 | 官方页 JS 渲染无法直接抓取；经官方页搜索索引（open.larksuite.com 同文）+ cc-connect 生产可用反证双重印证，QA live test 复核 |
| `im.message.receive_v1` 事件 | `document/uAjLw4CM/ukTMukTMukTM/reference/im-v1/message/events/receive` | 订阅权限表共 9 项（含两项历史版本）；群/@机器人侧 7 项、单聊侧 2 项 | 本 run 直接抓取正文 |

反证（`im:resource` 不是媒体下载的要求项）：CC Connect 生产环境可正常下载消息附件，其安装文档 `docs/feishu.md` 的权限清单不含 `im:resource`（也不含 `im:message.history:readonly`、`contact:contact.*`）。`im:resource`「获取与上传图片或文件资源」实际服务于图片/文件上传面（`POST /im/v1/images`、`POST /im/v1/files`）；旧 catalog 把它当媒体下载要求，导致安装按指引去找一个该 API 并不需要的权限。

## 3. 与 CC Connect 的复用/分叉对照（逐能力）

基线：`cfgxy/cc-connect`（本地检出 `/home/guxy/Codes/offcial/cc-connect`，分支 `guxy`，tip `9397f066d34ee1ceaec39f1807d1d27512596745`，只读；该检出 remote 已指向 `cfgxy/cc-connect`）。以下「M」= Multica，`C` = CC Connect。

### 3.1 用户姓名解析 — 复用

- C：`resolveUserName`（`platform/feishu/feishu.go:1029`）→ SDK `Contact.User.Get`，`UserIdType("open_id")`；`sync.Map` 缓存永不失效；失败/无 name 回退显示 open_id 本身。
- M：`httpAPIClient.GetUserName`（`http_client.go`）→ 同 endpoint `GET /contact/v3/users/{open_id}?user_id_type=open_id`，同参数。endpoint/参数/解析字段与 C 一致。
- 分叉（均为必要架构适配，非 endpoint/语义分叉）：
  - 缓存加 FIFO 上限（1024 条/进程，键 `app_id+open_id`）——C 的 sync.Map 无界，多租户服务化后必须限界；
  - 失败回退用 Multica 既有的位置标签「User N」而非裸 open_id（保留 Multica 入站富集既有 UX 与 hint 通路）；
  - 权限类失败接 `PermissionHintSender`（`ObserveDenied`/`ObserveSuccess`）驱动补授权提示；并发查询信号量 8（≤50 QPS/app 预算）。
  - name 为空的 code=0 响应按错误处理（「name 字段 scope 缺失」的可观测信号），对齐 C 的「no data」分支语义。

### 3.2 引用消息单条读取 — 复用

- C：`fetchSingleMessage`（`feishu.go:1243`）→ `GET /open-apis/im/v1/messages/{message_id}?card_msg_content_type=raw_card_content`，tenant token；引用链沿 `parent_id` 递归。
- M：`httpAPIClient.GetMessage` → 同 endpoint `…?user_id_type=open_id`。响应 `items`（merge_forward 时 1+N 条）同为解析输入。
- 分叉：查询参数不同——C 要 `card_msg_content_type=raw_card_content`（拿交互卡片原始 JSON），M 要 `user_id_type=open_id`（sender id 直接可作 contact 查询键）。两参数同 endpoint 不冲突；M 的卡片处理走 `content_flatten` 既有面。属有据分叉（各自消费字段不同），endpoint 与 scope 语义无分叉。

### 3.3 merge_forward 展开 — 复用

- C：对 merge_forward 消息用同一单条 GET（官方文档：该 endpoint 对 merge_forward 返回 1 条合并消息 + N 条子消息，`upper_message_id` 标父子关系），递归格式化并下载子消息附件（`formatMergeForwardTree`）。
- M：`GetMessage` 返回的 items 含子消息，`inbound_enricher` + `content_flatten.go` 展开（`http_client_getmessage_test.go:69` 钉住该契约）。endpoint、scope 需求与 C 完全一致（同 3.2）。

### 3.4 消息附件/媒体下载 — 复用

- C：`downloadImage`/`downloadResource`（`feishu.go:2015/2041`）→ SDK `Im.MessageResource.Get(message_id, file_key, type)`；失败记日志、正文留「[file: name - download failed]」占位，不阻塞消息流。
- M：`httpAPIClient.DownloadMessageResource` → 同 endpoint 同参数；失败同样降级不阻塞（`media_ingest` 面保持既有占位语义）。scope 由 `im:resource` 校正为消息读取三项（见 §2 反证）。

### 3.5 群历史（近期上下文窗口）— Multica 独有

- C：无「拉取近期群历史作上下文」的面（其上下文来自事件消息与引用链）。
- M：`ListChatMessages`（`GET /open-apis/im/v1/messages`）拉窗口。非分叉——Multica 架构特有需求，endpoint 与 scope 已按官方行单独核验（§1）。

## 4. capability ID 三端一致性

核对命令：`git grep -n '"receive_messages"\|"send_messages"\|"read_history"\|"media_resources"\|"contact_lookup"'` 于 server / packages / apps 三面，另以 `git grep -rn 'download_media\|lookup_contacts'` 确认两处仅作 locale key（映射 `media_resources`/`contact_lookup`），非独立 ID。

- server：`permission.go` catalog（唯一事实源）→ `handler/lark.go` 安装面板 API → `capability_state.go` probe → `permission_hint.go` 补授权差集。
- Web：`packages/views/settings/components/lark-tab.tsx` 五个 case 与 server ID 一一对应；四语言 locale（en/ja/ko/zh-Hans）key 齐全。
- tests：`permission_test.go`、`lark_permission_test.go`（handler）、`lark-tab.test.tsx` 全部使用同五个 ID。
- 结论：零错配、零旧符号残留。历史 ID 错配面（`download_media`/`lookup_contacts` 作为 ID）未复现。

## 5. `/file/` 分享链接能力边界

结论：**属新增能力，不在本单范围**。`cfgxy/cc-connect` 飞书 adapter（`platform/feishu/`）对正文中的 `/file/`、Drive/Docs/Wiki 分享链接零处理（`git grep "file/"`、域名模式均无命中）——Owner「CC Connect 未实现该能力」的判断独立复核成立。本单校正的 `media_resources` 只覆盖带 `message_id + file_key` 的真消息附件；云文件分享链接需要「识别链接 → 解析 token → Drive/Docs API → 下载注入」的独立链路与独立 capability/space 设计，不得混入 `media_resources`。建议 Leader 拆独立 Issue 立项（含真实飞书 live test）。

## 6. 对 RUYI-545 的接口语义说明

`CapabilitySpec` 输出形状未变（`ID` / `Scopes [][]string` AND-of-OR / `Probeable`），capability ID 集未变；变化仅在各组内 scope 清单内容（§1）。RUYI-545 的权限状态/补授权 UI 经 server catalog API 消费，无需改接口；安装面板文案中 `read_history` 的说明已更新为「引用回复、转发展开与近期群聊上下文」。live test 须覆盖：仅持 `contact:user.base:readonly` 的安装现应判 `contact_lookup` missing（旧目录误判 granted）。

## 7. 遗留与验证边界

- 单元/静态自测本 run 闭环；「同一真实飞书场景 CC Connect 能工作 Multica 也能工作」的验收（真实姓名、引用消息、merge_forward、真实附件、真实 installation token 下 probe）归 QA 阶段，本审计 §2 表即其复核清单。
- 媒体 endpoint 权限行的直接抓取因官方页 JS 渲染未成，采用双重印证（§2），QA live test 时以安装面板实际映射复核。
