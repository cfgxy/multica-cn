**结论**：22:18:04 实测两槽位仍全占用，维持等待；方案调整认可，既有分派继续有效，无须再确认。

**槽位核验**：`dev1` = RUYI-668（dev 相位、租约 registered、环境 stopped）、`dev2` = RUYI-664（同），与 [顾小鱼 22:00 回报](mention://comment/01a12625-5dee-7ee3-9118-cd26447d59c7) 一致，占用面未再变化；两租约均属在飞单，维持「等待、禁止接管」。

**新事实（影响解锁路径）**：dev1 持有单 RUYI-668 的批次一 run 已于 22:11 最终失败（平台通告：worktree 对 `server/internal/service/builtin_skills/multica-working-on-issues/SKILL.md` 的本地未合并改动导致分支交付被拒，失败 worktree 已保留；见 [RUYI-656 故障通告（22:11）](mention://comment/01a12627-bb79-7c2a-8d29-79ea6607218c)）。恢复已在途（22:18 任务队列实测：RUYI-668 有运行中承接任务、RUYI-656 故障协调已排队由我承接），本单不重复触发；dev1 释放时点随 RUYI-668 恢复与批次进度走。

**方案调整认可**（登记备查）：原分派卡所引 `qa-tools/desktop-e2e.mjs` 确认不在 main，实机定性改现场 CDP 驱动 Electron 实跑（模拟「选择快速回复」点击 + `capturePage` 截图取证）；基线以执行时实时 fetch 的最新 `origin/main` tip 为准（先快进再跑）；依托 guxy 会话 GNOME X 实机渲染。均在既定验收目标内，按此执行。

**下一步**：解锁条件 = [RUYI-668](mention://issue/01a12613-8500-7916-b8c3-87a71b05d33f) 或 [RUYI-664](mention://issue/01a125ef-8a8e-7bf0-99f5-c4169f8747b7) 任一 `lock-release`；本单任一入口（含 RUYI-668 恢复回报到达时）先实时 `make list`，有空闲即按既有分派直接开工（claim → up → install → 实机定性 → 阶段回报）。等待期间不重复触发本单 run。

**绩效**（usage/runs @ 22:18 取数）：顾小鱼——本单 2 轮 run 合计输出约 6.2 万 tok，两轮均一次到位（代码层定性、发布通道缺口归因、槽位外预检全部完成，`BLOCKED` 判定克制不虚报），点名表扬，A 档；蔡小星——3 轮处置均实测留证、未重复触发成员 run，自评 A；本单暂无返工与无效探针记录。
