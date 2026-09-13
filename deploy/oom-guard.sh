#!/bin/bash
# multica-oom-guard：内存压力下的分级 OOM 诱饵配平（系统级 root 服务）。
#
# 保护目标（Owner 2026-09-10 定稿）：保 Agent 持续能力与对话能力——
#   multica-daemon 主进程、各 Agent 运行时（claude/zcode-acp/zcode-cli/
#   codex/hermes/cursor/reasonix/kimi）及其拉起的 MCP 进程不被 OOM 波及；
#   Agent 运行中派生的命令（bash 工具、git、构建、测试、一次性 npm exec
#   等）按可牺牲处理。
# 机制：内核 OOM 按 badness 选 victim（adj 越高越先被杀）——
#   daemon MainPID 钉 -1000（永不选中）；
#   Agent/MCP 进程保持 0（与系统普通进程同级，永不高 bait）；
#   其余进程抬到 +500（内存告急时最先被杀的就是它们）。
set -u
MAIN_ADJ=-1000
PROTECTED_ADJ=0
BAIT=500
CG=/sys/fs/cgroup/system.slice/multica-daemon.service

# Agent 运行时与 MCP 识别。误判方向刻意偏保护：误保一条命令无害
# （OOM 转杀次高 badness），误杀 Agent 则中断对话。
is_protected() {
	local pid=$1 comm argv base
	comm=$(cat "/proc/$pid/comm" 2>/dev/null) || return 1
	argv=$(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null)
	base=$(basename "${argv%% *}" 2>/dev/null)
	case "$comm" in
		zcode-cli|zcode-acp|claude|codex|hermes|cursor|reasonix|kimi) return 0 ;;
	esac
	case "$base" in
		zcode-cli|zcode-acp|claude|codex|hermes|cursor|reasonix|kimi) return 0 ;;
	esac
	case "$argv" in
		*clawgod*) return 0 ;;  # claude shim（bun …/.clawgod/cli.cjs）
		*zcode*)  return 0 ;;   # zcode-acp 桥、zcode-cli backend、node -e 桥接、repl
		*mcp*|*MCP*) return 0 ;; # 各类 MCP server（context7/mcpvault/ssh-mcp/z_ai/aws-mcp…）
	esac
	return 1
}

while sleep 1; do
	mainpid=$(systemctl show -p MainPID --value multica-daemon.service 2>/dev/null)
	[ -n "${mainpid:-}" ] && [ "$mainpid" != "0" ] || continue
	cur=$(cat "/proc/$mainpid/oom_score_adj" 2>/dev/null) || continue
	if [ "$cur" != "$MAIN_ADJ" ]; then
		echo "$MAIN_ADJ" > "/proc/$mainpid/oom_score_adj" 2>/dev/null
	fi
	while IFS= read -r pid; do
		[ "$pid" = "$mainpid" ] && continue
		if is_protected "$pid"; then target=$PROTECTED_ADJ; else target=$BAIT; fi
		adj=$(cat "/proc/$pid/oom_score_adj" 2>/dev/null) || continue
		if [ "$adj" != "$target" ]; then
			echo "$target" > "/proc/$pid/oom_score_adj" 2>/dev/null
		fi
	done < "$CG/cgroup.procs"
done
