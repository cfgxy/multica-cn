# Desktop 下载体系 — 文案定位（Step 1 产出）

**目的**：为 `/download` 页面、onboarding、login、landing 所有 Desktop/CLI/Cloud 相关触点提供**唯一文案真相源**。后续 Step 2/3/4 实现时，UI 层只从这里拿文案，不临时发明。

**双语策略**：遵循当前项目 i18n 现状——
- **Landing / `/download` / Web Login**：i18n 双语（en + zh），接入 `apps/web/features/landing/i18n/`
- **Onboarding（共享 views 包）**：保持英文单语（当前现状，i18n 基建本次不引入）

---

## 一、三个 surface 的核心定位句

写 UI 时所有文案都派生自这三句。每一句都**以场景开头**，不以能力比较开头。

| Surface | EN | ZH |
|---|---|---|
| **Desktop** | Install the app. Agents run on your machine. | 下载桌面应用，agent 在你的电脑上运行。 |
| **CLI** | For servers, remote dev boxes, and automation. | 适合服务器、远程开发机、自动化场景。 |
| **Cloud** | We host the runtime. No local install. | 我们为你托管 runtime，无需本地安装。 |

### 一句话决策树（用户视角）

- "我就是想在自己电脑上用" → Desktop
- "我想让 agent 跑在我的服务器 / 远程机器上" → CLI
- "我一点都不想装东西" → Cloud（目前是 waitlist）

---

## 二、文案设计原则

| 原则 | 理由 | 例子 |
|---|---|---|
| 场景先于能力 | Desktop 和 CLI 运行后能力等价，差异在 setup moment 和使用场景 | ✅ "For servers and remote boxes" / ❌ "Lighter-weight Desktop" |
| 避免"easy / simple / just" | 这些是 claim，用户不信；且会和现实冲突（CLI 的 `multica setup` 实际 10-30s） | ✅ "Terminal setup" / ❌ "Just one command" |
| 诚实时间估计 | Welcome 当前 "Takes about 3 minutes" 对 web 用户是谎 | ✅ 差异化文案或去掉时间 |
| 第二人称 + 直接语气 | 和 Linear / Cursor 一致 | ✅ "Agents run on your Mac." / ❌ "Our runtime operates locally." |
| 不夸"强大"/"智能" | 现代用户免疫 marketing 形容词 | ✅ "Agents pick up tasks." / ❌ "Powerful AI agents tackle your work." |

---

## 三、触点文案对照表

### 3.1 Landing Hero（web only）

**位置**：`apps/web/features/landing/components/landing-hero.tsx:44-65` + `landing/i18n/en.ts:19` + `zh.ts:19`

**当前**：
- EN: `"Download Desktop"` (ghost 按钮)
- ZH: `"下载桌面端"` (ghost 按钮)
- href: `https://github.com/multica-ai/multica/releases/latest`

**新**：
- EN: `"Download Desktop"` ← 文案不变，**视觉升级为 primary/solid** + **href 改为 `/download`**
- ZH: `"下载桌面端"` ← 同上
- i18n key：复用现有 `hero.downloadDesktop`，**不新增 key**

**理由**：
- 文案已经合适
- 改动只在视觉权重（ghost → solid）和链接目标（GitHub releases → `/download`）
- href 变更落在 `landing-hero.tsx:45` 的 hardcoded URL——改成相对路径 `/download`

---

### 3.2 Landing Nav / Footer（web only）

**位置**：`landing/i18n/en.ts:230` + `zh.ts:230`

**当前**：
```ts
{ label: "Desktop" / "桌面端", href: "https://github.com/multica-ai/multica/releases/latest" }
```

**新**：
```ts
{ label: "Download" / "下载", href: "/download" }
```

**理由**：
- 把"Desktop"改成"Download"——`/download` 页面本身就是三个选项的聚合（Desktop/CLI/Cloud），不只是 desktop
- href 统一到 `/download`

---

### 3.3 Web Login Page — 新增 Desktop CTA

**位置**：`apps/web/app/(auth)/login/page.tsx` 调用 `LoginPage`（`packages/views/auth/login-page.tsx`）时注入新 prop `extra`

**当前**：无此 UI

**新**（Google 按钮下方低调一行）：
- EN: `"Prefer the desktop app? Download →"`
- ZH: `"想用桌面应用？下载 →"`

**i18n**：新增 key
```ts
auth: {
  login: {
    extraDownloadPrompt: "Prefer the desktop app?" / "想用桌面应用？",
    extraDownloadCta: "Download" / "下载",
  }
}
```

**理由**：
- 登录页是**最轻投入时刻**，推 Desktop 最便宜
- 不强推，低调一行，不影响 Google OAuth 主流
- Desktop app 的 Login Page（`apps/desktop/src/renderer/src/pages/login.tsx`）**不传** `extra` → 不显示。这条 CTA 只在 web 出现。

---

### 3.4 Welcome 屏 — web 分支新增 Desktop 引导

**位置**：`packages/views/onboarding/steps/step-welcome.tsx`（单语英文）

**当前**（所有平台）：
```
Takes about 3 minutes. You'll end with a real agent
replying to a real issue.
```
按钮：`Start exploring` + (optional) `I've done this before`

**新**（web 分支，`isWeb=true`）：
- 上方文字保留 `"Your AI teammates, in one workspace."` 标题
- 副文案改为：`"About 3 minutes on desktop. A bit more on web — you'll need a local runtime."`
- 按钮区追加第三个按钮（视觉权重：primary > secondary ghost）：
  - Primary: `"Start exploring"` (保留，引导继续 web 流程)
  - **新增 secondary**: `"Download Desktop — faster setup"`（指向 `/download`，新窗口打开）
  - Ghost: `"I've done this before"` (保留条件)

**新**（desktop 分支，`isWeb=false`）：
- 文案完全保持：`"Takes about 3 minutes. You'll end with a real agent replying to a real issue."`
- 按钮不变

**理由**：
- 首次向 web 用户承认"desktop 更顺"——早于任何投入
- Primary CTA 仍是"Start exploring"——不强推 desktop，只是让它可见
- 3 minutes 文案按平台差异化——对 desktop 用户诚实，对 web 用户不再骗

---

### 3.5 Step 3 Platform Fork — Desktop 卡

**位置**：`packages/views/onboarding/steps/step-platform-fork.tsx:232-299`（`ForkPrimary` 组件）

**当前（Mac detect 命中）**：
- 标题：`"Download the desktop app"`
- 副文案：`"macOS · runtime bundled — detects your tools automatically, nothing to install."`

**当前（非 Mac）**：
- 标题：`"Desktop app — macOS only for now"` (disabled card)
- 副文案：`"Windows and Linux builds are on the way. In the meantime, install the CLI below — it takes about two minutes."`

**新（所有平台，按 detect 结果适配）**：
- 标题：`"Download the desktop app"`
- 副文案（按 detect 分支）：
  - macOS arm64: `"macOS (Apple Silicon) · bundled daemon, zero setup."`
  - macOS Intel/unknown: `"macOS · bundled daemon, zero setup."` + 小字 `"Apple Silicon only — on Intel? Use CLI."`
  - Windows: `"Windows · bundled daemon, zero setup."`
  - Linux: `"Linux · bundled daemon, zero setup."`
  - 非 Mac 检测不到: `"Bundled daemon, zero setup."`
- 按钮 pill：`"Download"`（不变）

**理由**：
- 拆掉 `isMac` 门——Windows/Linux 包已经齐
- 副文案主打**"zero setup"**——这才是和 CLI 的真差异
- Intel Mac 诚实提示，不骗他们点 arm64 包

---

### 3.6 Step 3 Platform Fork — CLI 卡

**位置**：同上，`ForkAlt` 调用

**当前**：
- 标题：`"Install the CLI"`
- 副文案：`"Run the Multica daemon yourself — a couple of terminal commands."`
- 按钮：`"Show steps"`

**新**：
- 标题：`"Install the CLI"` (不变)
- 副文案：`"For servers, remote dev boxes, and headless setups. Terminal required."`
- 按钮：`"Show steps"` (不变)

**理由**：
- 副文案从"自己跑 daemon"改为"服务器 / 远程 / headless"——让 CLI 归位到它真正的场景
- 加 "Terminal required" 给用户明确预期，不伪装成轻量路径

---

### 3.7 Step 3 Platform Fork — Cloud 卡

**位置**：同上，`ForkAlt` 调用

**当前**：
- 标题：`"Cloud runtime"`
- 副文案：`"We host it for you. Not live yet — leave your email and we'll let you know."`
- 按钮：`"Join waitlist"` / `"On the list"`

**新**：
- 标题：`"Cloud runtime"` (不变)
- 副文案：`"We host the runtime. Not live yet — join the waitlist."`
- 按钮不变

**理由**：
- 微调，对齐定位句
- 不再把 "we'll let you know" 说得像客户支持

---

### 3.8 Step 3 Footer Hint

**位置**：`step-platform-fork.tsx:101-112`

**当前 non-Mac**：`"Install the CLI to connect a runtime, or skip for now."`

**新（去掉 non-Mac 分支，因为 Desktop 对所有平台 active）**：
```ts
if (waitlistSubmitted) return "You're on the waitlist — pick Skip to keep exploring.";
if (downloaded) return "Downloading… finish setup in the desktop app, or pick another path.";
return "Pick a path above — or skip and configure a runtime later.";
```

**理由**：删掉 non-Mac 专属分支，现在所有平台 Desktop 都可用。

---

### 3.9 CLI Install Dialog — Title + Description

**位置**：`step-platform-fork.tsx:378-384`

**当前**：
```
Title: "Install the CLI"
Description: "Runs the same daemon the desktop app bundles — you install it yourself."
```

**新**：
```
Title: "Install the CLI"
Description: "Same daemon, installed on your terminal. Use it when Desktop doesn't fit — servers, remote dev boxes, or headless setups."
```

**理由**：
- 明确 CLI 和 Desktop 是**同一个 daemon**——消除"CLI 是否弱化版 Desktop"的误解
- 直接说 CLI 的正当场景——当 Desktop 不适合时

---

### 3.10 CliInstallInstructions — 头部提示

**位置**：`packages/views/onboarding/steps/cli-install-instructions.tsx:65-68`

**当前**：
```
You'll need a local AI coding tool (Claude Code, Codex,
Cursor, …) installed for the runtime to do real work.
```

**新**：
```
You'll need an AI coding tool on this machine (Claude Code,
Codex, Cursor, …) for the daemon to do real work. Also works
on servers and remote dev boxes.
```

**理由**：
- 最后一句点出 CLI 的远程场景——和 Step 3 CLI 卡的副文案呼应

---

### 3.11 CLI Dialog Waiting — "Stalled" 文案

**位置**：`step-platform-fork.tsx:552-561`

**当前**：
```
Nothing coming through yet. Close this dialog and try another
path on the previous screen — Skip for now (in the footer)
enters your workspace in read-only mode, or the Cloud runtime
card lets you join the waitlist.
```

**新**：
```
Nothing coming through yet. If you're not comfortable with the
terminal, Desktop is the smoother path — it bundles the daemon.
Close this dialog and pick Desktop, or hit Skip to continue.
```

**理由**：
- 在 stall 发生时主动把 Desktop 作为退路——这是用户最需要听到的
- 原文案把 Cloud waitlist 作为退路不合理（那是 soft exit，不解决问题）

---

### 3.12 `/download` 页面（全新，i18n 双语）

**位置**：`apps/web/app/(landing)/download/page.tsx`（新建）

**页面结构 + 文案**：

#### Hero 区（顶部主 CTA，按 detect 结果拼出）

**检测到 macOS arm64**：
- EN: 
  - H1: `"Multica for macOS"`
  - Sub: `"Apple Silicon · bundled daemon, zero setup"`
  - Primary button: `"Download (.dmg)"` → macArm64Dmg
  - Alt link: `"or download .zip"` → macArm64Zip
- ZH:
  - H1: `"Multica for macOS"`
  - Sub: `"Apple Silicon · 内置 daemon，无需额外配置"`
  - Primary: `"下载 (.dmg)"`
  - Alt: `"或下载 .zip"`

**检测到 macOS Intel（Chromium）**：
- EN:
  - H1: `"Multica for macOS"`
  - Sub: `"Apple Silicon required — Intel Macs not yet supported."`
  - Primary button 样式: **muted + disabled**，文案 `"Apple Silicon required"`
  - 次要段落：`"On an Intel Mac? Use the CLI below — it runs the same daemon."`
- ZH：对应翻译

**检测到 Windows x64**：
- EN:
  - H1: `"Multica for Windows"`
  - Sub: `"Bundled daemon, zero setup"`
  - Primary: `"Download (.exe)"` → winX64Exe
- ZH: `"Multica for Windows"` / `"内置 daemon，无需额外配置"` / `"下载 (.exe)"`

**检测到 Linux**：
- EN:
  - H1: `"Multica for Linux"`
  - Primary: `"Download AppImage"` → linuxAmd64AppImage
  - Alt links: `"or .deb / .rpm"`
- ZH: 对应翻译

**未检测 / SSR 初始状态**：
- 默认渲染 macOS arm64 作为 H1（占位）；JS hydration 后按 detect 替换

#### All Platforms 区（永远可见，在 Hero 下方）

**标题**：
- EN: `"All platforms"`
- ZH: `"所有平台"`

**内容**：表格或卡片，每行一个包：
```
macOS      · Apple Silicon (.dmg / .zip)
Windows    · x64 (.exe)    · ARM64 (.exe)
Linux      · x64 (.AppImage / .deb / .rpm)   · ARM64 (.AppImage / .deb / .rpm)
```

**Intel Mac 说明**：
- EN: `"Apple Silicon only — Intel Macs not supported in this release."`
- ZH: `"仅支持 Apple Silicon——Intel Mac 目前暂不支持。"`

#### CLI 区（二级标题，独立 section）

**标题**：
- EN: `"Prefer the CLI?"`
- ZH: `"想用 CLI？"`

**副文案**：
- EN: `"For servers, remote dev boxes, and headless setups. Same daemon as Desktop, installed via terminal."`
- ZH: `"适合服务器、远程开发机、无图形界面环境。底层 daemon 和 Desktop 相同，通过终端安装。"`

**命令块**（复用 `CliInstallInstructions` 的样式）：
```bash
# Install
curl -fsSL https://raw.githubusercontent.com/multica-ai/multica/main/scripts/install.sh | bash

# Start daemon
multica setup
```

**底部说明**：
- EN: `"Already on a server? Same commands work over SSH."`
- ZH: `"已经在服务器上？通过 SSH 执行同样的命令即可。"`

#### Cloud 区（最小、置底）

**标题**：
- EN: `"Cloud runtime (waitlist)"`
- ZH: `"Cloud runtime（等待名单）"`

**副文案**：
- EN: `"We'll host the runtime for you. Not live yet — leave your email to be notified."`
- ZH: `"我们将为你托管 runtime，目前尚未上线——留下邮箱，上线后通知你。"`

**表单**：复用 `CloudWaitlistExpand`（`packages/views/onboarding/components/cloud-waitlist-expand.tsx`）

#### Footer 区

- Release notes 链接：`"What's new in {version}"` → GitHub release tag URL
- All releases：`"View all releases →"` → `https://github.com/multica-ai/multica/releases`
- 版本号小字：`"Current version: v0.2.13"`（来自 `/api/latest-version`）

---

## 四、i18n Keys 规划

### 4.1 现有 key 复用

- `hero.downloadDesktop` — 保持，landing hero 按钮文案
- `nav.desktop` → **重命名为 `nav.download`**（需同步改 landing-nav 组件读的 key）

### 4.2 新增 key 命名空间

```ts
// apps/web/features/landing/i18n/en.ts + zh.ts
{
  // ... 现有 key ...

  download: {
    hero: {
      macArm64: {
        title: "Multica for macOS",
        sub: "Apple Silicon · bundled daemon, zero setup",
        primary: "Download (.dmg)",
        altZip: "or download .zip",
      },
      macIntel: {
        title: "Multica for macOS",
        sub: "Apple Silicon required — Intel Macs not yet supported.",
        disabledCta: "Apple Silicon required",
        intelHint: "On an Intel Mac? Use the CLI below — it runs the same daemon.",
      },
      winX64: {
        title: "Multica for Windows",
        sub: "Bundled daemon, zero setup",
        primary: "Download (.exe)",
      },
      winArm64: {
        title: "Multica for Windows",
        sub: "ARM · bundled daemon, zero setup",
        primary: "Download (.exe)",
      },
      linux: {
        title: "Multica for Linux",
        sub: "Bundled daemon, zero setup",
        primary: "Download AppImage",
        altFormats: "or .deb / .rpm",
      },
    },
    allPlatforms: {
      title: "All platforms",
      macLabel: "macOS · Apple Silicon",
      winX64Label: "Windows · x64",
      winArm64Label: "Windows · ARM64",
      linuxX64Label: "Linux · x64",
      linuxArm64Label: "Linux · ARM64",
      intelNote: "Apple Silicon only — Intel Macs not supported in this release.",
    },
    cli: {
      title: "Prefer the CLI?",
      sub: "For servers, remote dev boxes, and headless setups. Same daemon as Desktop, installed via terminal.",
      installLabel: "Install",
      startLabel: "Start daemon",
      sshNote: "Already on a server? Same commands work over SSH.",
    },
    cloud: {
      title: "Cloud runtime (waitlist)",
      sub: "We'll host the runtime for you. Not live yet — leave your email to be notified.",
    },
    footer: {
      releaseNotes: "What's new in {version}",
      allReleases: "View all releases",
      currentVersion: "Current version: {version}",
    },
  },

  auth: {
    login: {
      extraDownloadPrompt: "Prefer the desktop app?",
      extraDownloadCta: "Download",
    },
  },
}
```

中文翻译按 ZH 对照表同步填入 `zh.ts`。

### 4.3 Onboarding 触点（单语英文）

所有 `step-welcome.tsx` / `step-platform-fork.tsx` / `cli-install-instructions.tsx` 的新文案**直接硬编码到 TSX**，不进 i18n——保持和当前 onboarding 代码风格一致。

---

## 五、文案审校清单

Step 2/3/4 实施时，逐条检查：

- [ ] 每个触点的文案都在本文档有定义（不临时发明）
- [ ] Landing / `/download` / Login extra 走 i18n，双语齐备
- [ ] Onboarding 触点英文硬编码，与现有代码风格一致
- [ ] "3 minutes" 时间声明仅出现在 desktop 分支
- [ ] 没有 "easy / simple / just" 出现在 Desktop 或 CLI 文案里
- [ ] 所有 Download CTA 指向 `/download`，不再有直接指向 GitHub releases 的链接（landing nav、landing hero、step 3 Desktop 卡点击、登录页 extra）
- [ ] CLI 文案强调 server/remote/headless 场景，不再暗示"Desktop 的轻量版"
- [ ] Intel Mac 处处诚实标注，不欺骗

---

## 六、开放事项

- `/download` 页面的视觉风格是否跟 landing 一致（serif 标题 / 背景色）？→ **建议跟 landing 一致**，但本文档不锁死，Step 2 UI 实现时决定
- 是否加"系统最低要求"区块？→ **不做**（Cursor 有，但我们产品期不引入这种 clutter）
- 是否在 `/download` 置顶放一个 `<video>` 或产品截图？→ **不做**（保持克制；landing 已承担营销角色）
