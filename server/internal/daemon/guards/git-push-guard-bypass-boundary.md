# git-push-guard 绕过边界与语义说明（RUYI-403）

## 定位

git-push-guard 是**客户端行为防线**，与 RUYI-401 gh-pr-guard（PR 目标仓白名单）共用同一份 canonical 登记源 `~/.config/gh-guard/canonical-repos.conf`（格式 `owner/repo [upstream=owner/repo]`），防线互补：gh-pr-guard 管「PR 建到哪」，git-push-guard 管「push 推到哪」。两者均属纵深防御的软闸门，不是安全边界本身。

## 覆盖面（装上即生效）

- 仅 `git push` 子命令被拦截判定；其余子命令与全局选项全参透传，参数保真。
- 白名单语义：remote 解析到 `github.com` 且 `owner/repo` 在 conf 白名单内才放行；host 非 `github.com` 的同形路径（如 `github.com.evil.com`）不构成命中。
- `upstream=` 声明仓与名为 `upstream` 的 remote deny-by-default；`PUSH_GUARD_ALLOW_UPSTREAM=<owner/repo|remote名>` 显式授权可解，授权行为落审计日志。
- 受保护分支（默认 `main master`，`PUSH_GUARD_PROTECTED` 可覆盖）任何形态直推拒绝：裸名、`--force`/`-f`/`--force-with-lease`、`HEAD:main`、`+src:dst`、全限定 `refs/heads/*`、`:dst` 删除、`--delete`；PR 合流是唯一改道。
- 不可枚举目标拒绝：`--all`/`--mirror`/refspec 通配/裸 `:`/`push.default=matching`。
- 直连 URL push 拒绝（含 `https://`、`ssh://`、`git@`、scp 形态）；守卫先拦，无网络出流量。
- conf 缺失 fail-closed；日志只记时间/动作/remote 名/owner-repo/分支，无 URL、无凭据。

## 明确的绕过面（防线边界，不掩盖）

以下途径可绕过本守卫，登记为已知边界而非缺陷：

- `command git push`、`\git push`、绝对路径 `/usr/bin/git push`：wrapper 是 shell 函数，不经函数名的调用天然绕过。
- 非交互/无 rc 的 shell：`bash --norc`、`sh`、CI 脚本、cron、systemd 单元不加载 bashrc/zshrc，wrapper 不存在。
- `GIT_*` 环境变量注入：`GIT_SSH_COMMAND`、`GIT_PROXY_COMMAND`、`GIT_CONFIG_*` 等可改变传输或配置解析路径，守卫不扫描环境变量面。
- IDE/GUI（VS Code、Fork、SourceTree 等）直接调用 git 二进制，不经 shell 函数。
- alias 覆盖：用户自定义 `alias git=` 在 rc 中位于本守卫块之后时优先生效。
- `--recurse-submodules <mode>` 独立值形态等少数带独立值选项的参数消歧存在残余歧义，可能影响 refspec 推导精度；remote 级白名单判定不受影响。

结论：本守卫对「顺手误推」提供强拦截，对「刻意绕过」只提供审计成本（绕过者必须主动选择非常规路径）。团队约束的最终闸门是远端仓库自身的分支保护与权限配置，本守卫不替代它们。

## push.default 推导语义

无 refspec 时按 `remote.pushDefault` → `branch.<当前分支>.remote` → `origin` 推导实际 remote，按 `push.default`（默认 `simple`）推导 dst；`nothing` 无操作放行；detached HEAD 拒绝。

## 安装 / 卸载 / 日志

- 安装：`tools/install-git-push-guard.sh`（幂等，marker 块 `# >>> git-push-guard (RUYI-403) >>>`；conf 只种子不覆盖；仅注入已存在的 rc 文件；wrapper 函数**不 export**，子 shell 与脚本内 `git` 仍是真实二进制）。
- 卸载：`tools/uninstall-git-push-guard.sh`（移除 wrapper 块与守卫目录；**保留 conf 登记源**，RUYI-401 资产不受影响）。
- 日志：`${GIT_PUSH_GUARD_HOME:-$HOME/.config/git-push-guard}/guard.log`，仅目录存在时落盘（未安装环境静默）。`PUSH_GUARD_LOG` 可覆盖。
- 环境变量：`PUSH_GUARD_CONF`（conf 路径）、`PUSH_GUARD_PROTECTED`（受保护分支表）、`PUSH_GUARD_ALLOW_UPSTREAM`（单次显式授权）、`GIT_PUSH_GUARD_HOME`（守卫目录）。

## 与 gh-pr-guard 并存

两者 marker 块独立（`gh-pr-guard (RUYI-401)` / `git-push-guard (RUYI-403)`），安装/卸载互不触碰；conf 同源共享，任一方种子后另一方不再重复写入。
