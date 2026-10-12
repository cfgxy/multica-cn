# multica-slot-base 镜像烘焙流水线（RUYI-676 P1）

把 multica 槽位租约 VM 的底座镜像做成**脚本化、版本化、可重建**的流水线：
Ubuntu Server 24.04（autoinstall 全自动）+ PostgreSQL + node 运行时 + 守卫/清点/
解压装载 guest 脚本 + 轻量调试工具，**不含构建工具链**（无 gcc/make/build-essential/
dkms/linux-headers，`build-seed.test.sh` 负断言钉死）。

## 机制来源（实测依据，非纸面设计）

宿主侧机制逐字对齐 2026-10-06 RUYI-454/RUYI-679 已验证的制备路径
（`guxy-desk` `I:\resource-pool\install\` 下的 mk-cidata/install-vm/finalize-base，
见池仓 `docs/06-guxy-desk-survey.md` §8.3 一）：

- subiquity 从挂载的 FAT32 卷（label `CIDATA`，256MB Fixed VHD）读 autoinstall
  种子，全自动安装后 `shutdown: poweroff`；
- install VM：Gen2、SecureBoot Off、4 vCPU / 4GB 静态内存、Default Switch、
  80GB 动态目标盘、DVD（原版 ISO）为第一启动设备；
- 终态：`Optimize-VHD -Mode Full` 瘦身后置只读；差分租约盘继承该 base 的
  逻辑上限（`New-VHD -Differencing` 不接受 `-SizeBytes`，上限必须覆盖最大模板
  disk_gb——RUYI-679 实测语义）。

与 P0 版本的差异：用户 `slotadmin`（原 rpmadmin，rpm 项目专用）；新增
PostgreSQL/node/jq/unzip/git 等运行时与调试工具；guest 脚本以 base64
late-commands 内嵌到 `/usr/local/lib/multica/`；密钥仍为 acceptance 公钥 +
一次性控制台救援密码（渲染时生成，只落 `$6$` 哈希与本地 600 权限文件）。

## 流程

```text
① build-seed.sh（任意 bash 机器）     渲染 seed/：user-data、meta-data、
                                      seed-manifest.json、.console-password
② 拷贝 seed/ → 宿主机 I:\resource-pool\install\seed\
③ host\mk-cidata.ps1                 CIDATA 种子 VHD（幂等重建）
④ host\install-vm.ps1                ISO sha256 门禁 → 建 VM → 等待自动关机
⑤ host\finalize-base.ps1             Optimize-VHD → 只读 → SHA256 → 镜像 manifest
```

宿主机前置：Hyper-V PowerShell 模块（管理员）；ISO 与 `SHA256SUMS` 已在
`I:\resource-pool\iso\`（versions.env 钉了 24.04.5 live-server 与其 SHA256，
`install-vm.ps1 -IsoSha256` 强校验，不匹配拒绝烘焙）。

```powershell
# ④⑤ 示例（宿主机）
.\host\mk-cidata.ps1 -InstallDir I:\resource-pool\install -SeedDir I:\resource-pool\install\seed
.\host\install-vm.ps1 -InstallDir I:\resource-pool\install `
  -IsoPath I:\resource-pool\iso\ubuntu-24.04.5-live-server-amd64.iso `
  -IsoSha256 97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8
.\host\finalize-base.ps1 -InstallDir I:\resource-pool\install `
  -BaseDir I:\resource-pool\base -ImageVersion 2026.10.11
```

## 版本化与产物

- **镜像名**：`multica-slot-base@<version>.vhdx` + 同名 `.manifest.json`
  （schema v1：versions{ubuntu/iso sha256/node_major/postgres_major}、
  guest_scripts[{name,sha256}]、output{size/sha256/read_only}）。
- **升版 = 新文件**：finalize 遇同名 base 直接 throw——只读 base 永不原地覆盖；
  池侧模板 `tpl-multica-dev/qa` 的 `image_ref` 按版本引用（池仓段交付）。
- guest 脚本改动 → 重渲染 seed → 重烘焙新版本镜像（guest 脚本哈希在 manifest
  可审计）。

## 边界与待办

- guest 脚本（`guest/`）在租约 VM 内的实测属池侧闭环验收（E3/E6 联调），
  本流水线只保证其进入镜像；audit 范围说明见 `slot-pool.sh audit`。
- PS1 三脚本在本机（gti-server）无 PowerShell 运行面，仅静态评审；首次重建
  需宿主机窗口（按 resource-pool-qa skill 的租约流程申请，禁止直连宿主机操作）。
- 磁盘 per-instance 硬配额缺失（差分盘继承 base 上限）为 RUYI-679 已登记
  遗留项，不在本单范围。
