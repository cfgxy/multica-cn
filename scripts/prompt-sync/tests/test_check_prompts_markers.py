#!/usr/bin/env python3
"""立法门闸条款渲染段适配的单元测试（ADR 007 拆单 6 / RUYI-765）。

被测脚本经环境变量 PROMPT_SYNC_GATE 指定，缺省为被适配的
scripts/prompt-sync/tools/check_prompts.py；tests/run.sh 会以
tests/baseline/check_prompts.py 再跑一遍作为 RED 证据（适配前的
脚本必须在本套件的关键用例上失败）。

标记契约的平台权威实现是 multica 仓 server/pkg/clausemark/clausemark.go，
本文件中的正则与错误语义断言逐字对齐该实现（RUYI-751 合并版）。
"""
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

TESTS_DIR = Path(__file__).resolve().parent
ADAPTED_GATE = TESTS_DIR.parent / 'tools' / 'check_prompts.py'
BASELINE_GATE = TESTS_DIR / 'baseline' / 'check_prompts.py'


def load_gate(path=None):
    path = Path(path or os.environ.get('PROMPT_SYNC_GATE', ADAPTED_GATE))
    spec = importlib.util.spec_from_file_location('gate_under_test', path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def gate_path():
    return Path(os.environ.get('PROMPT_SYNC_GATE', ADAPTED_GATE)).resolve()


def run_gate(gate_src, repo_root):
    """把被测门闸装进 fixture 仓 tools/ 下再运行。

    门闸从自身同目录读 prompt-structure.json、以 parent.parent 为 ROOT——
    这就是真实部署形态（restore.sh 在仓根执行 `python3 tools/check_prompts.py`），
    所以必须复制进 fixture 而不是原地运行。
    """
    repo_root = Path(repo_root)
    deployed = repo_root / 'tools' / 'check_prompts.py'
    deployed.parent.mkdir(parents=True, exist_ok=True)
    deployed.write_text(Path(gate_src).read_text(encoding='utf-8'), encoding='utf-8')
    r = subprocess.run([sys.executable, 'tools/check_prompts.py', '--root', str(repo_root)],
                       capture_output=True, text=True, cwd=str(repo_root))
    return r.returncode, r.stdout + r.stderr


def make_repo(files):
    tmp = tempfile.mkdtemp(prefix='gate-fixture-')
    root = Path(tmp)
    for rel, text in files.items():
        p = root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text, encoding='utf-8')
    return root


def seg(slug, rev, body):
    return f'<!-- clause:id={slug} rev={rev} -->\n{body}\n<!-- /clause -->'


MARKER_OK_BODY = (
    '- **发布纪律**：QA PASS 后由 Leader 统一 push，禁止已废弃流程旁路。\n'
    '| 列A | 列B |\n'
    '| ---- | ---- |\n'
    '| 此前为旧版 | 应该尽量避免 |\n'
)


class MarkerParseCase(unittest.TestCase):
    """mask/parse 函数级断言，语义对齐 clausemark.Parse。"""

    def setUp(self):
        self.gate = load_gate()

    def test_no_markers_is_identity(self):
        text = '# 标题\n\n正文 A\n\n## 章节\n内容\n'
        masked, segments, errors = self.gate.mask_clause_segments(text)
        self.assertEqual(masked, text)          # 原样返回（零回归锚点）
        self.assertEqual(segments, [])
        self.assertEqual(errors, [])

    def test_wellformed_segments_masked_with_line_numbers_kept(self):
        text = (
            '## 稳定上下文\n'
            '稳定行 1\n'
            + seg('cls_a1b2c3d4', 3, '条款正文行甲\n不应该出现大象词：已废弃') + '\n'
            '稳定行 2\n'
            + seg('cls_0000ffff', 1, '另一段') + '\n'
        )
        masked, segments, errors = self.gate.mask_clause_segments(text)
        self.assertEqual(errors, [])
        self.assertEqual([s[0] for s in segments], ['cls_a1b2c3d4', 'cls_0000ffff'])
        self.assertEqual([s[1] for s in segments], [3, 1])
        # 稳定上下文原样保留
        self.assertIn('稳定行 1', masked)
        self.assertIn('稳定行 2', masked)
        # 渲染段（含标记行与正文）被屏蔽，且大象词不再可见
        self.assertNotIn('已废弃', masked)
        self.assertNotIn('条款正文行甲', masked)
        # 行数不变：内容检查的行号保持与原文件一致
        self.assertEqual(len(masked.split('\n')), len(text.split('\n')))
        first_marker_line = text.split('\n').index('<!-- clause:id=cls_a1b2c3d4 rev=3 -->')
        self.assertEqual(masked.split('\n')[first_marker_line], '')

    def test_missing_close_is_error(self):
        text = '稳定\n<!-- clause:id=cls_a1b2c3d4 rev=1 -->\n正文无关闭\n'
        masked, segments, errors = self.gate.mask_clause_segments(text)
        self.assertEqual(segments, [])
        self.assertTrue(any('缺少关闭标记' in e for e in errors), errors)

    def test_orphan_close_is_error(self):
        text = '稳定\n<!-- /clause -->\n'
        masked, segments, errors = self.gate.mask_clause_segments(text)
        self.assertTrue(any('孤立' in e for e in errors), errors)

    def test_nested_open_is_error(self):
        text = ('<!-- clause:id=cls_a1b2c3d4 rev=1 -->\n'
                '<!-- clause:id=cls_0000ffff rev=1 -->\n'
                '<!-- /clause -->\n')
        masked, segments, errors = self.gate.mask_clause_segments(text)
        self.assertTrue(any('新的打开标记' in e for e in errors), errors)

    def test_empty_body_is_error(self):
        text = '<!-- clause:id=cls_a1b2c3d4 rev=1 -->\n<!-- /clause -->\n'
        masked, segments, errors = self.gate.mask_clause_segments(text)
        self.assertTrue(any('正文为空' in e for e in errors), errors)

    def test_duplicate_slug_is_error(self):
        text = (seg('cls_a1b2c3d4', 1, '甲') + '\n' + seg('cls_a1b2c3d4', 2, '乙') + '\n')
        masked, segments, errors = self.gate.mask_clause_segments(text)
        self.assertTrue(any('重复出现' in e for e in errors), errors)

    def test_malformed_marker_shape_is_detected(self):
        # 形似标记但不合契约（id 格式 / rev 非数字 / 大写）必须被门闸检出，
        # 不能静默当作正文写回平台。
        for bad in ('<!-- clause:id=cls_short rev=1 -->',
                    '<!-- clause:id=cls_A1B2C3D4 rev=1 -->',
                    '<!-- clause:id=cls_a1b2c3d4 rev=x -->',
                    '<!-- clause:id=foo_a1b2c3d4 rev=1 -->',
                    '<!-- clause:id=cls_a1b2c3d4 -->',
                    '<!-- /clause  -->x'):
            text = f'稳定\n{bad}\n'
            masked, segments, errors = self.gate.mask_clause_segments(text)
            self.assertTrue(errors, f'损坏标记未被检出：{bad}')

    def test_strip_segments_removes_whole_lines_for_parity(self):
        a = '## 相同\n共同行\n' + seg('cls_a1b2c3d4', 1, '空间A专属条款')
        b = '## 相同\n共同行\n' + seg('cls_a1b2c3d4', 2, '空间B另一条')
        sa = self.gate.strip_clause_segments(a)
        sb = self.gate.strip_clause_segments(b)
        self.assertEqual(sa, sb)            # 渲染段差异不计入两空间 parity
        self.assertIn('共同行', sa)

    def test_strip_orphan_markers_tolerant(self):
        # StripMarkers 语义：行首孤立标记行被移除而不报错；行中形似标记按正文保留
        #（对齐 clausemark 宽容层——只有严格合式的标记行才被剥除）。
        text = '<!-- /clause -->\n下一行\n尾巴 <!-- clause:id=cls_a1b2c3d4 rev=1 --> 内联'
        out = self.gate.strip_clause_segments(text)
        self.assertNotIn('<!-- /clause -->', out)
        self.assertIn('下一行', out)
        self.assertIn('内联', out)


class GateEndToEndCase(unittest.TestCase):
    """门闸 E2E：fixture 仓 + 子进程运行，覆盖过闸零误伤 / 损坏检出 / 零回归。"""

    MANIFEST = {
        'files': ['workspace-ruyi/context.md'],
        'workspace-ruyi/context.md': {
            'sections': ['空间身份与成员 UUID', '协作纪律'],
        },
    }

    def make_manifest_repo(self, context_text):
        return make_repo({
            'tools/prompt-structure.json': json.dumps(self.MANIFEST, ensure_ascii=False),
            'workspace-ruyi/context.md': context_text,
        })

    def test_marker_file_passes_gate_zero_error(self):
        # 渲染段内含大象词/弱表述/表格（平台原文合法）——适配后不得误伤。
        ctx = (
            '# workspace context\n\n'
            '## 空间身份与成员 UUID\n'
            '本空间 ID：42f61469-c332-41f3-a2b4-018667473993。\n\n'
            + seg('cls_a1b2c3d4', 3, MARKER_OK_BODY) + '\n\n'
            '## 协作纪律\n'
            '- 回报用中文，先结论后依据。\n'
        )
        root = self.make_manifest_repo(ctx)
        rc, out = run_gate(gate_path(), root)
        self.assertEqual(rc, 0, f'含标记文件被门闸误伤：\n{out}')
        self.assertIn('0 error', out)

    def test_corrupted_marker_detected(self):
        # 人工注入损坏标记（缺关闭标记）必须被检出。
        ctx = (
            '# workspace context\n\n'
            '## 空间身份与成员 UUID\n'
            '本空间 ID：42f61469-c332-41f3-a2b4-018667473993。\n\n'
            '<!-- clause:id=cls_a1b2c3d4 rev=3 -->\n'
            '正文没有关闭标记\n\n'
            '## 协作纪律\n'
            '- 回报用中文，先结论后依据。\n'
        )
        root = self.make_manifest_repo(ctx)
        rc, out = run_gate(gate_path(), root)
        self.assertEqual(rc, 1, f'损坏标记未被检出：\n{out}')
        self.assertIn('缺少关闭标记', out)

    def test_malformed_marker_shape_detected(self):
        ctx = (
            '# workspace context\n\n'
            '## 空间身份与成员 UUID\n'
            '<!-- clause:id=cls_bad rev=1 -->\n'
            '正文\n'
            '<!-- /clause -->\n\n'
            '## 协作纪律\n'
            '- 回报用中文。\n'
        )
        root = self.make_manifest_repo(ctx)
        rc, out = run_gate(gate_path(), root)
        self.assertEqual(rc, 1, f'形似标记的损坏未检出：\n{out}')

    def test_no_marker_regression_pinned(self):
        # 零回归锚点：无标记文件的既有违规照常报，错误集合钉死。
        ctx = (
            '# workspace context\n\n'
            '## 空间身份与成员 UUID\n'
            '本功能已废弃，旧版走老流程。\n\n'
            '## 协作纪律\n'
            '- 发布应该尽量避免直接推送。\n'
        )
        root = self.make_manifest_repo(ctx)
        rc, out = run_gate(gate_path(), root)
        self.assertEqual(rc, 1)
        self.assertIn('大象/修订痕迹', out)
        self.assertIn('弱表述', out)
        self.assertIn('3 error', out)   # 大象词×2（同线各计一次）+ 弱表述×1

    def test_marker_section_headers_do_not_break_structure(self):
        # 渲染段内出现 ## 标题不计入结构基线比对。
        ctx = (
            '# workspace context\n\n'
            '## 空间身份与成员 UUID\n'
            'ID：42f61469-c332-41f3-a2b4-018667473993。\n\n'
            + seg('cls_a1b2c3d4', 1, '## 渲染段里的标题\n平台渲染的正文。') + '\n\n'
            '## 协作纪律\n'
            '- 回报用中文。\n'
        )
        root = self.make_manifest_repo(ctx)
        rc, out = run_gate(gate_path(), root)
        self.assertEqual(rc, 0, f'渲染段标题破坏结构基线判定：\n{out}')

    def test_marker_clauses_do_not_enter_clause_registry(self):
        # 渲染段内加粗条款名不进仓侧条款登记比对（平台资产，非本仓立法）。
        manifest = {
            'files': ['workspace-ruyi/context.md'],
            'workspace-ruyi/context.md': {
                'sections': ['空间身份与成员 UUID', '协作纪律'],
                'clauses': ['仓侧既有条款'],
            },
        }
        ctx = (
            '# workspace context\n\n'
            '## 空间身份与成员 UUID\n'
            '- **仓侧既有条款**：仍在。\n\n'
            + seg('cls_a1b2c3d4', 1, '- **平台渲染条款**：平台资产正文。') + '\n\n'
            '## 协作纪律\n'
            '- 回报用中文。\n'
        )
        root = make_repo({
            'tools/prompt-structure.json': json.dumps(manifest, ensure_ascii=False),
            'workspace-ruyi/context.md': ctx,
        })
        rc, out = run_gate(gate_path(), root)
        self.assertEqual(rc, 0, f'渲染段条款被误登记：\n{out}')


if __name__ == '__main__':
    unittest.main(verbosity=2)
