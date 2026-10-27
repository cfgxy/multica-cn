# RUYI-810：Dry Run 与深度 Review

## 范围与结论口径

Owner 指令：「执行完整思想实验 / Dry Run……发现问题则修复并补测；若 Dry Run + 深度 Review 均无问题，则可按 QA PASS 收口。」本轮按此方式复核 Linux/Bash 路径，不以思想实验冒充真实 daemon/profile 重启或其他宿主实测。

审查起点为 `3bdc338e97151b04014c2430edf694580b1ce3bf`；完整功能基线为 `e9caaf69069aeb01ffa5e407132f145bfc25eece`。读取 Codex 启动装配、环境合并、shim 生成/委托、全部相关测试和方案文档，检查失败时的进程启动边界。

## 路径与状态矩阵

| 路径 / 状态变化 | 预期与审查结果 | 证据类型 |
| --- | --- | --- |
| 新 Run、空 cache → 安装 → 启动 | 三个文件成功后才向 Codex 交付环境；shim 首选 | Go 回归 + 装配审查 |
| login profile 覆盖 PATH → 执行命令 | BASH_ENV 在 profile 后恢复 shim 优先级 | `bash -lc` 回归 |
| 原 BASH_ENV 存在 / 不存在 | 存在时先加载，再恢复 PATH；不存在时正常注入 | Go 回归 + 分支审查 |
| 原 BASH_ENV 再加入 shim 别名 → guard 子 Bash | 活动标记必须在加载原文件前检查，委托解析为 native | 本轮红→绿回归 |
| profile A 运行 → profile B 启动 → A 再发命令 | A 不能加载 B 的启动环境 | 本轮红→绿回归 |
| 同配置并发启动 / 重启 | 配置目录一致、文件原子替换，无半写脚本 | 8 个有界并发安装回归 |
| 不同配置并发启动 / 重启 | 按 home 与原 BASH_ENV 路径散列隔离，不覆盖运行中配置 | 并发回归 + 路径审查 |
| 正常 Git / gh guard → native | 参数保真，剔除自身目录及符号链接别名，无递归 | Go 回归 |
| guard 存在 → 缺失 / 不可执行 | gh 全拒绝；Git 只允许 status；写入不回退 | 两态 Go 回归 |
| 缺 guard → push / send-pack / 配置 alias | 拒绝，native Git 日志无写入调用 | 两态 Go 回归 |
| 缺 guard → `git -C <repo> status --porcelain` | 原始全局参数完整传递 | 两态参数日志回归 |
| guard 恢复为可执行 | 每次调用重新检查路径，重新委托 guard | 分支思想实验；正常态回归 |
| 空 home / 空目录 / cache 路径被文件占用 | 返回错误与 nil 环境；executeOnce 取消且不启动 Codex | 本轮异常回归 + 装配审查 |
| 安装中断 / 文件写入、chmod、rename 出错 | 错误向上传播；临时文件 defer 清理；不启动裸 Codex | 逐分支 Review |
| 合法仓、特性分支 / 合法 gh 显式仓 | 既有策略允许并保持参数 | 原 guard 离线矩阵 |
| 非法仓 / upstream / main 直推 | 既有策略拒绝 | 原 guard 离线矩阵 |
| gh 缺 --repo 且无 expected 声明 | 拒绝；有合法 expected 声明时允许注入 | 原 guard 离线矩阵 |
| daemon 停止 → 重启 → 全新 Run | executeOnce 每次重建环境，不依赖前次 shell 或安装状态 | 启动路径思想实验；本轮未实机重启 |
| agent profile 覆盖 HOME / PATH / BASH_ENV | guard 来源绑定 daemon OS home；BASH_ENV 可继承，PATH 在 Bash 命令前恢复 | 装配 Review；profile 配置隔离回归 |
| 退出 / 取消 / rollback | 既有进程树清理路径不变；回退旧构建后不再注入；cache 为惰性配置文件 | 接缝 Review / 思想实验 |
| 绝对路径、command -p、主动修改 PATH、非 Bash、其他客户端 | 属已声明的解析约束边界，不承诺不可绕过 | 范围核对 |
| Windows / macOS | Windows 明确拒绝启动；macOS 未实测，不宣称支持验收完成 | 平台分支 Review / 未覆盖 |

## 本轮发现与修复

1. **跨 Run 状态串用**：所有启动共用一个含原 BASH_ENV 路径的脚本。先安装 first 再安装 second，first 的 Shell 实际输出 second。改为按可信 home 与原启动文件路径生成独立配置目录；相同配置可安全复用。
2. **递归保护顺序错误**：原启动文件在检查活动标记前加载，可将已剔除的 shim 路径重新加入。回归中 Git/gh guard 均解析到 shim 别名而非 native。将活动标记检查移至最前；子进程沿用父进程已初始化的环境，不再重复加载启动脚本。

两项均保存修复前失败和修复后通过的原始输出。没有改变既有仓库白名单、保护分支策略、系统二进制、shell rc、数据库或部署配置。

## 测试与剩余边界

- 定向测试、完整 `pkg/agent` 回归的具体执行结果以本轮 Issue 附件为准；执行前通过 Python 环境过滤移除全部继承的 `MULTICA_*`，原始 JSON 保留失败证据。
- 原 gh guard 离线脚本：36/36 通过；原 Git guard 离线脚本：72/72 通过。策略脚本来源为 `cfgxy/ruyi/tools`；不发生 GitHub 写入。
- 前端、浏览器、bundle、数据库迁移不适用。本轮不启动服务或数据库，不抢占 dev1/dev2，不运行真实负载测试。
- QA 结论采用 Owner 本次批准的 Dry Run + Review 方法；原先真实 QA 的 FAIL 历史保留，不追溯改写。daemon 重启、其他宿主、Windows 原生支持及绝对路径拦截不属于本轮已实测结论。

## 本轮最终结果

- 深度 Review：两项新发现均已修复，无未解决阻断项；按 Owner 授权方法判定 **QA PASS（Linux/Bash 范围）**。
- `go test ./pkg/agent -parallel=4 -count=1 -timeout=240s -json`：1 个包、1331 个顶层测试通过，0 失败。
- 最终定向 `-race`：1 个包、5 个顶层测试通过，0 失败，无数据竞态报告。
- 默认并发的首次全包运行：1327 通过、4 失败；三项为 80–120ms 级时序超时，一项为 `text file busy`。失败项独立复跑 4/4 通过，降低并发后的完整复跑 1331/1331 通过。记作并发运行下的间歇性失败，不将首次红项隐去，也不据此宣称已证明某个唯一环境根因。
- 本轮未放宽测试阈值、跳过测试或修改非本单测试。具体命令、首次失败及复验 JSON 随 Issue 附件交付。
