# ADR：MCP OAuth 管理控制面（scope 三档、查表撤销与 consent 确认页）

- 创建日期：2026-10-04
- 状态：accepted（RUYI-420；Leader 分派卡 `01a10715-7b8d-7d74-a2ec-fba8312e5c37` 载五项设计选型与九条验收标准）
- 关系：修订 ADR 001 §3.6「scope 首版只有 mcp 一个值」、§5.1「无撤销 UI」两条；其余拓扑与认证契约不变

## 1 背景

ADR 001 交付了 OAuth 授权面（授权码 + PKCE、RS256 JWT、`/api/mcp` 收口），但管理能力是空白：

- access token 的 `scope` 只有 `mcp` 一个值，读、写、执行不区分；
- 撤销只有「删除 client」一个粗粒度动作，已签发的 90 天 token 无法提前失效；
- 没有 consent 确认页，授权即静默全量；没有管理 UI，客户端与授权记录只有 CLI 可查。

本 ADR 固化 RUYI-420 的实现契约。

## 2 决策

### 2.1 撤销 = 授权记录查表闸门

access token 在原有 claims 之上增加 `cid`（client_id）与 `gid`（oauth_grant.id）。新增 `oauth_grant`
表（迁移 924，见下），每行一个 (client, user) 授权。`middleware.Auth` 验签后查该行状态：
grant 已撤销、client 已禁用或 client 不存在即 401，与 JWT 剩余有效期无关。

查表结果进 Redis 缓存（key `mul:auth:oauthgrant:<gid>`，TTL 与 PAT 缓存同款 10 分钟，
`server/internal/auth/oauth_gate.go` 与 `pat_cache.go` 同构）。撤销/禁用/删除的写路径主动失效
管理链路缓存；CLI（`cmd/oauth_client`）没有 Redis 句柄，其变更在 TTL 窗口内收敛，已写入 CLI 文档注释。
存量无 `cid`/`gid` 的 token 按 legacy 处理（闸门放行），自然到期淘汰。

迁移 924（单编号原子交付）：

1. `oauth_client` 增列 `disabled_at`/`disabled_by`（软禁用）、`secret_updated_at`（当前 secret 写入时间，用于展示「轮换于」而非 hash）；
2. 新表 `oauth_grant`（client_id、user_id、scope、created_at、last_used_at、revoked_at），
   `UNIQUE(client_id, user_id)` 支撑 authorize 时的 upsert 判定；
3. 无外键（仓库硬规则），引用关系在应用层维护；删 client 先撤 grants 再删行，grant 行保留作审计与「我的授权」历史。

### 2.2 scope 三档

`mcp:read` / `mcp:write` / `mcp:run`。存量与兼容值 `mcp` = 全量。校验单点在 `middleware.Auth`：
Go 后端看到的是 MCP 转发后的真实 method + path（Node 侧不验签，ADR 001 §3.4），按方法与路径映射
所需档位判定；`tools/list` 全量展示，越权调用返回 403。authorize 的 `scope` 参数经
`internal/oauth/scope.go` 的 `NormalizeRequestScope` 校验：未知 scope 值整请求拒绝
（重定向 `invalid_scope`，不静默剔除——consent 展示的必须与实际授予一致），参数缺省
回落 `mcp` 全量，写入 grant 行。

### 2.3 Secret 与 client 生命周期

- secret **原位轮换**：`secret_hash` 单列即时替换，旧 secret 立即失效，无双 hash 宽限期；
  已签发 token 按 2.1 的闸门语义继续有效至过期或撤销。ADR 001 的「轮换 = 建 新 client 删旧 client」作废。
- client **软禁用**（`disabled_at`）：禁用即撤销其全部 live grants；重新启用只恢复发起授权的能力，不恢复已撤销的 grants。
- **删除保留**：撤 grants 后物理删行（grants 行保留）。

### 2.4 TTL 与 refresh

维持 ADR 001 §3.5：access token 90 天、不发 refresh token。撤销能力由 2.1 的闸门补足，
不再需要「短寿命 + refresh 轮换」。

### 2.5 consent 确认页

authorize 验证通过后将请求落入 Redis（`internal/oauth/consent_store.go`，TTL 10 分钟），
302 到 `{站点根}/oauth/consent?request=<ticket>`。确认页渲染 client 名与 scope 说明
（scope 的展示文案在前端 locale，Go 只传稳定键），approve/deny 各自 302 回原 redirect_uri
（deny 带 `access_denied`）。approve 持久化 `oauth_grant` 行（upsert），并使旧 gate 缓存失效。

### 2.6 管理面与审计

- 管理端点全部在 `/api/admin` + `RequireSuperAdmin` 之内：`/api/admin/oauth/clients`（GET/POST）、
  `clients/{id}`（GET/PATCH/DELETE）、`clients/{id}/disabled`（PATCH）、`clients/{id}/rotate`（POST）、
  `/api/admin/oauth/grants`（GET）、`grants/{id}`（DELETE）、`/api/admin/mcp/status`（GET）。
- 用户侧 `GET/DELETE /api/oauth/grants(/{id})`：仅本人记录；撤销做所有权检查后才落写，他人 grant 返回 404 不泄露存在性。
- 管理写操作全部落 `admin_audit_log`（action 前缀 `oauth_client.*` / `oauth_grant.*`）；metadata 不含任何 secret 或 hash。
- 明文 secret 仅存在于 create 与 rotate 的单次响应；list/detail 响应无 hash 字段，前端 schema 层面即无该字段。
- `/api/admin/mcp/status` 聚合 OAuth 面配置与本进程对 Node MCP `GET /diag` 的探活（3s 超时，任何失败降级为不可达）；
  `/diag` 只返回启动期静态计算的工具名与描述清单，无凭据、不触达后端、不可能触发 Agent Run。

## 3 后果

**正面**：撤销从「删 client + 等 90 天」变为「缓存窗口（≤10 分钟）内生效」；读/写/执行权限可分-client 授予；
用户可自查自撤授权；全部管理动作有审计。

**负面 / 需承担**：

1. `middleware.Auth` 对每个 OAuth token 多一次缓存查表；缓存击穿时表现为一次 DB 查询。
   Redis 故障且查库也失败时闸门 **fail-closed**（`ErrGateUnavailable` → 401，`rejectDeadGrant`，
   与 token endpoint 的 store 取向一致）：OAuth token 在双重故障期间不可用，PAT 路径不受影响。
2. 存量 legacy token（无 `cid`/`gid`）不受闸门约束，最长存活至签发后 90 天；紧急场景用「删 client」覆盖
   （token 的 `cid` 对应 client 不存在即 401）。
3. `oauth_grant.last_used_at` 受闸门缓存节流，至多每 TTL 窗口刷新一次，是近似值而非精确审计。
4. Node `/diag` 扩大了 MCP 进程的 HTTP 面：仅 GET、静态、默认绑定 `127.0.0.1`
   （`MULTICA_MCP_HOST` 未配置时的缺省值），暴露的信息只有工具名与描述。

## 4 回退路径

1. **代码层**：闸门构造点在 `router.go`（`oauthGate` 闭包 + `middleware.Auth` 第 6 参），
   传 nil 语义即回到纯 JWT 验签（未采用，保留描述以备回退）。
2. **数据层**：`oauth_grant` 为纯新增表，drop 即回退；`oauth_client` 三个新列均可安全 drop。
3. **前端层**：admin 两个 tab 与 Settings「我的授权」tab 为纯新增入口，移除路由项即不可见。
