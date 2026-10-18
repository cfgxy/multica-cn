#!/usr/bin/env python3
"""prompt 立法门闸（机械校验）。restore.sh apply 前自动执行，FAIL 阻断回写。

依据：SPEC-AGENT 约束分级与反模式 + 2026-09-16 Owner 立法门闸批复。
error = 阻断；warning = 报告不阻断。
"""
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MANIFEST = Path(__file__).resolve().parent / 'prompt-structure.json'

ERRORS, WARNINGS = [], []

def err(msg): ERRORS.append(msg)
def warn(msg): WARNINGS.append(msg)

# 允许存在表格的章节（成员 UUID 映射表为 F1 条款要求）
TABLE_OK_SECTIONS = ('空间身份与成员 UUID',)

# 大象模式：修订痕迹/废弃残留（要求4：文档只体现最终内容）
ELEPHANT = re.compile(
    r'不再|已废弃|已下线|已迁移至|旧版|deprecated|历史上曾|此前为|原为|废弃声明')

# 大象豁免：快照从平台逐字拉回的 Owner 直改内容不适用大象模式（门闸约束本仓立法，不改写平台原文）。
# 锚失效 = error，防白名单腐化（同 per-space-divergence 白名单纪律）。
ELEPHANT_OK = {
    'workspace-ruyi/proj-如意打印.md': ['不再是一次性 Demo'],
}

# 弱表述（SPEC-AGENT E5：纪律条款禁用）
WEAK = re.compile(r'应该尽量|建议避免|原则上|尽量避免|尽量不|可能的话|酌情')

# 内联清单启发式：加粗标签+冒号后长顿号枚举（SPEC-AGENT 反模式）
INLINE = re.compile(r'\*\*[^*\n]{2,20}\*\*[：:][^*\n]{30,}(?:、[^、\n]{2,}){3,}$')

def load_manifest():
    return json.loads(MANIFEST.read_text(encoding='utf-8'))

def check_structure(name, text, manifest):
    """闸：章节顺序与集合不破坏（要求1/3）。manifest 外新增章节 = error。"""
    headers = [m.group(1).strip() for m in re.finditer(r'(?m)^## (.+)$', text)]
    expected = manifest.get(name)
    if expected is None:
        warn(f'{name}: 无结构基线，跳过结构检查')
        return headers
    exp = expected['sections']
    if headers != exp:
        missing = [h for h in exp if h not in headers]
        added = [h for h in headers if h not in exp]
        for h in missing:
            err(f'{name}: 基线章节被删除/改名：「{h}」（改标题须全仓更新引用并重建基线）')
        for h in added:
            err(f'{name}: 未经批复新增章节「{h}」（新条款进既有章节或分册；结构变更须 Owner 批复并重建基线）')
        if not missing and not added:
            common = [h for h in exp if h in headers]
            if [h for h in exp if h in headers] != [h for h in headers if h in exp]:
                err(f'{name}: 章节顺序被改变（要求3：不破坏章节序）')
    return headers

def check_file(path, manifest):
    rel = str(path.relative_to(ROOT))
    text = path.read_text(encoding='utf-8')
    for anchor in ELEPHANT_OK.get(rel, []):
        if anchor not in text:
            err(f'{rel}: 大象豁免锚失效（平台原文已变，复核后更新豁免）：「{anchor}」')
    section = ''
    for i, line in enumerate(text.split('\n'), 1):
        m = re.match(r'^## (.+)$', line)
        if m:
            section = m.group(1).strip()
        if line.strip().startswith('|') and section not in TABLE_OK_SECTIONS:
            err(f'{rel}:{i} Markdown 表格（SPEC-AGENT 反模式，改 bullet 列表）：{line.strip()[:50]}')
        elephant_ok = any(a in line for a in ELEPHANT_OK.get(rel, []))
        for pat, tag, level in ((ELEPHANT, '大象/修订痕迹', 'err'), (WEAK, '弱表述', 'err'), (INLINE, '内联清单', 'warn')):
            if tag == '大象/修订痕迹' and elephant_ok:
                continue
            for mm in pat.finditer(line):
                msg = f'{rel}:{i} {tag}：「{mm.group(0)[:40]}」'
                (err if level == 'err' else warn)(msg)
    check_structure(rel, text, manifest)

def check_dangling_refs(files_text):
    """闸：指向已迁章节的悬挂引用（要求4 的配套）。"""
    known = set()
    for name, text in files_text.items():
        known.update(m.group(1).strip() for m in re.finditer(r'(?m)^## (.+)$', text[0]))
    handbook = set()
    for name, text in files_text.items():
        for h in re.finditer(r'〈([a-z\-]+)〉', text[1] if len(text) > 1 else ''):
            handbook.add(h.group(1))
    for name, (ctx, _) in files_text.items():
        for mm in re.finditer(r'workspace context「([^」]+)」', ctx):
            ref = mm.group(1)
            if ref not in known:
                err(f'{name}: 悬挂引用 workspace context「{ref}」（章节已迁出或不存在）→ 改指 collab-handbook〈分册〉')

def check_cross_dup(files_text):
    """重复立法检测（E2）：同句（≥24字符）出现在 ≥2 个常驻文件 = warning。"""
    seen = {}
    for name, (ctx, _) in files_text.items():
        logical = re.sub(r'workspace-\w+', 'workspace', name)
        for line in ctx.split('\n'):
            line = line.strip().lstrip('- ').lstrip('> ')
            if (len(line) < 24 or line.startswith('#') or line.startswith('[ ]') or line.startswith('**Owner 最高指挥权优先**')
                    or re.search(r'以 workspace context「', line) or re.search(r'→\s*[\u4e00-\u9fff]{0,4}\s*workspace', line)
                    or re.search(r'统一遵守|详见|见 workspace|按 workspace', line)):
                continue
            key = re.sub(r'\s', '', line)[:40]
            seen.setdefault(key, []).append((logical, line[:40]))
    for key, spots in seen.items():
        uniq = {s[0] for s in spots}
        if len(uniq) > 1:
            warn(f'跨文件重复立法（收敛为单点+引用）：{spots[0][1]}… → {sorted(uniq)}')

def load_skill_registry():
    """读取 skills/ 实际形态：skill 名以 SKILL.md frontmatter 的 name 为准，分册以 references/*.md 文件名为准。"""
    registry = {}
    skills_dir = ROOT / 'skills'
    if not skills_dir.is_dir():
        return registry
    for d in sorted(p for p in skills_dir.iterdir() if p.is_dir()):
        sk = d / 'SKILL.md'
        if not sk.exists():
            err(f'skills/{d.name}: 缺少 SKILL.md（skill 不可被运行时发现）')
            continue
        m = re.search(r'(?m)^name:\s*(\S+)\s*$', sk.read_text(encoding='utf-8'))
        if not m:
            err(f'skills/{d.name}/SKILL.md: frontmatter 缺少 name 字段')
            continue
        declared = m.group(1)
        if declared != d.name:
            err(f'skills/{d.name}/SKILL.md: name「{declared}」与目录名「{d.name}」不一致（运行时按目录名发现）')
        refs = {p.stem for p in (d / 'references').glob('*.md')} if (d / 'references').is_dir() else set()
        registry[d.name] = refs
    return registry

def check_skill_refs(files_text, registry):
    """闸：skill 名与分册名一致性（指针指向不存在的 skill 或分册 = 条款不可达）。"""
    for name, (ctx, _) in files_text.items():
        for i, line in enumerate(ctx.split('\n'), 1):
            for mm in re.finditer(r'([A-Za-z][A-Za-z0-9\-]*)〈([a-z0-9\-]+)〉', line):
                skill, ref = mm.group(1), mm.group(2)
                if skill not in registry:
                    err(f'{name}:{i} 引用不存在的 skill「{skill}」（skills/ 实有：{sorted(registry)}）')
                elif ref not in registry[skill]:
                    err(f'{name}:{i} skill「{skill}」无分册〈{ref}〉（实有：{sorted(registry[skill])}）')
            for mm in re.finditer(r'skill[：:]\s*([A-Za-z][A-Za-z0-9\-]*)', line):
                skill = mm.group(1)
                if skill not in registry:
                    err(f'{name}:{i} 手册指针写了不存在的 skill「{skill}」（skills/ 实有：{sorted(registry)}）')

def check_context_parity(files_text):
    """闸：两空间 workspace context 一致性。白名单外差异 = error；白名单锚点未命中差异 = error（防腐化）。
    依据：Owner 2026-09-28 批复——SHAN-373 式单空间立法不得使通用机制条款静默分叉。"""
    import difflib
    a = next((k for k in files_text if 'workspace-shanghui/context' in k), None)
    b = next((k for k in files_text if 'workspace-ruyi/context' in k), None)
    if not a or not b:
        return
    div_file = Path(__file__).resolve().parent / 'per-space-divergence.json'
    anchors = []
    if div_file.exists():
        anchors = [(x['match'], x.get('reason', '')) for x in json.loads(div_file.read_text(encoding='utf-8'))['anchors']]
    la = files_text[a][0].split('\n')
    lb = files_text[b][0].split('\n')
    diff_lines = []
    for tag, i1, i2, j1, j2 in difflib.SequenceMatcher(None, la, lb).get_opcodes():
        if tag != 'equal':
            diff_lines += la[i1:i2] + lb[j1:j2]
    hit = set()
    for line in diff_lines:
        if not any(anchor in line for anchor, _ in anchors):
            err(f'两空间 context 存在未登记差异（per-space-divergence.json 白名单外）：{line.strip()[:60]}')
    for anchor, reason in anchors:
        if not any(anchor in line for line in diff_lines):
            err(f'白名单锚点未命中任何实际差异（已腐化，须随裁定移除）：「{anchor}」')
    if diff_lines:
        hit_count = sum(1 for line in diff_lines if any(anchor in line for anchor, _ in anchors))
        warn(f'两空间 context 差异 {len(diff_lines)} 行，其中白名单内 {hit_count} 行')

CLAUSE_RE = re.compile(r'(?m)^(?:[-*]|\d+\.)\s+\*\*([^*\n]+)\*\*|^\*\*([^*\n]+)\*\*[：:]')

def check_clause_registry(files_text, manifest):
    """闸：条款级防删。文件内加粗条款名须与 manifest clauses 登记一致：文件有而登记无 = 未批复新增；
    登记有而文件无 = 静默删除（856fd29 教训）。立法时同笔提交更新 manifest 即批复留痕。"""
    for rel, (text, _) in files_text.items():
        reg = (manifest.get(rel) or {}).get('clauses')
        if reg is None:
            continue
        current = [m.group(1) or m.group(2) for m in CLAUSE_RE.finditer(text)]
        cur_set, reg_set = set(current), set(reg)
        for c in sorted(reg_set - cur_set):
            err(f'{rel}: 已登记条款被删除/改名：「{c}」（恢复条款或随 Owner 批复更新 manifest）')
        for c in sorted(cur_set - reg_set):
            err(f'{rel}: 未登记的新条款：「{c}」（立法须同笔提交把条款名加入 manifest clauses）')

def main():
    manifest = load_manifest()
    targets = manifest['files']
    # 闸（反向）：受管目录内的 .md 必须全部登记 manifest——新增文件不登记 = 逃避结构/条款/一致性校验
    managed_dirs = ['workspace-shanghui', 'workspace-ruyi', 'ruyi']
    for d in managed_dirs:
        base = ROOT / d
        if not base.is_dir():
            continue
        for p in sorted(base.glob('*.md')):
            rel = f'{d}/{p.name}'
            if rel not in targets:
                err(f'未受管文件: {rel}（workspace-*/ruyi/ 下的 .md 必须登记 manifest 才能过闸；同步更新 sections 与 clauses）')
    files_text = {}
    for rel in targets:
        p = ROOT / rel
        if not p.exists():
            err(f'缺失受管文件: {rel}')
            continue
        files_text[rel] = (p.read_text(encoding='utf-8'), '')
    for rel in list(files_text):
        check_file(ROOT / rel, manifest)
    check_dangling_refs(files_text)
    check_cross_dup(files_text)
    check_skill_refs(files_text, load_skill_registry())
    check_context_parity(files_text)
    check_clause_registry(files_text, manifest)

    for w in WARNINGS:
        print(f'WARN  {w}')
    for e in ERRORS:
        print(f'ERROR {e}')
    print(f'门闸结果: {len(ERRORS)} error / {len(WARNINGS)} warning')
    sys.exit(1 if ERRORS else 0)

if __name__ == '__main__':
    main()
