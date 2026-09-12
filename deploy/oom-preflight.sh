#!/usr/bin/env bash
# multica-oom-guard 前置条件自检（只读）：核对 OOM 分级机制依赖的内核/系统参数。
# 不满足项打印「用户自行配置」的具体命令（sysctl / grub 持久化示例），
# 本脚本绝不代改任何系统配置。
#
# 用法：bash oom-preflight.sh [cgroup路径]
#   默认 cgroup 路径：/sys/fs/cgroup/system.slice/multica-daemon.service
set -u
UNIT=multica-daemon.service
CG="${1:-/sys/fs/cgroup/system.slice/$UNIT}"
sys() { cat "/proc/sys/$1" 2>/dev/null; }
say_fix() { printf '  ✗ %s\n    → 自行修复（临时生效）：sysctl -w %s=%s\n    → 持久化：写入 /etc/sysctl.d/99-multica-oom.conf 后 sysctl --system\n' "$1" "$2" "$3"; }
say_grub() { printf '  ✗ %s\n    → 老系统需在 /etc/default/grub 的 GRUB_CMDLINE_LINUX 追加 "%s"，\n      重新生成 grub 配置（grub2-mkconfig -o /boot/grub2/grub.cfg 或 update-grub）并重启\n' "$1" "$2"; }
warn() { printf '  ! %s\n' "$1"; }
ok() { printf '  ✓ %s\n' "$1"; }

echo "== multica-oom-guard 前置自检 =="

# 1. cgroup v2：MemoryHigh/MemoryMax 与诱饵枚举（cgroup.procs 路径）依赖 unified 层级
t=$(stat -fc %T /sys/fs/cgroup 2>/dev/null)
if [ "$t" = "cgroup2fs" ]; then
  ok "cgroup v2 unified hierarchy"
else
  say_grub "cgroup 类型为 $t，守护机制依赖 cgroup v2" "systemd.unified_cgroup_hierarchy=1"
fi

# 2. overcommit_memory：模式 2 会在 malloc 阶段直接失败，绕过 OOM killer 的诱饵选择
v=$(sys vm/overcommit_memory)
if [ "$v" = 0 ]; then ok "vm.overcommit_memory=0（启发式 overcommit，OOM killer 兜底）"; else
  say_fix "vm.overcommit_memory=$v（模式 2/1 改变内存分配语义，影响诱饵机制的触发路径）" "vm/overcommit_memory" 0
fi

# 3. panic_on_oom：=1/-1 时内核直接 panic 重启而不是杀进程
v=$(sys vm/panic_on_oom)
if [ "$v" = 0 ]; then ok "vm.panic_on_oom=0"; else
  say_fix "vm.panic_on_oom=$v（=1/-1 时内存告急会 panic 整机而非杀诱饵）" "vm/panic_on_oom" 0
fi

# 4. oom_kill_allocating_task：=1 时内核杀「正在申请内存的进程」而不按 badness 选 victim
#    这是诱饵机制的核心前提——非 0 会让 daemon 本体暴露在被杀风险下
v=$(sys vm/oom_kill_allocating_task)
if [ "$v" = 0 ]; then ok "vm.oom_kill_allocating_task=0（按 badness 选 victim——诱饵机制的前提）"; else
  say_fix "vm.oom_kill_allocating_task=$v（=1 绕过 badness 选择，可能直接杀 daemon）" "vm/oom_kill_allocating_task" 0
fi

# 5. systemd-oomd：本单元已不含 ManagedOOM*，但需确认没有 slice/unit 级策略波及
if systemctl is-active -q systemd-oomd 2>/dev/null; then
  if systemctl show "$UNIT" -p ManagedOOMMemoryPressure 2>/dev/null | grep -q "kill"; then
    say_fix "$UNIT 仍配置 ManagedOOMMemoryPressure=kill（StopUnit 会整树连主进程一起杀）" "移除单元内 ManagedOOM* 行后 systemctl daemon-reload 并重启服务"
  else
    ok "systemd-oomd 运行中，但本单元未启用 ManagedOOM*（不会波及）"
  fi
else
  ok "systemd-oomd 未运行"
fi

# 6. 第三方 OOM 守护进程：按自己的规则杀进程，可能绕过诱饵优先级
if pgrep -x earlyoom >/dev/null 2>&1; then
  warn "检测到 earlyoom 在运行：其选 victim 逻辑独立于 oom_score_adj 之外的部分可能误杀 daemon，请确认其配置或停用"
fi
if pgrep -x nohang >/dev/null 2>&1; then
  warn "检测到 nohang 在运行：同上，请核对其选择策略"
fi

# 7. swap（非强制，行为说明）：有 swap 时压力先走 swap，OOM 触发更晚更温和
if swapon --noheadings 2>/dev/null | grep -q .; then
  ok "swap 存在（$(swapon --noheadings 2>/dev/null | wc -l) 个设备，swappiness=$(sys vm/swappiness)）——压力先走 swap，OOM 触发更晚更温和"
else
  ok "无 swap：路径为 限流(MemoryHigh) → 硬顶(MemoryMax) OOM，触发更直接"
fi

# 8. 守护运行态抽查（daemon 在跑才有意义）
main=$(systemctl show -p MainPID --value "$UNIT" 2>/dev/null)
if [ -n "$main" ] && [ "$main" != "0" ]; then
  adj=$(cat "/proc/$main/oom_score_adj" 2>/dev/null)
  if [ "$adj" = "-1000" ]; then
    ok "daemon 主进程 oom_score_adj=-1000"
  else
    warn "daemon 主进程 oom_score_adj=$adj（应为 -1000；确认 multica-oom-guard 服务在跑，等 1-2 秒复查）"
  fi
  prot=0; bait=0
  for pid in $(cat "$CG/cgroup.procs" 2>/dev/null); do
    [ "$pid" = "$main" ] && continue
    a=$(cat "/proc/$pid/oom_score_adj" 2>/dev/null) || continue
    if [ "$a" -ge 500 ] 2>/dev/null; then bait=$((bait+1)); else prot=$((prot+1)); fi
  done
  ok "cgroup 进程分级：受保护/中间 $prot 个，诱饵 $bait 个（新增 Agent 进程会在 1 秒内被正确分级）"
else
  warn "daemon 未运行，跳过运行态抽查"
fi

echo "== 自检完成：以上 ✗ 项请按提示自行配置；本脚本不会修改任何系统参数 =="
