#!/usr/bin/env bash
# prompt-sync 条款标记适配（ADR 007 拆单 6 / RUYI-765）统一测试入口。
#
#   ./run.sh          GREEN：门闸单元套件 + 往返 Feature 测试，全部对适配版
#                     （本目录上一级的三个工具）跑，任何失败即非零退出。
#   ./run.sh --red    RED：同两套件对 tests/baseline/ 下的 ruyi 原版基线
#                     （e447d138…，快照/回写/门闸均不识别条款标记）复跑，
#                     必须失败——「先失败后通过」证据可随时复现。
#
# 基线与适配版的差集即本拆单的适配面；基线通过的那 1 项门闸用例是
# 无标记文件零回归锚点，属预期 PASS，不算 RED 失败缺口。
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODE=green
if [ "${1:-}" = "--red" ]; then MODE=red; fi

if [ "$MODE" = red ]; then
  GATE="$HERE/baseline/check_prompts.py"
  SNAPSHOT="$HERE/baseline/snapshot.sh"
  RESTORE="$HERE/baseline/restore.sh"
  echo "══ RED 模式：被测对象 = ruyi 基线副本（预期失败）══"
else
  GATE="$HERE/../tools/check_prompts.py"
  SNAPSHOT="$HERE/../snapshot.sh"
  RESTORE="$HERE/../restore.sh"
  echo "══ GREEN 模式：被测对象 = 适配版工具（预期全过）══"
fi

rc_gate=0
PROMPT_SYNC_GATE="$GATE" python3 "$HERE/test_check_prompts_markers.py" 2>&1 \
  | tail -n 3
[ "${PIPESTATUS[0]}" -eq 0 ] || rc_gate=1

rc_rt=0
SNAPSHOT_SH="$SNAPSHOT" RESTORE_SH="$RESTORE" GATE_PY="$GATE" \
  bash "$HERE/test_roundtrip.sh" 2>&1 | tail -n 5
[ "${PIPESTATUS[0]}" -eq 0 ] || rc_rt=1

echo "────────────────────────────────────────────"
if [ "$MODE" = green ]; then
  if [ "$rc_gate" -eq 0 ] && [ "$rc_rt" -eq 0 ]; then
    echo "GREEN 总结：门闸单元 + 往返 Feature 全部通过"
    exit 0
  fi
  echo "GREEN 总结：存在失败（门闸=$rc_gate 往返=$rc_rt），适配版不得交付"
  exit 1
fi
if [ "$rc_gate" -ne 0 ] || [ "$rc_rt" -ne 0 ]; then
  echo "RED 总结：基线按预期失败（门闸=$rc_gate 往返=$rc_rt）——先失败后通过证据成立"
  exit 0
fi
echo "RED 总结：基线意外全过，RED 证据不成立，禁止据当前基线宣称适配有效"
exit 1
