# RUYI-638 构建交付索引

构建时间：2026-10-18（Asia/Shanghai）
源码仓库：`https://github.com/cfgxy/multica-cn.git`
分支：`agent/agent-f70e39b85abd/ruyi-638-hermes-recovery-01a1244b`
完整来源 SHA：`8e25685f1025e95d15ceaf1e3066e1b83285b781`
依赖解析：`pnpm install --frozen-lockfile`；pnpm-lock SHA256 `972d4dd746565c3229c2bab5a3de9b364b9baf06f9b8313e3f60f28fb90614db`

## APK

- 文件：`ruyi-638-mobile-0.2.0-x86_64.apk`
- 包名：`ai.multica.mobile`
- 版本：`0.2.0`，versionCode `1`
- ABI：`x86_64`，仅模拟器 QA 包，不是全 ABI 发布包
- 字节数：`59092769`
- SHA256：`dfb543a92d2d20ac55801e78bab12290f37e37d1a8c258fba3ec2c80062e157f`
- 签名：本地 release variant；未配置发布 keystore，使用仓库 workflow 的 debug-key fallback。仅用于本单 QA，不可用于商店发布/覆盖正式签名安装。
- 下载：重组附件 `ruyi-638-mobile-apk.00` 至 `.11`，按序拼接为上述文件后复算 SHA256。
- 静态检查：Android `aapt dump badging` 读出包名、版本与 SDK；APK ZIP 中 native library 仅含 `x86_64`。

## Web Release

- 文件：`ruyi-638-web-release.tar.xz`
- 字节数：`21178484`
- SHA256：`2b8cc07bfb4eb366fffae5cb7a4f63c5f7093c6e4e6eb40d82c61cc625eb1da4`
- 下载：重组附件 `ruyi-638-web-release.00` 至 `.04`，按序拼接后复算 SHA256，再解包。
- `RELEASE_MANIFEST.json`：`source_sha` 与本索引完整来源 SHA 一致；tree SHA256 `5e0079b93ea852a5b93ce2e0ece29c21e6241939eb53f85701d3d5ffbe6329b7`；文件 3492 个、109877122 字节。
- 结构预检：`scripts/web-release.sh verify` 逻辑通过，输出 `verify PASS`。该校验脚本取自已有提交 `7d980dbdf4c95b1c000232e49f3ef661ef68447a`；本恢复分支不含此脚本。

## 构建记录

- Web：`STANDALONE=true pnpm --filter @multica/web build`，Next.js 16.2.6；编译、TypeScript、25 个静态页面生成和 standalone trace 均成功。
- APK：production prebuild 后以 OpenJDK 17、`NODE_ENV=production`、`EXPO_NO_BUNDLE_SPLITTING=1` 运行 `./gradlew assembleRelease --no-daemon -PreactNativeArchitectures=x86_64`；Gradle `BUILD SUCCESSFUL`。
- 修复回归：模拟 `Array.prototype.toSorted` 缺失时，旧实现在 `aggregateDailyCost` 抛出 `TypeError: Array.from(...).toSorted is not a function`；统计首屏所有同步聚合路径的新定向回归通过。
- 测试：core Vitest 174 个文件 / 2275 项通过；mobile 与 core TypeScript 检查通过。
- 未执行：未启动模拟器或设备；统计屏 22 项独立 QA 尚未完成。APK 仅做静态元数据/ABI检查，不能视为 QA PASS。
