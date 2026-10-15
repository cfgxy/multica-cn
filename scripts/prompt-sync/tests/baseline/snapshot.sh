#!/usr/bin/env bash
# 多空间提示词仓库 快照工具（Multica 平台 → 本仓库）
#
# 仓库布局：
#   ruyi/                    小队 + 成员提示词（空间无关，全空间一致）
#   workspace-<空间>/         context.md（空间上下文）+ proj-<项目名>.md（项目指令）+ 空间绑定
#
# 默认 dry-run，只拉取平台内容与本地对比并显示差异，不写入本地文件、不 commit；
# 加 --apply 且确认（yes）后才真正写入并提交。写入前先核对工作树洁净度：若存在
# 与本次快照无关的改动，拒绝执行（防止裹挟提交），提交时只暂存本次实际写入的文件
# （pathspec 精确指定，不用 -A）。
#
# 空间绑定（用户只面向空间操作，profile 由空间自动解析，禁止手工拼）：
#   workspace-<空间>/.workspace-id   该空间完整 36 位 UUID（目录归属的真源）
#   workspace-<空间>/.profile        所属 multica 实例的 CLI profile 名；无此文件 = 默认实例
#
# 目标空间（优先级从高到低）：
#   --space <目录短名|完整UUID> > MULTICA_SPACE > MULTICA_WORKSPACE_ID > 仓库根 .workspace-id
# 一切平台调用显式传 --profile（按空间绑定解析）与 --workspace-id，永不依赖 CLI 默认值。
#
# 快照范围：空间上下文、全部项目指令、小队指令、全部成员提示词。
# 快照历史即回退依据（配合 restore.sh <SHA>）。
#
# 硬性条款：iData 空间上下文与项目提示词均不进 GitHub 仓库，只在 devstation 节点本地维护
# （项目：2026-09-14 Owner 批复；空间上下文：2026-09-24 Owner 指令，本地跟踪仓
# ~/multica-idata-local-prompts）。本脚本对 workspace-idata 拒绝 context.md 与 proj-*.md，
# 目录内出现即报错退出；通用 ruyi/ 层照常快照。dry-run 与 apply 均生效。
#
# 用法：
#   ./snapshot.sh                       # dry-run：只显示平台与本地的差异，不写入不提交
#   ./snapshot.sh --space idata         # 显式指定空间（目录短名或完整 UUID）
#   ./snapshot.sh --apply               # 展示差异后询问一次，确认（yes）才写入并提交
#   SNAPSHOT_ASSUME_YES=1 ./snapshot.sh --apply   # 非交互自动化场景显式放行确认
#
# 工作树洁净度：--apply 写入前检查 `git status --porcelain`，若存在不在本次快照
# 写入清单内的改动（即与快照无关的在途工作），直接拒绝执行并列出这些文件，避免
# 自动提交把它们裹挟进去；需要用户先自行 commit/stash 后重试。

set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

SEL=""; APPLY=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) sed -n '2,37p' "${BASH_SOURCE[0]}" | sed 's/^# \?//'; exit 0;;
    --space) SEL="$2"; shift 2;;
    --apply) APPLY=1; shift;;
    *) echo "未知参数：$1" >&2; exit 1;;
  esac
done
[ -n "$SEL" ] || SEL="${MULTICA_SPACE:-}"

WS_DIR=""; WID=""
if [ -n "$SEL" ]; then
  for d in "$ROOT"/workspace-*/; do
    name="$(basename "$d")"
    if [ "$SEL" = "$name" ] || [ "$SEL" = "${name#workspace-}" ]; then
      WS_DIR="${d%/}"; WID="$(cat "$WS_DIR/.workspace-id" 2>/dev/null || true)"; break
    fi
  done
fi
if [ -z "$WS_DIR" ]; then
  WID="${SEL:-${MULTICA_WORKSPACE_ID:-$(cat "$ROOT/.workspace-id" 2>/dev/null || true)}}"
  for d in "$ROOT"/workspace-*/; do
    if [ "$WID" = "$(cat "$d.workspace-id" 2>/dev/null || true)" ]; then WS_DIR="${d%/}"; break; fi
  done
fi
[ -n "$WS_DIR" ] || { echo "错误：无法解析目标空间（--space / MULTICA_SPACE / MULTICA_WORKSPACE_ID / 根 .workspace-id）" >&2; exit 1; }
[[ "$WID" =~ ^[0-9a-fA-F-]{36}$ ]] || { echo "错误：空间 UUID 必须是完整 36 位（收到：$WID），短前缀一律拒绝——见运维手册硬规则 8" >&2; exit 1; }

PROFILE="$(cat "$WS_DIR/.profile" 2>/dev/null || true)"
CFG="$HOME/.multica/config.json"
[ -n "$PROFILE" ] && CFG="$HOME/.multica/profiles/$PROFILE/config.json"
SERVER_URL="$(python3 -c "import json;print(json.load(open('$CFG')).get('server_url',''))" 2>/dev/null || true)"
echo "目标空间：$WS_DIR（$WID @ ${SERVER_URL:-实例未知}，profile=${PROFILE:-默认}）"
echo "模式：$([ "$APPLY" = 1 ] && echo '--apply（差异确认后写入并提交）' || echo 'dry-run（只读预览，不写入不提交）')"

# ── 硬性条款：iData 空间上下文与项目提示词不进 GitHub 仓库 ──
# iData 的平台 workspace/project instructions 只在 devstation 节点本地维护（本地跟踪仓
# ~/multica-idata-local-prompts）；本空间拒绝二者快照与入库，目录内出现 context.md 或
# proj-*.md 即视为违规并退出；ruyi/ 共享层不受影响。dry-run 与 apply 均执行此校验。
NOREPO_PROJ=0; NOREPO_CTX=0
if [ "$WID" = "6c337a4d-0c82-4c1c-a46c-630258b2ec34" ] || [ "$(basename "$WS_DIR")" = "workspace-idata" ]; then
  NOREPO_PROJ=1; NOREPO_CTX=1
  if ls "$WS_DIR"/proj-*.md >/dev/null 2>&1; then
    echo "错误：硬性条款——iData 项目提示词只在 devstation 节点本地维护，不进 GitHub 仓库。" >&2
    echo "以下文件不得存在于 $WS_DIR，请移出或删除后重试：" >&2
    ls "$WS_DIR"/proj-*.md >&2
    exit 1
  fi
  if [ -f "$WS_DIR/context.md" ]; then
    echo "错误：硬性条款（2026-09-24 Owner 指令）——iData 空间上下文不进 GitHub 仓库。" >&2
    echo "维护位置是 devstation 本地跟踪仓 ~/multica-idata-local-prompts；违规文件：$WS_DIR/context.md，请移出或删除后重试。" >&2
    exit 1
  fi
fi

export WID ROOT WS_DIR PROFILE NOREPO_PROJ NOREPO_CTX APPLY
python3 - <<'PYEOF'
import json, os, subprocess, sys, difflib

ROOT, WS_DIR, WID = os.environ['ROOT'], os.environ['WS_DIR'], os.environ['WID']
PROFILE = os.environ.get('PROFILE', '')
APPLY = os.environ.get('APPLY') == '1'
MC = ['multica'] + (['--profile', PROFILE] if PROFILE else [])

def run(*args):
    r = subprocess.run(MC + list(args), capture_output=True, text=True)
    if r.returncode != 0:
        print('CLI 失败:', ' '.join(args[:3]), r.stderr[:200]); sys.exit(1)
    return r.stdout

def jrun(*args):
    return json.loads(run(*args, '--workspace-id', WID, '--output', 'json'))

def read_local(path):
    return open(path, encoding='utf-8').read() if os.path.exists(path) else None

def norm(s): return (s or '').rstrip('\n')

def rel(path): return os.path.relpath(path, ROOT)

# ── 阶段一：只读拉取平台内容，与本地逐项比对，不做任何写入 ──
targets = []  # (path, new_content, label)

if os.environ.get('NOREPO_CTX') == '1':
    print('  [硬性条款] iData 空间上下文不进 GitHub 仓库——跳过空间上下文快照（本地跟踪仓：~/multica-idata-local-prompts）')
else:
    ws = jrun('workspace', 'get')
    targets.append((os.path.join(WS_DIR, 'context.md'), ws.get('context') or '', '空间上下文'))

if os.environ.get('NOREPO_PROJ') == '1':
    print('  [硬性条款] iData 项目提示词不进 GitHub 仓库——跳过项目指令快照')
else:
    d = jrun('project', 'list')
    rows = d if isinstance(d, list) else d.get('projects') or []
    for p in rows:
        title = (p.get('title') or '').strip()
        if not title:
            print('  [跳过] 无标题项目', p.get('id', '')[:8]); continue
        inst = p.get('instructions') or ''
        targets.append((os.path.join(WS_DIR, f'proj-{title}.md'), inst, f'项目指令：{title}'))
    print('  项目指令读取完成（%d 个项目）' % len(rows))

d = jrun('squad', 'list')
rows = d if isinstance(d, list) else d.get('squads') or []
squad = next((s for s in rows if s.get('name') == 'RUYI'), None)
if squad:
    cur = json.loads(run('squad', 'get', squad['id'], '--workspace-id', WID, '--output', 'json')).get('instructions') or ''
    targets.append((os.path.join(ROOT, 'ruyi', '_squad-RUYI.md'), cur, '小队指令'))
else:
    print('  [警告] 本空间无 RUYI 小队')

with open(os.path.join(ROOT, 'tools', 'persona-roster.json'), encoding='utf-8') as _rf:
    _roster = json.load(_rf)
_managed = _roster['repo_managed']
AGENTS = [n for n, sps in _managed.items() if not sps or WID in sps]
_local = {f[:-3] for f in os.listdir(os.path.join(ROOT, 'ruyi'))
          if f.endswith('.md') and f != '_squad-RUYI.md'}
_unknown = _local - set(_managed)
if _unknown:
    raise SystemExit('persona-roster 未登记的仓内 persona：%s —— 先更新 tools/persona-roster.json' % '、'.join(sorted(_unknown)))
d = jrun('agent', 'list')
rows = d if isinstance(d, list) else d.get('agents') or []
amap = {a['name']: a['id'] for a in rows if not a.get('archived_at')}
for name in AGENTS:
    aid = amap.get(name)
    if not aid:
        print('  [提示] 本空间无该成员（roster 适用空间不含此空间或平台未建）：', name); continue
    inst = json.loads(run('agent', 'get', aid, '--workspace-id', WID)).get('instructions') or ''
    targets.append((os.path.join(ROOT, 'ruyi', f'{name}.md'), inst, f'成员提示词：{name}'))
_extra = [a['name'] for a in rows if not a.get('archived_at') and a['name'] not in _managed]
_unclassified = [n for n in _extra if n not in _roster.get('platform_only', [])]
if _unclassified:
    print('  [警告] 平台成员未分类（既不入仓也不在 platform_only）：', '、'.join(_unclassified))
elif _extra:
    print('  [提示] 平台成员不入仓（platform_only）：', '、'.join(_extra))
print('  成员提示词读取完成（%d 人）' % len(AGENTS))

diffs = []
for path, new, label in targets:
    old = read_local(path)
    if norm(old) == norm(new):
        print('  [一致]', rel(path))
    else:
        diffs.append((path, old, new, label))

def show_diff(path, old, new, label):
    print(f'--- 差异：{label}（{rel(path)}）---')
    d = list(difflib.unified_diff(norm(old).splitlines(), norm(new).splitlines(),
                                   '本地当前', '平台最新', lineterm='', n=1))
    for l in d[:40]: print(l)
    if len(d) > 40: print(f'... 共 {len(d)} 行差异，此处截断。')

if not APPLY:
    if not diffs:
        print()
        print('平台与本地一致，无差异。')
    else:
        print()
        for path, old, new, label in diffs:
            show_diff(path, old, new, label)
        print()
        print(f'共 {len(diffs)} 个文件存在差异（以上为预览，dry-run 未写入、未提交）。')
        print('确认无误后加 --apply 执行。')
    sys.exit(0)

# ── 阶段二：--apply——先做洁净度校验，再确认，确认后才写入并提交 ──
if not diffs:
    print()
    print('平台与本地一致，无需写入，未产生新快照。当前 SHA：', end='')
    sys.stdout.flush()
    subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=ROOT)
    sys.exit(0)

target_rel = {rel(path) for path, _, _, _ in diffs}
status = subprocess.run(['git', '-c', 'core.quotepath=false', 'status', '--porcelain'],
                         cwd=ROOT, capture_output=True, text=True).stdout
unrelated = [line for line in status.splitlines() if line[3:].split(' -> ')[-1] not in target_rel]
if unrelated:
    print()
    print('错误：工作树存在与本次快照无关的改动，为避免裹挟提交已拒绝执行：', file=sys.stderr)
    for l in unrelated:
        print('  ', l, file=sys.stderr)
    print('请先处理（commit 或用带唯一标记的 `git stash push -u -m`）以上改动后重试，'
          '或用 --space 缩小快照范围令其不与这些文件重叠。', file=sys.stderr)
    sys.exit(1)

print()
for path, old, new, label in diffs:
    show_diff(path, old, new, label)
print()
if os.environ.get('SNAPSHOT_ASSUME_YES') == '1':
    ans = 'yes'
    print(f'（SNAPSHOT_ASSUME_YES=1，视为确认写入以上 {len(diffs)} 个文件）')
else:
    sys.stdout.write(f'确认把以上 {len(diffs)} 个文件的差异写入本地并提交？[yes/N]: ')
    sys.stdout.flush()
    try:
        ans = open('/dev/tty').readline().strip()
    except Exception:
        ans = ''
if ans != 'yes':
    print('已取消，未做任何写入。')
    sys.exit(0)

for path, old, new, label in diffs:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'w', encoding='utf-8') as f:
        f.write(new)
    print('  [已写入]', rel(path), f'({len(new)} 字符)')

subprocess.run(['git', 'add', '--'] + sorted(target_rel), cwd=ROOT, check=True)
staged = subprocess.run(['git', 'diff', '--cached', '--quiet'], cwd=ROOT).returncode != 0
if not staged:
    # 写入内容与已提交版本字节一致（如工作树此前存在与平台一致但未提交的还原类改动）——
    # 无新变化可提交，不视为失败。
    sha = subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=ROOT, capture_output=True, text=True).stdout.strip()
    print(f'已写入 {len(target_rel)} 个文件，但内容与当前 HEAD 一致，无新增改动可提交。当前 SHA：{sha}')
    sys.exit(0)
msg = ("snapshot(prompts): 定期快照\n\n自动快照，时间：" +
       subprocess.run(['date', '+%Y年%m月%d日 %H:%M:%S'], capture_output=True, text=True).stdout.strip())
subprocess.run(['git', 'commit', '-m', msg], cwd=ROOT, check=True)
sha = subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=ROOT, capture_output=True, text=True).stdout.strip()
print(f'快照已提交（仅暂存本次写入的 {len(target_rel)} 个文件）。SHA：{sha}')
PYEOF
