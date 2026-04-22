# Desktop 下载体系重设计 — 执行计划

**日期**：2026-04-22
**作者**：Naiyuan
**状态**：方案定稿，分步执行中

---

## 一、为什么要做

### 1.1 现状的核心矛盾

Multica 本质是"**本地 runtime + 云端协作**"的产品。Desktop app 内置 bundled daemon，登录即用；web 是预览/入口，不是等价平台。但**当前代码和文案把 Desktop 和 Web 当作等价路径**，结果：

1. **登录到 Step 3 之间，零 Desktop 推广**。用户建完 workspace 才被告知"其实你得装 app"，此时沉没成本已高
2. **Step 3 分流屏三张卡（Desktop / CLI / Cloud）视觉和文案伪对称**，用户感知不到 Desktop 是正解
3. **`isMac` 过时门**：Windows/Linux 桌面包已齐（v0.2.13），代码却把非 Mac 用户推去 CLI
4. **所有 Download 入口（landing / Step 3 / footer）都指向 GitHub releases 页面**——30+ assets 的列表，对非技术用户是灾难
5. **Welcome 屏 "Takes about 3 minutes" 对 web 用户是谎**——不含下载/安装/换端时间

### 1.2 Cursor 对标带来的确认

Cursor 的 `/download` 页面（<https://cursor.com/cn/download>）模式：
- **Client-side auto-detect**：用 `navigator.userAgentData.getHighEntropyValues(['architecture'])`，精确到 arch；fallback 到 UA 字符串
- **SSR 发全量 HTML**（所有 OS 内容都在），hydration 后 JS 挑对应平台作主 CTA
- **三个并列 surface**：Desktop / Terminal / Web——不是"三选一"，是**三个场景**
- 有 `useDownloadTracking` hook，埋点下载事件

这验证了"桌面主推 + 其他平台可见 + CLI 作为独立场景"的正确性。

---

## 二、核心洞察（从代码扒出来的）

### 2.1 两端用户是两种心态，不是两种路径

| 维度 | Desktop 用户 | Web 用户 |
|---|---|---|
| 入口 | 主动下 .app，越过安装门槛 | 浏览器打开，零投入 |
| 心态 | **投入者**："我认真对待 Multica" | **探索者**："先试试" |
| 对本地安装的态度 | 已接受 | **主动拒绝过**（选 web 就是为了不装） |
| Step 3 的本质 | 确认屏（daemon 已在跑） | 决策屏（产品真相首次披露） |

这解释了为什么 Step 3 在 web 上是漏斗流失点——**那是"你以为是 web 产品 / 实际是本地产品"的期望违约时刻**。

### 2.2 CLI 不是 Desktop 的低配版，是另一个场景

Desktop 和 CLI 跑的是**同一个 Go 二进制**（`daemon-manager.ts` spawn 的 bundled CLI）。区别仅在 **setup moment**：

| 维度 | Desktop | CLI |
|---|---|---|
| 安装 | 双击 .dmg | `curl \| bash` |
| 启动 | `daemonAPI.autoStart()` 登录后自动 | `multica setup` 手动 |
| 运行后能力 | 完全等价 | 完全等价 |
| **真正适合的场景** | 个人机器、交互使用 | **服务器、远程 dev box、on-prem、自动化、CI** |

CLI 的合法性不来自"Desktop 不可得时的替代"，来自它**真的有 Desktop 永远覆盖不了的场景**：

- Self-host / on-prem：daemon 跑在自有服务器
- 远程 dev box：SSH 到 Linux 机器，在那里跑 daemon
- CI/CD：headless 环境里调度 agent
- 多机部署：一个人，多台 runtime

**文案必须攻击 setup moment 不对称，而不是比较能力**——因为能力是一样的。

### 2.3 当前代码和资产状态

**已有的完整跨平台包（v0.2.13）**：

| 平台 | 包 | 大小 |
|---|---|---|
| macOS arm64 | `.dmg` / `.zip` | 184MB |
| macOS Intel (x64) | ❌ 无（`electron-builder.yml` 只 target arm64） | — |
| Windows x64 | `.exe` (NSIS) | 140MB |
| Windows arm64 | `.exe` | 430MB |
| Linux | `.AppImage` / `.deb` / `.rpm`（amd64 + arm64） | 113–189MB |

**代码当前状态**：

- Desktop 端 Step 3 里根本没有 CLI 这张卡（`StepRuntimeConnect.EmptyView` 只有 Skip + Cloud waitlist）——产品团队自己的立场是"bundled daemon 是 Multica 的本分"
- Web 端 Step 3（`StepPlatformFork`）把 CLI 作为一等平级卡，`isMac` 门 disabled 非 Mac 的 Desktop CTA
- 所有 Download 链指向 `https://github.com/multica-ai/multica/releases/latest`

---

## 三、定位策略（文案骨架）

每个 surface 用同一套"场景导向"句子，不比较能力：

| 形态 | 一句话定位 | 适合谁 |
|---|---|---|
| **Desktop** | "Your personal machine. Double-click, agents run locally." | 95% 新用户 |
| **CLI** | "Servers, remote boxes, on-prem, automation. No GUI required." | 开发者 / 运维 / 自建 |
| **Cloud**（waitlist） | "No local install — we host the runtime for you." | 评估 / 不想本地跑 |

**Welcome 屏在 web 分支追加一条引导**：诚实告诉用户"Better on desktop"，给一个 Download 按钮 + "Continue on web" 次选。

---

## 四、已做的决定

| 决策 | 选择 | 理由 |
|---|---|---|
| 语言 | **中英双语**，跟 landing 的 `en.ts` / `zh.ts` 一致 | 保持全站 i18n 体系 |
| Intel Mac | **暂不支持**，`/download` 页诚实标注 | 2026 年 Intel Mac 已是 4+ 年前老机器；包体积翻倍影响所有人；CLI 对 Intel 用户是合理路径 |
| 版本号获取 | **Next.js API route 代理 GitHub API**（`/api/latest-version`），Vercel ISR 5 分钟 cache | `latest.yml` 无 CORS；build-time 注入需每次重部署；GitHub API 未认证 60/hr 对 5min cache 绰绰有余 |
| 部署 | Vercel（已确认） | ISR 原生支持，`export const revalidate = 300` 一行解决 |
| Desktop 链接 URL | 客户端按 detect 结果拼 GitHub asset 直链 | 无需后端端点；带版本号的 URL 从 `/api/latest-version` 返回的 assets 里取 |
| Auto-detect 方式 | `navigator.userAgentData.getHighEntropyValues(['platform','architecture'])` + UA 字符串 fallback | 抄 Cursor 模式；Safari 无此 API，macOS 下无法区分 Intel/arm——默认推 arm64 + 诚实文案 |
| CLI 在 onboarding 的定位 | 保留 Step 3 第二张卡，但**文案重写为服务器/远程场景**，不再假装是 Desktop 的轻量版 | CLI 场景真实存在，但大多数 onboarding 用户不在这个场景里 |
| 开发顺序 | **Step 1 文案 → Step 2 `/download` → Step 3 Onboarding → Step 4 Login + Landing** | Step 1 确立真相源，后续 UI 改动有唯一文案来源，可并行 |

---

## 五、执行步骤

### Step 1 · 文案与定位对齐（不写代码）

**做什么**：

- 建 `docs/download-positioning.md`（本文档的姊妹文档，专门放文案）
- 三个 surface 的定位句（中英）
- 盘点所有触点的**当前文案 vs 新文案**：
  - Landing hero（`landing-hero.tsx`）
  - Landing nav + footer 链接（`landing/i18n/en.ts`、`zh.ts`）
  - Login page（`packages/views/auth/login-page.tsx`）——新增 Desktop CTA
  - Welcome step（`step-welcome.tsx`）——web 分支新增 Desktop CTA
  - Step 3 三张卡（`step-platform-fork.tsx`）
  - `/download` 页面（全新）
  - `CliInstallInstructions`（`cli-install-instructions.tsx`）
- 每条文案带中英双语对照

**目标**：后续所有 UI 改动有唯一文案真相源，不临时发明

**产出**：1 个 markdown doc
**工期**：0.5 天
**产物文件**：`docs/download-positioning.md`

### Step 2 · `/download` 页面

**做什么**：

- **路由**：`apps/web/app/(landing)/download/page.tsx`（放 landing group 共享 layout）
- **SSR**：全量渲染 Desktop / CLI / Cloud 三块，所有平台包都在 HTML 里
- **API route**：`apps/web/app/api/latest-version/route.ts`
  - 调 `https://api.github.com/repos/multica-ai/multica/releases/latest`
  - 解析 assets，按文件名模式抽出每个平台的 URL
  - 返回 `{ version, assets: { macArm64, winX64, winArm64, linuxDeb, linuxRpm, linuxAppImage, ... } }`
  - Vercel ISR：`export const revalidate = 300`
- **Client detect**：`packages/views/utils/os-detect.ts`（新建）
  - 优先用 `navigator.userAgentData.getHighEntropyValues(['platform','architecture'])`
  - Fallback 到 `navigator.userAgent` + `navigator.platform`
  - 返回 `{ os: 'mac'|'windows'|'linux'|'unknown', arch: 'arm64'|'x64'|'unknown' }`
- **UI 行为**：
  - 顶部大 CTA：按 detect 结果拼好的 Desktop 下载按钮（macOS arm64 / Windows x64 / Linux AppImage 作主推）
  - 检测到 Intel Mac（Chromium）→ 主 CTA 变成"Apple Silicon required — use CLI"，CLI 区块置顶
  - 检测到 Safari on macOS → 默认推 arm64 + 小字提示"On Intel Mac? Use CLI"
  - 全平台直链列表（arch 清晰标注）
  - CLI 区块：`curl | bash` + 场景说明
  - Cloud 区块：复用 `CloudWaitlistExpand`
- **i18n**：`apps/web/features/landing/i18n/en.ts` / `zh.ts` 新增 `download` 命名空间

**目标**：全站下载总入口，版本自动更新，用户下到对的包

**工期**：1-2 天
**产物文件**：
- `apps/web/app/(landing)/download/page.tsx`
- `apps/web/app/api/latest-version/route.ts`
- `packages/views/utils/os-detect.ts`（或 `packages/core/platform/os-detect.ts`）
- `apps/web/features/landing/i18n/en.ts` + `zh.ts` 新增 keys
- `apps/web/features/landing/components/download-page.tsx`（主组件，landing 风格）

### Step 3 · Onboarding 修缮

**做什么**：

- **Welcome 屏 web 分支**：
  - `OnboardingFlow` 里派生 `isWeb = !!runtimeInstructions`，传给 `StepWelcome`
  - `StepWelcome` 在 `isWeb` 时，CTA 区域追加一行 "Better on desktop — bundled daemon, zero setup" + **Download 按钮**（指 `/download`）+ "Continue on web" 次选
  - "Takes about 3 minutes" 文案按平台差异化
- **Step 3 分流屏**：
  - 拆掉 `isMac` 门（`step-platform-fork.tsx:244-268`）
  - Desktop 卡对所有平台 active，按 detect 显示对应平台文案
  - Non-Mac 兜底卡改成 Cloud waitlist 强化，不再假装推 CLI
  - 三张卡的文案按 Step 1 确定的定位句重写
- **CLI dialog**：
  - `CliInstallInstructions` 加一行场景说明："Also great for servers and remote dev boxes."
  - `multica setup` 命令旁边保留现状
- **"Downloading"后态**：
  - Desktop 卡点击后的 downloaded 态文案改得更明确（"Check your Downloads folder. Open the .dmg to install."）

**目标**：Welcome 不再骗 web 用户；Step 3 三张卡场景清晰；Windows/Linux 用户不再被推 CLI

**工期**：0.5 天
**产物文件**：
- `packages/views/onboarding/onboarding-flow.tsx`
- `packages/views/onboarding/steps/step-welcome.tsx`
- `packages/views/onboarding/steps/step-platform-fork.tsx`
- `packages/views/onboarding/steps/cli-install-instructions.tsx`

### Step 4 · 上游漏斗（Login + Landing）

**做什么**：

- **Login page**：
  - `packages/views/auth/login-page.tsx` 的 `LoginPageProps` 加 `extra?: ReactNode` prop
  - Google 按钮下方低调一行 "Prefer the desktop app? **Download →**"
  - Desktop 调用方（`apps/desktop/src/renderer/src/pages/login.tsx`）**不传** extra → 不显示
  - Web 调用方（`apps/web/app/(auth)/login/page.tsx`）**传** extra → 显示
- **Landing hero**：
  - `landing-hero.tsx:44-65` 的 Download 按钮从 `heroButtonClassName("ghost")` 升级为 `heroButtonClassName("solid")`（或至少主次分明的 outline）
  - href 从 `https://github.com/multica-ai/multica/releases/latest` 改为 `/download`
- **Landing nav + footer**：
  - `landing/i18n/en.ts:230` / `zh.ts:230` 的 Desktop 链接统一改为 `/download`

**目标**：用户最轻投入时刻就看到 Desktop；Step 3 之前已有两次 Desktop touch

**工期**：2 小时
**产物文件**：
- `packages/views/auth/login-page.tsx`
- `apps/web/app/(auth)/login/page.tsx`
- `apps/desktop/src/renderer/src/pages/login.tsx`（确认不传 extra 即可）
- `apps/web/features/landing/components/landing-hero.tsx`
- `apps/web/features/landing/i18n/en.ts` + `zh.ts`

---

## 六、不做的事（明确范围）

- **后端 `/api/download?os=X&arch=Y` 302 端点**：方案 A 已够用，后端不动
- **下载埋点/数据分析**：本次不做，Cursor 有但我们暂缓
- **下载后 "waiting on desktop" 屏**：让 handoff 更丝滑的想法，留到数据出现再决定
- **Intel Mac universal build**：暂不补，`/download` 诚实标注"暂不支持"
- **CLI 文档页 / 自托管文档**：Step 3 CLI 卡副文案引向 docs，docs 本身不在本次范围
- **/download 页的 "system requirements" 区块**：不做详细 minimum specs，保持简洁

---

## 七、技术细节速查

### 7.1 OS + Arch Detection

```typescript
// 推荐实现骨架
export async function detectOS(): Promise<{ os: OSName; arch: Arch }> {
  // 优先用 userAgentData（Chromium）
  if (navigator.userAgentData?.getHighEntropyValues) {
    try {
      const data = await navigator.userAgentData.getHighEntropyValues([
        "platform",
        "architecture",
      ]);
      // data.platform: "macOS" | "Windows" | "Linux"
      // data.architecture: "x86" | "arm"
      return normalizePlatform(data);
    } catch {
      // fall through
    }
  }
  // Fallback: UA 字符串 + navigator.platform
  const ua = navigator.userAgent;
  const platform = navigator.platform || "";
  // ... 按 "Mac" / "Windows" / "Linux" 分支
}
```

**已知限制**：Safari on macOS 无法区分 Intel/arm64（Apple 故意不暴露）。默认推 arm64 + 诚实文案。

### 7.2 `/api/latest-version` Response Shape

```typescript
{
  version: "v0.2.13",
  publishedAt: "2026-04-21T13:13:52Z",
  assets: {
    macArm64Dmg: "https://github.com/.../multica-desktop-0.2.13-mac-arm64.dmg",
    macArm64Zip: "https://github.com/.../multica-desktop-0.2.13-mac-arm64.zip",
    winX64Exe: "https://github.com/.../multica-desktop-0.2.13-windows-x64.exe",
    winArm64Exe: "https://github.com/.../multica-desktop-0.2.13-windows-arm64.exe",
    linuxAmd64AppImage: "https://github.com/.../multica-desktop-0.2.13-linux-x86_64.AppImage",
    linuxAmd64Deb: "https://github.com/.../multica-desktop-0.2.13-linux-amd64.deb",
    linuxAmd64Rpm: "https://github.com/.../multica-desktop-0.2.13-linux-x86_64.rpm",
    linuxArm64AppImage: "...",
    linuxArm64Deb: "...",
    linuxArm64Rpm: "...",
  }
}
```

Asset 文件名模式由 `electron-builder.yml` 定义：`multica-desktop-${version}-${platform}-${arch}.${ext}`。解析靠正则匹配。

### 7.3 Vercel ISR 配置

```typescript
// apps/web/app/api/latest-version/route.ts
export const revalidate = 300; // 5 min

export async function GET() {
  const res = await fetch(
    "https://api.github.com/repos/multica-ai/multica/releases/latest",
    { next: { revalidate: 300 } }
  );
  if (!res.ok) {
    return Response.json({ error: "upstream" }, { status: 502 });
  }
  const data = await res.json();
  return Response.json({
    version: data.tag_name,
    publishedAt: data.published_at,
    assets: parseAssets(data.assets),
  });
}
```

### 7.4 Welcome 屏 `isWeb` 派生

```typescript
// onboarding-flow.tsx
const isWeb = !!runtimeInstructions;

// 传给 StepWelcome
<StepWelcome
  onNext={handleWelcomeNext}
  onSkip={canSkipWelcome ? handleWelcomeSkip : undefined}
  isWeb={isWeb}
/>
```

---

## 八、执行追踪

- [x] Step 1 · 文案 doc → `docs/download-positioning.md`
- [x] Step 2 · `/download` 页面（分支 `NevilleQingNY/download-redesign`）
- [ ] Step 3 · Onboarding 修缮
- [ ] Step 4 · 上游漏斗

### Step 2 产出

- 新文件：`apps/web/app/(landing)/download/page.tsx` + `download-client.tsx`
- 新组件：`apps/web/features/landing/components/download/{hero,all-platforms,cli-section,cloud-section,os-icons}.tsx`
- 新 utils：`apps/web/features/landing/utils/{os-detect,parse-release-assets,github-release}.ts`
- 扩展 i18n：`types.ts` 加 `download` + `auth.login.extra*`；`en.ts` + `zh.ts` 填双语
- Nav 更新：landing footer 的 "Desktop" / "桌面端" 链接 → "Download" / "下载"（指 `/download`）
- `@multica/views/onboarding` 新 export：`CloudWaitlistExpand`（`/download` 的 Cloud 区块复用）

### 本地开发注意

GitHub Releases API 未认证限流是 **60 req/hr per IP**。Vercel 生产环境的 fetch cache 跨所有 region 共享，每 5 分钟（`revalidate: 300`）全局最多 1 次调用，远低于限流。但**本地开发** + 共享办公室 IP 容易打爆限流，命中后页面降级到"Version unavailable"。

本地跑 `/download` 如遇到版本信息缺失：
1. 设置 `GITHUB_TOKEN` 环境变量（Personal Access Token，公共仓库不需要 scope）
2. `fetchLatestRelease` 会自动带 `Authorization: Bearer <token>` header，限流提到 5000 req/hr
3. Token 只在 server-side 用，不会泄漏到客户端

每完成一步，勾掉 checkbox 并在对应 section 底部补一行实际 commit hash。
