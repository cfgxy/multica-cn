# prompt-sync 三工具条款标记适配（ADR 007 拆单 6 / RUYI-765）

`snapshot.sh`、`restore.sh`、`tools/check_prompts.py` 三个提示词同步工具的
条款渲染段适配版：识别载体业务列中的条款渲染段

```
<!-- clause:id=cls_xxxxxxxx rev=N -->
…渲染正文…
<!-- /clause -->
```

并在快照、回写、立法门闸三条路径上保持其边界与语义。标记契约为
`server/pkg/clausemark/clausemark.go`（RUYI-751 合入）的 Python 转写；
ADR 设计依据见 `docs/adr/007-clause-asset-lifecycle-and-injection.md`。

## 落点说明（待 Leader 裁定）

三个工具的权威部署位置是提示词仓 `cfgxy/ruyi`（仓根 + tools/），本目录是
multica-cn 载体内的 staging 实现——本单唯一可写仓库是 multica-cn，故先在此
交付；向 ruyi 仓的移植路径由 Leader 裁定后执行。

- 来源基线：`cfgxy/ruyi` @ `e447d138930d50b510d16d976f8fe21ae581005a`
  （2026-10-14 取，基线副本在 `tests/baseline/`，逐字节核对一致）。
- 预存缺陷（基线即有，本单按「修 A 发现 B」不上报不入本提交）：
  `restore.sh` 以 `--root <SHA归档目录>` 调门闸，但 `check_prompts.py`
  从不解析 argv——`--root` 被忽略，门闸实际校验的是仓自身工作树而非目标
  SHA 归档内容，回退任意版本时校验对象错位。适配版保持字节忠实未顺手修，
  建议后续独立 Issue 修复。

## 各工具适配面

| 工具 | 适配内容 |
| ---- | ---- |
| `tools/check_prompts.py` | 门闸解析时掩蔽条款渲染段（标记行 + 段内正文原地置空，行号保持），段内大象词/弱表述/结构基线不再误报；损坏标记（孤立关闭、段内新开、缺关闭、空正文、重复 slug、畸形形状）按 error 阻断回写；条款段内的条款名不再进条款登记/结构基线。无标记文件掩蔽为恒等变换（零回归锚点）。 |
| `snapshot.sh` | 备份通道：dry-run 预览与 apply 输出标注「含条款渲染段 N 段」；检测到损坏标记时显式警告但照常入库（保备份优先，回写时由门闸阻断）——快照永不因标记损坏而拒绝备份。 |
| `restore.sh` | 回写通道：门闸照旧前置阻断损坏来源；回读核实新增标记感知——稳定上下文逐字节把关，平台已重渲染（rev 推进/段序变化）时报告「以平台重渲染为准」不误报，渲染段丢失/损坏时告警。写通道纪律不变（RESTORE_ASSUME_YES + 「写入 N」计数核对 + 独立回读核实），未新增绕行路径。 |

## 测试

```bash
bash scripts/prompt-sync/tests/run.sh          # GREEN：16 门闸单测 + 7 场景往返 Feature，全过
bash scripts/prompt-sync/tests/run.sh --red    # RED：同套件对 ruyi 基线复跑，必须失败
```

- `tests/test_check_prompts_markers.py`：门闸单元套件（掩蔽/损坏检出的
  10 个契约用例与 clausemark.Parse 对齐 + 6 个端到端门闸用例）。
- `tests/test_roundtrip.sh`：沙箱往返 Feature（假 multica CLI + 假 ruyi
  布局仓），覆盖快照逐字节入库、回写逐字节回写、平台重渲染不误报且稳定
  上下文逐字节把关、损坏来源被门闸阻断、平台下发损坏标记时快照告警照常备份。
- 零回归取证（2026-10-14）：真 `cfgxy/ruyi` 语料（HEAD `00bc3451`，121 个
  md、0 个含标记）分别以基线/适配门闸运行，输出逐字节一致
  （0 error / 68 warning）。
