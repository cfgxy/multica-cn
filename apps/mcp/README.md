# @multica/mcp

Multica 的 MCP（Model Context Protocol）server：让 codex / claude code / ChatGPT
等 MCP 客户端读写 Multica——查项目、聊需求、聊进度、灵感即时落成 issue。

- 双 transport：`stdio`（本地 MCP 客户端直连）与 `streamable HTTP`（远程 connector）。
- 鉴权仅经 Multica PAT（`mul_…`，与 `multica` CLI 同源；凭据不落日志）。
- 一律经后端 REST API，不直连数据库。

## v1 工具面

| 类别 | 工具 | 说明 |
| --- | --- | --- |
| 读 | `list_workspaces` | 当前用户所属工作区 |
| 读 | `list_agents` | 工作区内可派发的 agent |
| 读 | `list_projects` | 工作区项目清单 |
| 读 | `list_issues` | 按状态/项目/负责人过滤 |
| 读 | `get_issue` | 单条 issue（默认含评论线程） |
| 读 | `search_issues` | 关键词检索 |
| 读 | `progress_digest` | 进度摘要（状态计数 + 逾期/临期 + 最近活跃） |
| 写 | `create_issue` | 通用创建：任意空间、任意项目 |
| 写 | `add_comment` | 追加评论（@agent 会触发真实派发） |
| 写 | `update_issue_status` | 状态流转（`suppress_run` 可避免连带派发） |
| 派发 | `dispatch_agent` | 一句话建 issue 并派发 agent run（消耗配额） |

v1 不暴露：删除类操作、权限/成员变更、跨用户管理。

## 凭据解析（与 CLI 同源）

优先级从高到低：

1. `--token` / `--server-url` 命令行参数
2. `MULTICA_TOKEN` / `MULTICA_SERVER_URL` 环境变量
3. CLI 配置文件：`~/.multica/config.json`（命名 profile：`~/.multica/profiles/<name>/config.json`，
   由 `--profile` / `MULTICA_PROFILE` 选择；daemon 任务沙箱内为
   `$MULTICA_TASK_CONFIG_ROOT/config.json`）

默认后端地址为 Multica Cloud（`https://api.multica.ai`）；自托管部署用
`--server-url` 或 `MULTICA_SERVER_URL` 覆盖。

## 用法

stdio（claude code 示例，`.mcp.json`）：

```json
{
  "mcpServers": {
    "multica": {
      "command": "node",
      "args": ["/path/to/apps/mcp/dist/index.js"],
      "env": { "MULTICA_TOKEN": "mul_..." }
    }
  }
}
```

生产建议不写 env，直接复用 CLI 登录态（`multica login` 后零配置）。

streamable HTTP：

```bash
node apps/mcp/dist/index.js --transport http --port 8080 --host 127.0.0.1
# 探活：curl http://127.0.0.1:8080/healthz
# MCP 端点：POST /mcp，Authorization: Bearer mul_...
```

HTTP 模式无状态：每个请求自带 PAT，服务端不保存会话。默认只绑定
loopback；对外暴露请置于 TLS 反代之后。

## 客户端注册（`make mcp-*`）

在仓库根目录用 Makefile 把这份 checkout 构建的 `dist/index.js` 注册进本机已安装的
MCP 客户端（Claude Code / Codex / Kimi / ZCode / Cursor / OpenCode），语义对齐
`daemon-*` 六件套：

```bash
make mcp-build      # 只构建 dist/，不碰任何客户端配置
make mcp-install     # 构建 + 向每个检测到的客户端写入/更新 multica 条目
make mcp-update      # 构建 + 仅刷新已注册客户端指向的 dist 路径（checkout 挪动/重建后用它修复，不会新注册未装过的客户端）
make mcp-status      # 只读：哪些客户端已注册、指向哪个 dist 路径、路径是否已失效
make mcp-uninstall   # 从每个检测到的客户端配置中移除 multica 条目，不影响其他 server 条目和文件原有格式
```

- 未检测到的客户端自动跳过；`mcp-uninstall` 对未安装 multica 的客户端同样跳过，不改动其文件。
- 配置文件解析失败（如手工改坏的 JSON/TOML）时，`mcp-update` / `mcp-uninstall` 都会跳过并保留原文件不动，不会覆盖或删除。
- `mcp-status` 的“失效”指该客户端记录的 dist 路径在磁盘上已不存在（通常是 checkout 被移动或删除），此时应 `make mcp-update`（已注册）或 `make mcp-install`（重新登记）。
- 以上流程全程不读取、不打印、不持久化任何 Multica PAT。
- 全部命令均为用户级配置写入，不需要 `sudo`。

## 开发

```bash
pnpm install
pnpm --filter @multica/mcp build      # tsc -> dist/
pnpm --filter @multica/mcp test       # vitest
pnpm --filter @multica/mcp typecheck
pnpm --filter @multica/mcp lint
```

push 到 main（或 PR）时 `.github/workflows/mcp-build.yml` 自动构建并上传
npm tarball artifact（附 SHA256）。

## 安全语义（Owner 拍板生效）

- 凭据仅 PAT，存储与 CLI 同源，不新增明文存储面。
- 日志脱敏：不含 token 与评论正文；REST 客户端只记录方法、路径模板、状态码与耗时。
- `dispatch_agent` / 评论 @agent / 状态流转均可能触发真实 agent run，消耗
  token 所属用户的配额——工具描述中已显式提示。
