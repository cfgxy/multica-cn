# ADR：Multica MCP 的 OAuth 接入与端点收口

- 创建日期：2026-09-26
- 最后核验日期：2026-09-26
- 状态：accepted（Owner 已拍 A 方案与自建授权服务器；本 ADR 固化实现契约）
- 仓库：https://github.com/cfgxy/multica.git
- 来源：RUYI-82（multica MCP），Owner 决策评论 `01a0d9f4`、`01a0dae2`、`01a0dae7`
- 验证基线：`origin/main` = `0a788b948a29b195b8d4338e1e79d908ae286ef9`（2026-09-26 07:16:59 +0800）

## 1 背景

Multica 的 MCP server（`apps/mcp`，TypeScript/Node，stateless streamable HTTP）目前只认一种凭据：
`Authorization: Bearer mul_<40 hex>`（Multica PAT）。ChatGPT 接入第三方 MCP 服务只接受 OAuth 或完全匿名，
连接面板里没有"填固定 token"这一项。目标是在**保留 PAT 路径完全不变**的前提下增加 OAuth 授权码流，
并把对外暴露面从"3000 + 3002 两个端口"收敛为"只有 3000"。

Owner 已拍定的前置决策：

| 决策 | Owner 原文出处 | 内容 |
| --- | --- | --- |
| 授权服务器自建（Go） | `01a0d9f4` | 「决策 1：A」 |
| 端点经 Next.js（3000）转发，不新增 Go 反向代理 | `01a0dae2` | 「A 先做 流式/SSE 经 Next.js 代理的行为测试，不行的话按你的建议走B方案」+ 实测通过 |
| 授权端点落 `/auth/*` | `01a0dae7` | 「授权端点可以放在 /auth/\* 我同意」 |
| 发现文档用通配路由 | `01a0dae7` | 「注册 /.well-known/oauth-protected-resource\* 路由解决」 |
| 旧约束解除 | `01a0d9f4` | 「『Credentials are exactly the CLI's credentials — no second storage format』可以改，这是以前的认知」 |

## 2 驱动因素

1. ChatGPT 的硬性要求（OpenAI《Build an MCP server》）：RFC 9728 受保护资源元数据、
   RFC 8414 授权服务器元数据且 `code_challenge_methods_supported` 含 `S256`、授权码 + PKCE、
   401 的 `WWW-Authenticate` 带 `resource_metadata` 指针。缺 `S256` 时官方明确写「MCP servers are unsupported」。
2. PAT 路径零回归：CLI、本地 stdio、已安装客户端不得受影响。
3. 自部署友好：Multica 是自研发行版，新增必须配置的外部服务与环境变量越少越好。
4. 范围纪律：不做 DCR（RFC 7591）、不做多租户联邦、不做细粒度 scope——ChatGPT 支持预置客户端，
   首版够用（依据 Owner 提供的 ChatGPT「高级 OAuth 设置」截图：client_id / client_secret / 授权 URL / Token URL / 权限范围五个输入框）。

## 3 决策

### 3.1 拓扑（A 方案，已实测）

```
ChatGPT ──HTTPS──> frpc ──> :3000 Next.js proxy.ts
                                   ├─ POST /api/mcp                        → 127.0.0.1 Node MCP（apps/mcp）
                                   ├─ /.well-known/oauth-protected-resource* → :8080 Go backend
                                   ├─ /.well-known/oauth-authorization-server → :8080 Go backend
                                   └─ /auth/oauth/*（现有 /auth/* 规则已覆盖） → :8080 Go backend
```

对外只保留 3000；MCP 进程改回绑 `127.0.0.1`，frpc 撤掉 3002 映射（部署动作，不在开发单内）。

### 3.2 Next.js 这一跳的实测结论（依据顾小鱼评论 `01a0daf6`，附件 sha256 `1dccb145442c5962d267ac334478f2adf53fa3dd5d679ce0b32297215e3ac790`）

| 风险假设 | 实测结果 |
| --- | --- |
| Next.js 改写请求头导致 MCP 拒绝 | 未发生。负向对照：单值 `Accept` 时直连与经代理**同样** 406、同样 body、同样 content-length 142 |
| Next.js 改写响应 | 未发生。`initialize`/`tools/list` 四条响应 body sha256 逐条一致，dev 与 `next start` 生产模式均一致 |
| SSE 被缓冲成一整块 | 未发生。慢速上游五个 chunk 在 +67/1061/2061/3062/4062ms 到达，与直连逐档对齐误差 <10ms；`transfer-encoding: chunked` 与 `cache-control: no-cache, no-transform` 保留 |
| `text/event-stream` content-type 被改写 | 未发生（`enableJsonResponse:false` 探针复测，与直连字节等价） |

### 3.3 路径契约

**MCP 端点 = `/api/mcp`。** 理由：`proxy.ts` 的 matcher 已含 `/api/:path*`，选 `/api/mcp` 无需改 matcher，
只需在 `runtimeRewriteDestination()` 的 `pathname.startsWith("/api/")` 分支**之前**插入更具体的规则。
选非 `/api` 前缀（如 `/mcp`）要同时改 matcher，多一处改动面，收益为零。

**发现文档 = `/.well-known/oauth-protected-resource*` + `/.well-known/oauth-authorization-server`，两者都指向 Go backend。**
必须**同时改两处**：`proxy.ts` 的 `matcher` 数组加 `"/.well-known/:path*"`，以及 `runtimeRewriteDestination()` 的规则表。
只改规则表会静默失效——兜底 matcher `/((?!api|v1|_next/static|_next/image|favicon.ico|.*\.).*)` 末尾的 `.*\.`
把含点路径整个排除在 `proxy()` 之外，规则根本不会被执行，失效形态是"一个像模像样的 Next.js HTML 404"（实测，stage-a/c/d）。
matcher 放宽后规则表对未命中的 `.well-known` 路径仍返回 `undefined`，落回 Next 文件系统路由，行为不变。

`oauth-protected-resource` 用通配是为了同时覆盖根路径形式与 RFC 9728 path-insertion 形式
（`/.well-known/oauth-protected-resource/api/mcp`），不必先猜准 ChatGPT 探测哪一个。

**授权端点 = `/auth/oauth/authorize`、`/auth/oauth/token`。** `isBackendAuthPath()` 已把 `/auth/*` 整体转给 backend，
Next.js 侧零改动。

### 3.4 认证契约：MCP 侧不做验签

关键简化：`apps/mcp/src/rest.ts:241` 今天就把调用者的 token 原样放进 `Authorization: Bearer` 调 backend，
而 backend 的 `server/internal/middleware/auth.go:177` 已经有 `mul_` 前缀与 JWT 的双分支。
因此 **MCP 侧只需放宽 `BEARER_PATTERN`（`apps/mcp/src/http.ts:26`）并把 token 原样透传**，
OAuth access token 的验签、`iss`/`aud`/`exp`/`scope` 校验只在 Go 侧实现**一份**。

这解掉了上一轮推荐 B 方案的唯一理由（双份 JWT 验签）。A 方案由此在实现成本上也不劣于 B。

MCP 侧仍须承担一件事：backend 返回 401 时，映射为带指针的 401 ——
`WWW-Authenticate: Bearer resource_metadata="<绝对 URL>"`。这是 ChatGPT 发现链的第一跳，PAT 用户不受影响。

### 3.5 令牌与密钥

- access token = RS256 JWT。`iss` = 站点根 origin，`aud` = `resource` 参数回显值，`scope` = `mcp`，`sub` = Multica user id。
- 签名私钥从环境变量 `OAUTH_SIGNING_KEY`（PEM）读取，与现有 `JWT_SECRET` 同款纪律：**未配置时 OAuth 面不启用**
  （启动 `slog.Warn`，两份发现文档不发布，`/auth/oauth/*` 返回 501），PAT 路径不受影响。零新表、零启动期密钥生成。
- 公钥经 `/.well-known/jwks.json` 发布（由私钥导出，无需持久化）。选 RS256 而非复用 HS256 `JWT_SECRET`：
  对称密钥无法发布 JWKS，而 OAuth 客户端与未来的第三方资源服务器对 JWKS 的期待是既成标准，
  规避未知客户端行为造成的返工优先于省下一个环境变量。
- **首版不发 refresh token**，access token 有效期 90 天，与现有 PAT 初始签发窗口
  （`server/internal/handler/personal_access_token.go` 的 `PATRenewExtension`）一致。

### 3.6 客户端与 scope

- 预置客户端：新增一张 `oauth_client` 表（client_id、client_secret_hash、name、redirect_uris、created_by、时间戳），
  由管理入口手动创建；Owner 把 client_id/secret 填进 ChatGPT 的高级 OAuth 设置。不做 DCR。
- 授权码存 Redis（已有 `rdb`），TTL 60s，一次性消费。不建表。
- scope 首版只有 `mcp` 一个值；per-tool `securitySchemes` 全部标同一 scope。

### 3.7 过度设计预警的处置

初稿命中"新增超过 2 个新实体"（oauth_client / authorization_codes / refresh_tokens / signing_keys）。
已简化为**新增 1 个实体**：授权码入 Redis、首版无 refresh token、签名密钥走环境变量。

## 4 备选方案

| 方案 | 结论 |
| --- | --- |
| **B｜Go backend 新增反向代理**，`/api/mcp` → Node | 未采纳。Owner 拍 A 且实测通过；3.4 的 token 透传消除了 B 的唯一优势，B 的三跳链路成为纯成本 |
| **C｜用 Go 重写 MCP 协议层** | 未采纳。与 `apps/mcp` 的 stdio 形态、`make mcp-install` 安装链路、既有测试全部脱节 |
| **接第三方 IdP（Auth0/Logto）** | Owner 已否（决策 1 = A）。每个自部署实例多一个必须配置的外部服务，与自研发行版定位有张力 |
| **HS256 复用 `JWT_SECRET`** | 未采纳。无法发布 JWKS；见 3.5 |
| **首版即做 DCR（RFC 7591）** | 未采纳。ChatGPT 支持预置客户端，首版不需要 |

## 5 后果

**正面**：对外暴露面从两个端口收敛为一个；PAT 路径零改动；JWT 验签逻辑只有一份；新增持久实体只有 1 张表。

**负面 / 需承担**：

1. **90 天长寿命 bearer token 存在 ChatGPT 侧，且首版无 refresh、无撤销 UI。** 泄露窗口与现有 PAT 同量级，
   但持有方是外部服务。缓解：token 记录在 `oauth_client` 关联的审计路径上，撤销首版靠"删除 client"这一粗粒度动作。
   **这是本 ADR 最需要 Owner 知晓的一条**；Owner 可在任一轮要求改为短寿命 + refresh 轮换。
2. `.well-known` 的 matcher 放宽会让所有含点的 `.well-known` 路径进入 `proxy()` 一次函数调用，
   规则未命中即落回原行为；开销可忽略，但这是 proxy 覆盖面的一次实质扩大，需在实现单的 Review 中核对未命中路径行为不变。
3. `OAUTH_SIGNING_KEY` 未配置时 OAuth 面静默不启用。必须有启动日志与文档，否则表现为"发现文档 404"，
   与"路由没注册"难以区分。
4. RFC 9728 path-insertion 的实际探测路径仍是**推定**（ChatGPT 探哪一个未实证）。缓解已内置：通配路由覆盖两种形式，
   加 401 头里的绝对 URL 指针，双保障。验证命令 `curl -i https://multica.huayuebridge.com/.well-known/oauth-protected-resource`，
   判定标准是返回 backend JSON 而非 Next.js HTML 404。

## 6 验收标准

1. `curl -i https://<host>/.well-known/oauth-protected-resource` 与 `.../oauth-protected-resource/api/mcp` 均返回 backend JSON（非 Next.js HTML 404）。
2. `/.well-known/oauth-authorization-server` 的 `code_challenge_methods_supported` 含 `"S256"`。
3. 授权码 + PKCE(S256) 全流程跑通，`resource` 参数在授权与换取 token 两步回显并进入 access token 的 `aud`。
4. 带 OAuth access token 的 `POST /api/mcp` 完成 `initialize` + `tools/list` + 一次只读 tool 调用。
5. **PAT 回归**：带 `mul_` PAT 的同样三次调用行为与改造前逐字段一致。
6. 无凭据的 `POST /api/mcp` 返回 401，且 `WWW-Authenticate` 含 `resource_metadata="<绝对 URL>"`。
7. `OAUTH_SIGNING_KEY` 未配置时：PAT 路径正常，OAuth 面不启用且有启动日志。
8. ChatGPT 实机连接成功并能调用工具（需 Owner 侧操作，属发布后验证，不阻断开发单交付）。

## 7 回退路径

三层，逐层可独立回退：

1. **代码层**：OAuth 面完全由 `OAUTH_SIGNING_KEY` 开关控制。清空该环境变量即回到纯 PAT 行为，无需回滚代码。
2. **路由层**：`runtimeRewriteDestination()` 的 `/api/mcp` 规则与 matcher 的 `/.well-known/:path*` 两处改动可单独 revert，
   MCP 退回独立端口对外（frpc 恢复 3002 映射）。
3. **数据层**：`oauth_client` 表为纯新增，无外键、无数据迁移，drop 即回退（遵守项目"不加数据库外键"硬约束）。

## 8 未覆盖范围

- MCP 的 GET/DELETE 方法经代理的行为未测（当前 stateless 下本就返回 405；若未来启用 session 模式需补测）。
- HTTPS / frpc 那一跳未测（超出开发授权环境，属线上配置）。
- 细粒度 scope、DCR、refresh token 轮换、token 撤销 UI 均不在首版范围。
