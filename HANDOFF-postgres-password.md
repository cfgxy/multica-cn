# HANDOFF — multica 自托管 Postgres 密码反复被改问题（托孤指导书）

> 创建：2026-08-31 ｜ 作者：当日处置会话（zcode，工作目录 /mnt/data/Codes/offcial/multica）
> 状态：服务绿（自 2026-08-30 22:15 最后一次修复后稳定，2026-08-31 21:54 复核 AUTH-OK）
> 本文档与仓库根的 `HANDOFF-telegram.md` 无内容关联，独立成篇；是"postgres 密码漂移"问题的唯一权威交接文档。

---

## §0 写给后辈的信

这份文档写给下一个接手 `hp-server` 这台机器上 multica 自托管栈的人（或 agent）。你接手时服务大概率是绿的——**别被绿色骗了**。这个问题的本质不是密码错，而是**多个自治 agent 共享一个 postgres 容器、且谁都可能把它当自己的临时环境改密码**。今天（08-30）一天坏了三次，前两次我修了，第三次改密码的执行者在所有可搜的会话记录里都找不到。你迟早会在某个凌晨发现 backend 又在重启循环。

**接手后第一件事**：跑一遍 §2.1 的探活命令确认现状是绿的；然后立刻把 §3 M0 的看门狗装上（不需要任何人批准，10 分钟）。M1 的隔离方案需要 Owner 拍板，别自作主张动 QA 侧的环境。

**如果你正在处理一次新的故障**：直接跳 §2.2（修复），修完回来补 §5 执行记录。

## §1 背景与问题

### 1.1 部署拓扑（涉事机器 hp-server，用户 guxy）

- 部署目录：`/home/guxy/srv/multica`（multica 仓库的 checkout，运行 selfhost 栈）
- 栈：`docker compose -f docker-compose.selfhost.yml`，compose 项目名 `multica`
  - `multica-postgres-1`：pgvector/pgvector:pg17，数据卷 `multica_pgdata`（**2026-08-19 00:51 初始化**，密码就是那时固化的）
  - `multica-backend-1`：Go 后端 :8080，连 `postgres:5432/multica`
  - `multica-frontend-1`：Next.js :3000
- 密码事实源：`/home/guxy/srv/multica/.env` 第 4 行 `POSTGRES_PASSWORD`（本文档**不**记录明文；该值的 md5 前 8 位是 `8113d38a`，可做指纹比对）。compose 用它插值出 backend 的 `DATABASE_URL=postgres://multica:${POSTGRES_PASSWORD}@postgres:5432/multica?sslmode=disable`
- 启动命令两种：`make selfhost`（拉官方镜像 ghcr.io/multica-ai/*）和 `make selfhost-build`（本地构建，overlay `docker-compose.selfhost.build.yml`，产出 `multica-backend:dev`/`multica-web:dev`）。当前跑的是 dev 镜像。

### 1.2 核心机制：为什么密码会"漂移"

1. docker 官方 postgres 镜像的 `POSTGRES_PASSWORD` **只在空数据卷首次初始化时生效**。卷 `multica_pgdata` 一直存在 → 改 `.env` 里的密码**永远不影响库内角色密码**，反之亦然。
2. 不变量：**库内角色 `multica` 的密码必须与 `.env` 的 `POSTGRES_PASSWORD` 时刻一致**。任何一侧单方面变更 = backend 认证失败循环（SQLSTATE 28P01），症状是 `Running database migrations...` → `unable to ping database` 每 30-60 秒一轮。
3. **谁在改**：这台机器上同时跑着多套自治 agent（zcode CLI 会话、Claude Code 会话含 multica 平台 daemon 派出的 squad agent——蔡小星/金小欣等、clawgod 运行时 `~/.clawgod/cli.cjs`、VS Code claude 扩展）。RUYI-37 的 QA 会话为了让自己 worktree 起的 backend 连上共享库，执行：
   ```
   docker exec multica-postgres-1 psql -U multica -d multica -c "ALTER USER multica PASSWORD 'multica';"
   ```
   把共享角色密码改成 worktree 默认值 `'multica'`（该命令在其会话记录中出现 41 次，是 QA 环境准备/复原流程的一部分；worktree 的 `.env.worktree` 里 `POSTGRES_PASSWORD=multica`）。
4. 已核实：仓库自带的 `scripts/ensure-postgres.sh` 是安全的（只 `up -d postgres` + 缺库建库，不改密码）；shanghui 工作区的 `restore.sh`/`snapshot.sh` 也干净。**ALTER 全部是 agent 临场敲的 ad-hoc 命令**，不在任何仓库脚本里——所以单纯改脚本没用，要管住 agent（§3 M1）。

### 1.3 事件时间线（2026-08-30，当天三次故障）

| 时间 | 事件 | 证据/备注 |
|---|---|---|
| 08-19 00:51 | `multica_pgdata` 卷初始化 | `docker volume inspect` CreatedAt |
| 08-28~29 | 第一次发现（SHAN-80 期间）：down/up 后 backend 认证循环；当时已定位根因并用 ALTER USER 修复 | Claude 会话 `-mnt-data-Codes-guxy-workspace-multica-shanghui/ab5e966c*.jsonl`；备份 `.env.bak.1787851589` |
| 08-30 09:37 | 第二次：`make selfhost` 起栈后同样认证循环 | 本会话日志；09:45 修复 |
| 08-30 11:21 | RUYI-39 agent 改 `.env`：填 `CORS_ALLOWED_ORIGINS`（修 WebSocket origin 白名单，RUYI-39 的题目） | 备份 `.env.bak-ruyi39-20260830-112143`；**未动密码，无责** |
| 08-30 19:02:57 | **肇事动作**：RUYI-37 QA 会话 ALTER 共享角色密码为 `'multica'` | zcode rollout `model-io-sess_078a7db9*.jsonl`，41 处；其推理记录自述"上轮我 ALTER 过一次"，且它自己的 send-code 500 也是这导致的 |
| 08-30 19:28 | selfhost 栈重建（RUYI-39 侧改 CORS 后重启）→ 新 backend 用 `.env` 密码连库失败 → 循环 | postgres 日志 11:28 UTC（=本地 19:28）起 FATAL |
| 08-30 19:33 | 修复 #3：ALTER USER 同步回 `.env` 值，19:34 backend 自愈 | postgres 容器 19:32:56 也被重启过一次（执行者未查明，无害） |
| 08-30 19:34–20:53 之间 | **密码再次被改成 `'multica'`**。所有可搜 transcript（zcode rollout、~/.claude/projects）均无此 ALTER —— 至少一个执行者的记录不可搜（疑 clawgod 运行时或脚本化执行） | **审计盲区，未破案** |
| 08-30 20:53:53 | 认证失败恢复出现，至 22:13 累积 1.4 万+/30min，daemon 心跳、PAT 校验全挂 | postgres 日志首条 FATAL 时间 |
| 08-30 22:15 | 修复 #4（即当日第三次）：ALTER USER 同步，验证 AUTH-OK，backend 30 秒零失败 | 至 08-31 21:54 稳定 26h+ |

## §2 技术方案（检测 / 修复 / 取证）

### 2.1 检测（先跑这个判断现状）

```bash
cd /home/guxy/srv/multica
PW=$(grep '^POSTGRES_PASSWORD=' .env | cut -d= -f2- | tr -d '"' )
docker compose -f docker-compose.selfhost.yml exec -T \
  -e PGPASSWORD="$PW" postgres psql -h postgres -U multica -d multica -Atc "select 1;"
# 输出 1 = 绿；FATAL password authentication failed = 密码漂移了
```

辅助信号：
```bash
docker compose -f docker-compose.selfhost.yml logs backend --since 5m 2>&1 | grep -c "password authentication failed"
docker compose -f docker-compose.selfhost.yml logs postgres --timestamps 2>&1 | grep "password authentication failed" | head -1   # 首条失败时间 = 密码被改时间的上界
```

⚠️ **假阳性陷阱（我踩过）**：官方 postgres 镜像的 pg_hba 对容器内 `127.0.0.1` 是 **trust 免密**。在 postgres 容器里 `psql -h 127.0.0.1` 连密码都不校验，**永远会"成功"**。必须用 `-h postgres`（服务 DNS，走 TCP → scram 规则）或容器网络 IP（`docker inspect` 查，当前是 172.18.0.2 但重建会变，优先用服务名）。

### 2.2 修复（验证过的命令，直接抄）

```bash
cd /home/guxy/srv/multica
PW=$(grep '^POSTGRES_PASSWORD=' .env | cut -d= -f2- | tr -d '"' )
printf "ALTER USER multica WITH PASSWORD :'pw';\n" | \
  docker compose -f docker-compose.selfhost.yml exec -T postgres psql -U multica -d multica -v pw="$PW"
# 期望输出: ALTER ROLE
```

然后：backend 处于 restart 循环时会**自愈**（下一轮重试就连上了，约 1 分钟内）；赶时间就 `docker compose -f docker-compose.selfhost.yml up -d --force-recreate backend`。

⚠️ **两个坑**：
- `psql -c "ALTER ... :'pw'"` **不生效**——`-c` 模式不做变量插值，报 syntax error。SQL 必须从 **stdin** 传入（如上）。
- 若密码值含单引号，需按 SQL 规则双写转义（当前值不含）。

### 2.3 取证（密码被改后查是谁）

```bash
# 在 agent 会话记录里搜 ALTER（两个记录体系都要搜）
grep -rl "ALTER USER multica" ~/.zcode/cli/rollout/ ~/.claude/projects/ 2>/dev/null | xargs -r ls -lt | head
# 提取时间与上下文（rollout 是 jsonl，每行 completedAt 字段）
# 看看当下还有谁活着：
ps aux | grep -iE "zcode|claude|clawgod" | grep -v grep
```

已知盲区：clawgod 运行时（`/home/guxy/.clawgod/cli.cjs`）的执行记录不落在上述目录。08-30 第三次改密码就没抓到现行——所以 M0 看门狗的审计日志（见 §3）比事后翻记录可靠。

### 2.4 明确的"做"与"不做"

**做**：
- 密码修复后**总是**以 `.env` 为准（库向 `.env` 对齐，不是反过来）
- 用 §2.1 的探活命令做任何变更前后的验证
- 每次故障在 §5 追加记录

**不做（都是踩过的坑或明确否决的方案）**：
- ❌ **在 postgres 容器内用 `-h 127.0.0.1` 测认证**——trust 免密，假阳性
- ❌ **`psql -c` 里用 `:'var'` 变量**——不插值
- ❌ **只改 `.env` 密码就以为修好了**——卷已初始化，库不会跟着变；两侧必须同一时刻一致
- ❌ **把 `.env` 密码改成 `'multica'` 迁就 QA**——否决：等于把 QA 默认弱密码固化成生产凭据，且治不了下次的别的改动
- ❌ **pg_hba 加 trust 规则**——否决：等于取消认证
- ❌ **删卷重建 postgres 让 POSTGRES_PASSWORD 生效**——**严禁**：抹掉全部业务数据
- ❌ **QA/dev agent 对 `multica-postgres-1` 执行任何 ALTER/密码操作**——这是要写进 SOP 的红线（§3 M1）
- ❌ 在文档/日志里打印密码明文；一律运行时从 `.env` 读

## §3 任务分解与决策点

### M0：看门狗（无需批准，接手立刻做，~10 分钟）

- 入口：栈绿（§2.1 通过）
- 动作：
  1. 创建 `/home/guxy/srv/multica/scripts/pg-password-watchdog.sh`（内容：跑 §2.1 探活；FAIL 则执行 §2.2 修复；无论修复与否追加一行 `时间戳 状态` 到 `/home/guxy/srv/multica/pg-watchdog.log`）
  2. 装 host crontab（**不要**用会话级定时任务，要机器级持久）：`*/5 * * * * /home/guxy/srv/multica/scripts/pg-password-watchdog.sh >> /home/guxy/srv/multica/pg-watchdog.log 2>&1`
- 出口：连续两个周期 log 里出现 OK/已修复记录；手工把密码改成 `'multica'` 后 5 分钟内被自动纠正
- 看门狗日志本身就是**审计线索**：哪 5 分钟发生了漂移，比翻 transcript 准

### M1：根治 QA 侧改密码（需 Owner 批复，D1）

两个方案，**推荐 A**：

- **A. 隔离（推荐）**：worktree 的 backend 连自己的 postgres——QA 环境准备改用独立 compose project（`docker compose -p ruyi<nn> -f docker-compose.yml up -d postgres`，独立卷），与 `multica-postgres-1` 完全无关。仓库 `scripts/ensure-postgres.sh` 已具备该能力，只是 QA 用它时跑在了共享 project 里。
- **B. 共库分账号**：共享实例上 `CREATE ROLE qa_ruyi LOGIN PASSWORD ...` + 独立 database，QA 用自己的角色。改动小但共享实例的负载/风险仍在。

配套红线条款（写进 shanghui 工作区 `/mnt/data/Codes/guxy/workspace/multica-shanghui/prompts/` 下的 `_workspace-context.md`、`_squad-RUYI.md`、`蔡小欣.md`→实为 `金小欣.md`、`蔡小星.md`）：

> 环境红线：禁止对 `multica-postgres-1` 容器或其角色 `multica` 执行任何 ALTER/DROP/密码修改；worktree 环境的数据库只能使用独立 compose project 的自有 postgres 实例；需要共享栈配合时只允许 `up -d --force-recreate backend`。

### M2：加固（可选，优先级低）

- postgres 端口不暴露到宿主机（当前 `-p 5432` 是否映射待查）；compose 网络隔离使 worktree 容器根本路由不到 `multica-postgres-1`
- 审计盲区收口：clawgod 运行时的命令留痕

### 决策点（全部需 Owner 裁决）

| # | 决策 | 选项 | 默认推荐 |
|---|---|---|---|
| D1 | 根治方式 | A 隔离 / B 分账号 / 仅 M0 看门狗 | A |
| D2 | 红线条款写进哪些 agent prompt | 全部 squad / 仅 QA 类（金小欣） | 全部含 docker 权限的 squad |
| D3 | 未来若要换密码 | 流程必须是：先 ALTER 库 → 同一提交改 `.env` → 立刻 §2.1 验证 | — |

### 决策树

```
发现 backend 认证循环
├─ §2.1 探活 FAIL？
│   ├─ 是 → §2.2 修复 → 验证 AUTH-OK → 查 M0 看门狗为何没拦（没装？装上）→ §5 记录
│   └─ 否 → 不是本问题，去看 backend 日志其他错误
└─ 24h 内复发 ≥2 次且看门狗在岗 → 说明有高频肇事 agent → 直接把 D1-A 隔离方案摆给 Owner，别再修了
```

## §4 关键产物索引

| 产物 | 路径 |
|---|---|
| 部署目录（一切命令的 cwd） | `/home/guxy/srv/multica` |
| 密码事实源 | `/home/guxy/srv/multica/.env` 第 4 行（指纹 md5 前缀 `8113d38a`） |
| .env 历史备份 | 同目录 `.env.bak.1787851589`、`.env.bak-fix`（PEM 单行化）、`.env.bak-ruyi39-20260830-112143` |
| compose / overlay | `docker-compose.selfhost.yml`、`docker-compose.selfhost.build.yml` |
| 安全的环境脚本 | `scripts/ensure-postgres.sh`（不改密码，可放心用） |
| QA worktree 环境（肇事方模板） | `/home/guxy/Codes/offcial/worktrees/RUYI-37/.env.worktree`（`POSTGRES_PASSWORD=multica`） |
| squad prompt 落点（M1 条款） | `/mnt/data/Codes/guxy/workspace/multica-shanghui/prompts/`（`_workspace-context.md`、`_squad-RUYI.md`、`蔡小星.md`、`金小欣.md` 等） |
| 会话证据（08-30肇事） | `~/.zcode/cli/rollout/model-io-sess_078a7db9-cfc7-49f7-b7f2-aa36c1874aaf.jsonl`（19:02:57 ALTER，41 处） |
| 会话证据（当日处置） | `~/.zcode/cli/rollout/model-io-sess_b09d75ef-06f8-4a24-a1e3-99ac0c91fae7.jsonl` |
| 审计盲区运行时 | `/home/guxy/.clawgod/cli.cjs`（有进程在跑，记录不可搜） |
| 同期另一工作流的交接 | 本目录 `HANDOFF-telegram.md`（无关，勿混）；Docker 构建优化（dockerignore/缓存/国内源）已于 08-30 完成并验证，不赘述 |

## §5 任务执行记录（后续每次处置必须在下面追加）

- **R1 ｜ 2026-08-30 ｜ 当日三次故障处置**：09:37 发现第二次认证循环 → 09:45 ALTER USER 修复 + `--force-recreate backend` 全绿；19:28 第三次（根因锁定 19:02:57 RUYI-37 QA 的 ALTER，证据 41 处）→ 19:33 修复；20:53 第四次复发（执行者未破案，审计盲区）→ 22:15 修复并验证。同日完成：`.env` 多行 PEM 单行化（修 make selfhost 解析）、Docker 构建 context 从 219MB 压到 ~20MB、CN 镜像源 + BuildKit 缓存挂载、dev 镜像部署。
- **R2 ｜ 2026-08-31 21:54 ｜ 复核**：栈绿（三容器 Up 26h+），§2.1 探活 AUTH-OK；确认 restore.sh/snapshot.sh 与全部仓库脚本均无 ALTER；本指导书成文。M0/M1 待执行。
