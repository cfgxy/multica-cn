#!/usr/bin/env bash
# snapshot→restore 往返 Feature 测试（ADR 007 拆单 6 / RUYI-765）。
#
# 沙箱形态（同 multica 仓 scripts/*.test.sh 惯例）：
#   - 自建假 ruyi 布局仓（workspace-test/ + ruyi/ + tools/），git init 有 HEAD；
#   - PATH 前置假 multica CLI（python3 实现），以 state.json 充当平台状态并记录每次调用；
#   - HOME 重定向到沙箱，unset 全部 MULTICA_*（agent 运行时会注入真实生产值）。
#
# 被测工具经环境变量切换（供 run.sh --red 模式对 ruyi 基线副本复跑）：
#   SNAPSHOT_SH / RESTORE_SH / GATE_PY   缺省为本目录上一级的适配版。
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SNAPSHOT_SH="${SNAPSHOT_SH:-$HERE/../snapshot.sh}"
RESTORE_SH="${RESTORE_SH:-$HERE/../restore.sh}"
GATE_PY="${GATE_PY:-$HERE/../tools/check_prompts.py}"

SBX="$(mktemp -d /tmp/prompt-sync-rt-XXXXXX)"
trap 'rm -rf "$SBX"' EXIT
mkdir -p "$SBX/bin" "$SBX/home/.multica"
export HOME="$SBX/home"
unset MULTICA_SERVER_URL MULTICA_TOKEN MULTICA_WORKSPACE_ID MULTICA_SPACE \
      MULTICA_DAEMON_PORT MULTICA_WORKSPACE || true
WID="12345678-1234-5678-1234-567812345678"

CTX_MARKED='# workspace context

## 空间上下文
平台侧稳定上下文，Owner 可直编。

<!-- clause:id=cls_a1b2c3d4 rev=3 -->
- **平台条款甲**：含大象词「已废弃」与「旧版」。
| 列A | 列B |
| ---- | ---- |
| 表格 | 应该尽量避免 |
<!-- /clause -->

<!-- clause:id=cls_9f8e7d6c rev=1 -->
平台第二段渲染正文。
<!-- /clause -->
'
SQ_TEXT='# squad

## 小队指令
小队稳定上下文。
'
AG_TEXT='# agent

## 成员提示词
成员稳定上下文。
'

# ── fixture 仓 ──
# snapshot/restore 以自身所在目录为仓根（ROOT=$(dirname $BASH_SOURCE)）——
# 这就是 ruyi 的真实部署形态（工具在仓根），所以被测工具复制进 fixture 仓根运行。
REPO="$SBX/repo"
mkdir -p "$REPO/workspace-test" "$REPO/ruyi" "$REPO/tools"
cp "$SNAPSHOT_SH" "$REPO/snapshot.sh"
cp "$RESTORE_SH" "$REPO/restore.sh"
# baseline 副本经 `git show >` 落盘无执行位；仓根工具按真实形态以 ./tool 方式调用
chmod +x "$REPO/snapshot.sh" "$REPO/restore.sh"
printf '%s\n' "$WID" > "$REPO/workspace-test/.workspace-id"
cat > "$REPO/tools/persona-roster.json" <<'EOF'
{"repo_managed": {"顾小鱼": []}, "platform_only": []}
EOF
cat > "$REPO/tools/prompt-structure.json" <<'EOF'
{
  "files": ["workspace-test/context.md", "ruyi/_squad-RUYI.md", "ruyi/顾小鱼.md"],
  "workspace-test/context.md": {"sections": ["空间上下文"]},
  "ruyi/_squad-RUYI.md": {"sections": ["小队指令"]},
  "ruyi/顾小鱼.md": {"sections": ["成员提示词"]}
}
EOF
cp "$GATE_PY" "$REPO/tools/check_prompts.py"
printf '%s\n' '本地旧版上下文（与平台不同，供快照产生差异）。' > "$REPO/workspace-test/context.md"
printf '%s\n' '本地旧版小队指令。' > "$REPO/ruyi/_squad-RUYI.md"
printf '%s\n' '本地旧版成员提示词。' > "$REPO/ruyi/顾小鱼.md"
git -C "$REPO" init -q
git -C "$REPO" config user.email t@example.invalid
git -C "$REPO" config user.name t
git -C "$REPO" add -A
git -C "$REPO" commit -qm init

# ── 假 multica CLI ──
cat > "$SBX/bin/multica" <<PYEOF
#!/usr/bin/env python3
import json, os, re, sys
STATE = "$SBX/state.json"
OPEN_RE = re.compile(r'^<!--\s*clause:id=(cls_[0-9a-z]{8})\s+rev=(\d+)\s*-->\s*\$', re.M)
args = sys.argv[1:]
out = []
skip = False
for i, a in enumerate(args):
    if skip:
        skip = False
        continue
    if a in ('--workspace-id', '--profile', '--output'):
        skip = True
        continue
    if a == '--context-stdin':
        out.append(('STDIN', None))
        continue
    if a == '--instructions' and i + 1 < len(args):
        out.append(('INSTR', args[i + 1]))
        skip = True
        continue
    out.append(('ARG', a))
verb = [v for k, v in out if k == 'ARG']
def load():
    return json.load(open(STATE, encoding='utf-8'))
def save(s):
    json.dump(s, open(STATE, 'w', encoding='utf-8'), ensure_ascii=False)
def log(line):
    with open("$SBX/calls.log", 'a', encoding='utf-8') as f:
        f.write(line + '\n')
def rerender(text):
    def bump(m):
        return m.group(0).replace('rev=%d' % int(m.group(2)), 'rev=%d' % (int(m.group(2)) + 1))
    return OPEN_RE.sub(bump, text)
log(' '.join(verb))
s = load()
if verb[:2] == ['workspace', 'get']:
    print(json.dumps({'context': s['workspace']['context']}, ensure_ascii=False))
elif verb[:2] == ['workspace', 'update']:
    ctx = sys.stdin.read()
    s['workspace']['context'] = rerender(ctx) if s.get('rerender') else ctx
    save(s)
    log('WRITE workspace update')
    print(json.dumps({'ok': True}))
elif verb[:1] == ['project']:
    print(json.dumps([]))
elif verb[:2] == ['squad', 'list']:
    print(json.dumps([{'name': 'RUYI', 'id': 'sq-1'}]))
elif verb[:2] == ['squad', 'get']:
    print(json.dumps({'instructions': s['squads']['RUYI']['instructions']}))
elif verb[:2] == ['squad', 'update']:
    val = next((v for k, v in out if k == 'INSTR'), None)
    s['squads']['RUYI']['instructions'] = rerender(val) if s.get('rerender') else val
    save(s)
    log('WRITE squad update')
    print(json.dumps({'ok': True}))
elif verb[:2] == ['agent', 'list']:
    print(json.dumps([{'name': '顾小鱼', 'id': 'ag-1'}]))
elif verb[:2] == ['agent', 'get']:
    print(json.dumps({'instructions': s['agents']['顾小鱼']['instructions']}))
elif verb[:2] == ['agent', 'update']:
    val = next((v for k, v in out if k == 'INSTR'), None)
    s['agents']['顾小鱼']['instructions'] = rerender(val) if s.get('rerender') else val
    save(s)
    log('WRITE agent update')
    print(json.dumps({'ok': True}))
else:
    sys.exit('fake cli: 未知动词 %r' % (verb[:2],))
PYEOF
chmod +x "$SBX/bin/multica"
export PATH="$SBX/bin:$PATH"

printf '{"workspace": {"context": %s}, "squads": {"RUYI": {"instructions": %s}}, "agents": {"顾小鱼": {"instructions": %s}}}' \
  "$(python3 -c "import json,sys;print(json.dumps(open(sys.argv[1],encoding='utf-8').read()))" <(printf '%s' "$CTX_MARKED"))" \
  "$(python3 -c "import json,sys;print(json.dumps(open(sys.argv[1],encoding='utf-8').read()))" <(printf '%s' "$SQ_TEXT"))" \
  "$(python3 -c "import json,sys;print(json.dumps(open(sys.argv[1],encoding='utf-8').read()))" <(printf '%s' "$AG_TEXT"))" \
  > "$SBX/state.json"
: > "$SBX/calls.log"

echo '{"server_url": "http://127.0.0.1:9", "token": "dummy"}' > "$HOME/.multica/config.json"

fails=0
ok()   { echo "  PASS: $1"; }
bad()  { echo "  FAIL: $1"; fails=$((fails + 1)); }
assert_contain() { grep -qF "$2" "$1" && ok "$3" || bad "$3（缺：$2）"; }
assert_not_contain() { grep -qF "$2" "$1" && bad "$3（不应出现：$2）" || ok "$3"; }
plat_ctx() { python3 -c "import json;print(json.load(open('$SBX/state.json'))['workspace']['context'],end='')"; }

echo '── 场景 1：snapshot dry-run 预览渲染段标注，不写入 ──'
(cd "$REPO" && SNAPSHOT_ASSUME_YES=1 "$REPO/snapshot.sh" --space test) > "$SBX/s1.out" 2>&1 || bad 'snapshot dry-run 非零退出'
assert_contain "$SBX/s1.out" '含条款渲染段 2 段' 'dry-run 预览标注渲染段数'
cmp -s "$REPO/workspace-test/context.md" <(printf '%s\n' '本地旧版上下文（与平台不同，供快照产生差异）。') \
  && ok 'dry-run 未写入本地文件' || bad 'dry-run 写入了本地文件'
grep -q '^WRITE workspace update' "$SBX/calls.log" && bad 'dry-run 产生了写调用' || ok 'dry-run 无写调用'

echo '── 场景 2：snapshot --apply 逐字节入库（渲染段边界保留）──'
(cd "$REPO" && SNAPSHOT_ASSUME_YES=1 "$REPO/snapshot.sh" --space test --apply) > "$SBX/s2.out" 2>&1 || bad 'snapshot apply 非零退出'
plat_ctx > "$SBX/plat-ctx.txt"
cmp -s "$REPO/workspace-test/context.md" "$SBX/plat-ctx.txt" \
  && ok '快照文件与平台逐字节一致（标记/正文/顺序原样）' || bad '快照文件与平台不一致'
grep -q '渲染段' "$SBX/s2.out" && ok 'apply 输出仍标注渲染段' || bad 'apply 输出未标注渲染段'

echo '── 场景 3：restore dry-run 只预览，平台零写入 ──'
calls_before=$(grep -c '^WRITE ' "$SBX/calls.log" || true)
(cd "$REPO" && RESTORE_ASSUME_YES=1 "$REPO/restore.sh" --space test) > "$SBX/s3.out" 2>&1 || bad 'restore dry-run 非零退出'
assert_contain "$SBX/s3.out" 'dry-run' 'dry-run 提示'
[ "$(grep -c '^WRITE ' "$SBX/calls.log" || true)" = "$calls_before" ] && ok 'dry-run 平台零写入' || bad 'dry-run 改变平台状态'

echo '── 场景 4：restore --apply 逐字节回写（平台未接线重渲染时往返稳定）──'
python3 - "$SBX" <<'EOF'
import json, sys
sbx = sys.argv[1]
s = json.load(open(f'{sbx}/state.json', encoding='utf-8'))
s['workspace']['context'] = '平台被并发改旧了。\n'
s['squads']['RUYI']['instructions'] = '小队旧内容。\n'
s['agents']['顾小鱼']['instructions'] = '成员旧内容。\n'
json.dump(s, open(f'{sbx}/state.json', 'w', encoding='utf-8'), ensure_ascii=False)
EOF
(cd "$REPO" && RESTORE_ASSUME_YES=1 "$REPO/restore.sh" --space test --apply) > "$SBX/s4.out" 2>&1 || bad 'restore apply 非零退出'
assert_contain "$SBX/s4.out" '已写入并核实一致' '逐字节回写核实'
assert_contain "$SBX/s4.out" '写入 3' '写入计数 N 核对'
plat_ctx > "$SBX/plat-ctx2.txt"
cmp -s "$REPO/workspace-test/context.md" "$SBX/plat-ctx2.txt" \
  && ok '平台上下文与仓库逐字节一致（往返稳定、不丢失不重排）' || bad '平台上下文与仓库不一致'

echo '── 场景 5：平台回写后立即重渲染 → 校验不误报、稳定上下文逐字节把关 ──'
python3 - "$SBX" <<'EOF'
import json, sys
sbx = sys.argv[1]
s = json.load(open(f'{sbx}/state.json', encoding='utf-8'))
s['rerender'] = True
s['workspace']['context'] = '平台又一次被改旧。\n'
json.dump(s, open(f'{sbx}/state.json', 'w', encoding='utf-8'), ensure_ascii=False)
EOF
(cd "$REPO" && RESTORE_ASSUME_YES=1 "$REPO/restore.sh" --space test --apply) > "$SBX/s5.out" 2>&1 || bad 'restore（重渲染）非零退出'
assert_contain "$SBX/s5.out" '稳定上下文逐字节一致' '重渲染语义：稳定上下文把关通过'
assert_contain "$SBX/s5.out" '平台已重渲染条款段' '重渲染语义：报告重渲染'
assert_not_contain "$SBX/s5.out" '回读不一致' '重渲染不得误报为不一致'
assert_not_contain "$SBX/s5.out" '丢失条款渲染段' '重渲染不得误报渲染段丢失'
plat_ctx > "$SBX/plat-ctx3.txt"
grep -q 'rev=4' "$SBX/plat-ctx3.txt" && ok '平台侧重渲染已生效（rev 推进）' || bad '平台侧未见重渲染痕迹'
stable_rc=0
python3 - "$REPO" "$SBX" <<'EOF' || stable_rc=1
import re, sys
open_re = re.compile(r'^<!--\s*clause:id=(cls_[0-9a-z]{8})\s+rev=(\d+)\s*-->\s*$', re.M)
close_re = re.compile(r'^<!--\s*/clause\s*-->\s*$', re.M)
def stable(p):
    t = open(p, encoding='utf-8').read()
    out, region = [], False
    for line in t.split('\n'):
        if not region and open_re.match(line): region = True
        elif region and close_re.match(line): region = False
        elif not region: out.append(line)
    return '\n'.join(out)
a = stable(sys.argv[1] + '/workspace-test/context.md')
b = stable(sys.argv[2] + '/plat-ctx3.txt')
sys.exit(0 if a == b else 1)
EOF
[ "$stable_rc" -eq 0 ] && ok '重渲染后平台稳定上下文与仓库逐字节一致' || bad '重渲染后稳定上下文被平台改动'

echo '── 场景 6：来源含损坏标记 → 立法门闸阻断回写，平台零写入 ──'
python3 - "$SBX" <<'EOF'
import json, sys
sbx = sys.argv[1]
s = json.load(open(f'{sbx}/state.json', encoding='utf-8'))
s['rerender'] = False
s['workspace']['context'] = '基线平台内容，不得被损坏来源覆盖。\n'
json.dump(s, open(f'{sbx}/state.json', 'w', encoding='utf-8'), ensure_ascii=False)
EOF
cp "$REPO/workspace-test/context.md" "$SBX/ctx.bak"
python3 - "$REPO" <<'EOF'
import sys
p = sys.argv[1] + '/workspace-test/context.md'
t = open(p, encoding='utf-8').read()
t = t.replace('平台第二段渲染正文。\n<!-- /clause -->', '平台第二段渲染正文。')
open(p, 'w', encoding='utf-8').write(t)
EOF
set +e
(cd "$REPO" && RESTORE_ASSUME_YES=1 "$REPO/restore.sh" --space test --apply) > "$SBX/s6.out" 2>&1
rc6=$?
set -e
[ "$rc6" != 0 ] && ok '损坏标记被门闸阻断（非零退出）' || bad '损坏标记未被阻断'
assert_contain "$SBX/s6.out" '缺少关闭标记' '门闸报出损坏类型'
plat_ctx | grep -q '不得被损坏来源覆盖' && ok '平台未被损坏来源写入' || bad '平台被损坏来源覆盖'

echo '── 场景 7：平台下发损坏标记 → 快照照常入库但显式告警 ──'
cp "$SBX/ctx.bak" "$REPO/workspace-test/context.md"
git -C "$REPO" add -A && git -C "$REPO" commit -qm restore-after-corrupt >/dev/null 2>&1 || true
python3 - "$SBX" <<'EOF'
import json, sys
sbx = sys.argv[1]
s = json.load(open(f'{sbx}/state.json', encoding='utf-8'))
bad_ctx = open(f'{sbx}/plat-ctx3.txt', encoding='utf-8').read().replace(
    '<!-- clause:id=cls_a1b2c3d4 rev=', '<!-- clause:id=cls_bad rev=', 1)
s['workspace']['context'] = bad_ctx
json.dump(s, open(f'{sbx}/state.json', 'w', encoding='utf-8'), ensure_ascii=False)
EOF
(cd "$REPO" && SNAPSHOT_ASSUME_YES=1 "$REPO/snapshot.sh" --space test --apply) > "$SBX/s7.out" 2>&1 || bad 'snapshot（损坏标记）非零退出'
assert_contain "$SBX/s7.out" '损坏标记' '快照对损坏标记显式告警'
plat_ctx > "$SBX/plat-ctx4.txt"
cmp -s "$REPO/workspace-test/context.md" "$SBX/plat-ctx4.txt" \
  && ok '备份通道照常入库（保备份优先）' || bad '快照未入库损坏标记内容'

echo
if [ "$fails" -eq 0 ]; then
  echo "往返 Feature 测试：7/7 场景 PASS（工具：$(basename "$(dirname "$SNAPSHOT_SH")")/$(basename "$SNAPSHOT_SH")）"
else
  echo "往返 Feature 测试：$fails 项 FAIL"
  exit 1
fi
