#!/usr/bin/env bash
# 多空间提示词仓库 回写工具（本仓库 → Multica 平台）
#
# 将当前 Git 工作树或指定 SHA 的内容写回平台。默认 dry-run 只显示差异；
# 加 --apply 且逐项确认（yes）后才真正写入，写入后回读核实。
#
# 覆盖对象（新布局）：
#   workspace-<空间>/context.md   → 空间上下文
#   workspace-<空间>/proj-<名>.md → 项目指令（按文件名匹配项目标题）
#   ruyi/_squad-RUYI.md           → 小队指令
#   ruyi/<成员名>.md              → 成员提示词
#
# 空间绑定（用户只面向空间操作，profile 由空间自动解析，禁止手工拼）：
#   workspace-<空间>/.workspace-id   该空间完整 36 位 UUID（目录归属的真源）
#   workspace-<空间>/.profile        所属 multica 实例的 CLI profile 名；无此文件 = 默认实例
#
# 目标空间（优先级从高到低）：
#   --space <目录短名|完整UUID> > MULTICA_SPACE > MULTICA_WORKSPACE_ID > 仓库根 .workspace-id
# 一切平台调用显式传 --profile（按空间绑定解析）与 --workspace-id，永不依赖 CLI 默认值。
#
# 硬性条款：iData 空间上下文与项目提示词均不进 GitHub 仓库，只在 devstation 节点本地维护
# （项目：2026-09-14 Owner 批复；空间上下文：2026-09-24 Owner 指令，本地跟踪仓
# ~/multica-idata-local-prompts）。本脚本对 workspace-idata 拒绝推送 context.md 与
# proj-*.md，目录内出现即报错退出；通用 ruyi/ 层照常回写。
#
# 用法：
#   ./restore.sh                          # 预览当前工作树 → 平台
#   ./restore.sh --apply                  # 逐项确认后写入
#   ./restore.sh <SHA>                    # 预览指定历史版本 → 平台（任意版本回退）
#   ./restore.sh <SHA> --apply            # 确认后写入指定历史版本
#   ./restore.sh --target 蔡小星          # 只处理单个对象（文件名去 .md）
#   ./restore.sh --space idata --apply    # 显式指定空间并写入
#
# 回退任意版本 = 先 `git checkout <SHA> -- <路径>` 或直接用 <SHA> 参数，再本工具写回。

set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

SEL=""; if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  sed -n '2,37p' "${BASH_SOURCE[0]}" | sed 's/^# \?//'; exit 0
fi
SHA=""; TARGET=""; APPLY=0
if [[ "${1:-}" != "" && "${1:-}" != --* ]]; then SHA="$1"; shift; fi
while [[ $# -gt 0 ]]; do
  case "$1" in
    --space) SEL="$2"; shift 2;;
    --target) TARGET="$2"; shift 2;;
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

if [ -n "$SHA" ] && ! git rev-parse --verify "$SHA" >/dev/null 2>&1; then
  echo "错误：SHA '$SHA' 不存在。可用版本：git log --oneline" >&2; exit 1
fi

SRC_LABEL="${SHA:+Git 版本 $SHA}${SHA:-当前 Git 工作树}"
echo "写入来源：$SRC_LABEL"
echo "目标空间：$WS_DIR（$WID @ ${SERVER_URL:-实例未知}，profile=${PROFILE:-默认}）"

# ── 硬性条款：iData 空间上下文与项目提示词不进 GitHub 仓库 ──
# 本空间拒绝推送 context.md 与任何 proj-*.md；目录内出现即视为违规并退出。
# iData 空间上下文/项目指令的维护通道是 devstation 本地跟踪仓 + multica CLI 直写平台。
NOREPO_PROJ=0
if [ "$WID" = "6c337a4d-0c82-4c1c-a46c-630258b2ec34" ] || [ "$(basename "$WS_DIR")" = "workspace-idata" ]; then
  NOREPO_PROJ=1
  if ls "$WS_DIR"/proj-*.md >/dev/null 2>&1; then
    echo "错误：硬性条款——iData 项目提示词只在 devstation 节点本地维护，不进 GitHub 仓库。" >&2
    echo "以下文件不得存在于 $WS_DIR，请移出或删除后重试：" >&2
    ls "$WS_DIR"/proj-*.md >&2
    exit 1
  fi
  if [ -f "$WS_DIR/context.md" ]; then
    echo "错误：硬性条款（2026-09-24 Owner 指令）——iData 空间上下文不进 GitHub 仓库、不经本仓回写。" >&2
    echo "维护位置是 devstation 本地跟踪仓 ~/multica-idata-local-prompts（multica CLI 直写平台）；违规文件：$WS_DIR/context.md。" >&2
    exit 1
  fi
fi

export WID ROOT WS_DIR PROFILE NOREPO_PROJ SHA TARGET APPLY SRC_LABEL

# 立法门闸（2026-09-16 Owner 立法）：回写前机械校验，FAIL 阻断。
# 校验对象是回写来源（--restore <SHA> 时校验该历史版本，防止从旧版本回写复活已废弃内容）。
if [ "${SKIP_GATE:-}" != "1" ]; then
  GATE_TARGET="${SHA:-HEAD}"
  echo "── 立法门闸：校验 $GATE_TARGET 的 prompt 状态 ──"
  GATE_DIR=$(mktemp -d)
  mapfile -d '' GATE_PATHS < <(git ls-tree -r --name-only -z "$GATE_TARGET" | grep -zE '^(workspace-|ruyi/)')
  git archive "$GATE_TARGET" "${GATE_PATHS[@]}" | tar -x -C "$GATE_DIR"
  python3 tools/check_prompts.py --root "$GATE_DIR"
  GATE_RC=$?
  rm -rf "$GATE_DIR"
  if [ $GATE_RC -ne 0 ]; then
    echo "✗ 立法门闸 FAIL：回写被阻断。修复上列 error，或 Owner 明示放行时 SKIP_GATE=1 重跑。"
    exit 1
  fi
  echo "── 门闸 PASS ──"
fi

python3 - <<'PYEOF'
import json, os, subprocess, sys, difflib

ROOT, WS_DIR, WID = os.environ['ROOT'], os.environ['WS_DIR'], os.environ['WID']
SHA, TARGET, APPLY = os.environ['SHA'], os.environ['TARGET'], os.environ['APPLY']
SRC = os.environ['SRC_LABEL']
PROFILE = os.environ.get('PROFILE', '')
MC = ['multica'] + (['--profile', PROFILE] if PROFILE else [])

def run(*args, input=None):
    r = subprocess.run(MC + list(args), capture_output=True, text=True, input=input)
    return r

def jrun(*args):
    r = run(*args, '--workspace-id', WID, '--output', 'json')
    if r.returncode != 0 or not r.stdout.strip():
        return None
    try: return json.loads(r.stdout)
    except Exception: return None

def read_source(rel):
    """rel 是相对仓库根的路径；从工作树或指定 SHA 读取。"""
    if SHA:
        r = subprocess.run(['git', 'show', f'{SHA}:{rel}'], capture_output=True, text=True)
        if r.returncode != 0: return None
        return r.stdout
    p = os.path.join(ROOT, rel)
    if not os.path.exists(p): return None
    return open(p, encoding='utf-8').read()

def norm(s): return (s or '').rstrip('\n')

def show_diff(name, cur, new):
    diff = list(difflib.unified_diff(norm(cur).splitlines(), norm(new).splitlines(),
                                     '平台当前', f'写入来源 {SRC}', lineterm='', n=1))
    for l in diff[:40]: print(l)
    if len(diff) > 40: print(f'... 共 {len(diff)} 行差异，此处截断。')

def confirm(name):
    if APPLY != '1':
        print(f'（dry-run，未写入：{name}）'); return False
    # 非交互环境（无 tty 的自动化执行）用 RESTORE_ASSUME_YES=1 显式放行；
    # 交互路径仍逐项人工确认。
    if os.environ.get('RESTORE_ASSUME_YES') == '1':
        return True
    sys.stdout.write(f'确认把 {name} 写入平台？ [yes/N]: '); sys.stdout.flush()
    try:
        ans = open('/dev/tty').readline().strip()
    except Exception:
        ans = ''
    return ans == 'yes'

def verify(name, got, want):
    if norm(got) == norm(want): print(f'已写入并核实一致：{name}（{len(want)} 字符）')
    else: print(f'警告：{name} 写入后回读不一致（平台 {len(got or "")} / 期望 {len(want)}），请人工核查！')

# 收集任务清单：(对象名, kind, 平台 id 或 路径, 期望内容)
tasks = []
d = os.path.basename(WS_DIR)
rel_ctx = os.path.join(d, 'context.md')
if os.path.isfile(os.path.join(ROOT, rel_ctx)):
    c = read_source(rel_ctx)
    if c is not None and (TARGET in ('', 'context', d)):
        tasks.append((f'{d}/context.md', 'workspace', None, c))
if os.path.isdir(os.path.join(ROOT, d)):
    for f in sorted(os.listdir(os.path.join(ROOT, d))):
        if f.startswith('proj-') and f.endswith('.md'):
            if os.environ.get('NOREPO_PROJ') == '1':
                print(f'[硬性条款] 跳过 {f}：iData 项目提示词不进 GitHub 仓库，只在 devstation 本地维护')
                continue
            name = f[5:-3]
            if TARGET and TARGET not in (name, f[:-3], d): continue
            c = read_source(os.path.join(d, f))
            if c is not None: tasks.append((f'{d}/{f}', 'proj', name, c))

if not TARGET or TARGET in ('_squad-RUYI', 'squad'):
    c = read_source('ruyi/_squad-RUYI.md')
    if c is not None: tasks.append(('ruyi/_squad-RUYI.md', 'squad', 'RUYI', c))
with open(os.path.join(ROOT, 'tools', 'persona-roster.json'), encoding='utf-8') as _rf:
    _roster = json.load(_rf)
_managed = _roster['repo_managed']

def roster_admit(name, rel):
    if name not in _managed:
        print(f'[跳过] {rel}：persona-roster 未登记（先更新 tools/persona-roster.json）'); return False
    sps = _managed[name]
    if sps and WID not in sps:
        print(f'[跳过] {rel}：roster 适用空间不含本空间'); return False
    return True

if not TARGET:
    ruyi = os.path.join(ROOT, 'ruyi')
    if os.path.isdir(ruyi):
        for f in sorted(os.listdir(ruyi)):
            if f.endswith('.md') and f != '_squad-RUYI.md':
                if not roster_admit(f[:-3], f'ruyi/{f}'): continue
                c = read_source(os.path.join('ruyi', f))
                if c is not None: tasks.append((f'ruyi/{f}', 'agent', f[:-3], c))
elif TARGET not in ('context', 'squad') and os.path.exists(os.path.join(ROOT, 'ruyi', TARGET + '.md')):
    if roster_admit(TARGET, f'ruyi/{TARGET}.md'):
        c = read_source(os.path.join('ruyi', TARGET + '.md'))
        if c is not None: tasks.append((f'ruyi/{TARGET}.md', 'agent', TARGET, c))

if not tasks:
    print('没有匹配的对象。'); sys.exit(0)

changed = skipped = 0
for name, kind, ident, want in tasks:
    base = os.path.basename(name)
    if kind == 'workspace':
        cur = (jrun('workspace', 'get') or {}).get('context') or ''
        if norm(cur) == norm(want): print('[一致]', name); continue
        show_diff(name, cur, want)
        if not confirm(name): skipped += 1; continue
        r = run('workspace', 'update', '--workspace-id', WID, '--context-stdin', input=want)
        back = (jrun('workspace', 'get') or {}).get('context') or ''
        verify(name, back, want); changed += 1
    elif kind == 'proj':
        d = jrun('project', 'list')
        rows = d if isinstance(d, list) else (d or {}).get('projects') or []
        pid = next((p['id'] for p in rows if (p.get('title') or '').strip() == ident), None)
        if not pid:
            pid_n = next((p['id'] for p in rows if (p.get('title') or '').replace(' ', '') == ident.replace(' ', '')), None)
            pid = pid_n
        if not pid:
            print(f'[跳过] {name}：平台无标题为「{ident}」的项目'); skipped += 1; continue
        req_cur = jrun('project', 'get', pid) or {}
        cur = req_cur.get('instructions') or ''
        if norm(cur) == norm(want): print('[一致]', name); continue
        show_diff(name, cur, want)
        if not confirm(name): skipped += 1; continue
        import urllib.request
        # profile 感知：跨实例写项目必须读对应实例的 config（server_url/token 随 profile 走）
        cfg_path = (os.path.expanduser(f'~/.multica/profiles/{PROFILE}/config.json') if PROFILE
                    else os.path.expanduser('~/.multica/config.json'))
        cfg = json.load(open(cfg_path))
        _srv, _tok = cfg['server_url'], cfg['token']
        req = urllib.request.Request(f'{_srv}/api/projects/{pid}?workspace_id={WID}',
            data=json.dumps({'instructions': want}).encode(), method='PUT',
            headers={'Authorization': f'Bearer {_tok}', 'Content-Type': 'application/json'})
        d = json.loads(urllib.request.urlopen(req).read())
        verify(name, d.get('instructions') or '', want); changed += 1
    elif kind == 'squad':
        d = jrun('squad', 'list')
        rows = d if isinstance(d, list) else (d or {}).get('squads') or []
        s = next((x for x in rows if x.get('name') == ident), None)
        if not s: print(f'[跳过] {name}：平台无此小队'); skipped += 1; continue
        cur = (jrun('squad', 'get', s['id']) or {}).get('instructions') or ''
        if norm(cur) == norm(want): print('[一致]', name); continue
        show_diff(name, cur, want)
        if not confirm(name): skipped += 1; continue
        r = run('squad', 'update', s['id'], '--workspace-id', WID, '--instructions', want, '--output', 'json')
        back = (jrun('squad', 'get', s['id']) or {}).get('instructions') or ''
        verify(name, back, want); changed += 1
    elif kind == 'agent':
        d = jrun('agent', 'list')
        rows = d if isinstance(d, list) else (d or {}).get('agents') or []
        a = next((x for x in rows if x.get('name') == ident and not x.get('archived_at')), None)
        if not a: print(f'[跳过] {name}：平台无此成员'); skipped += 1; continue
        cur = (jrun('agent', 'get', a['id']) or {}).get('instructions') or ''
        if norm(cur) == norm(want): print('[一致]', name); continue
        show_diff(name, cur, want)
        if not confirm(name): skipped += 1; continue
        r = run('agent', 'update', a['id'], '--workspace-id', WID, '--instructions', want, '--output', 'json')
        back = (jrun('agent', 'get', a['id']) or {}).get('instructions') or ''
        verify(name, back, want); changed += 1

print()
print(f'完成：写入 {changed}，跳过/未匹配 {skipped}')
if APPLY != '1': print('以上为预览（dry-run）。确认无误后加 --apply 执行。')
PYEOF
