# Multica i18n 术语表 (Glossary)

> **所有翻译 agent 必读**。任何 PR 翻译都必须遵守此表。
> 不在表里的词，按"翻译风格"段处理。

## 核心区分：实体 vs 概念

Multica 的产品名词分两类，处理方式完全不同：

- **实体（typed entity）** — 有 URL、有数据库 row、是 API 响应里某种 type 的东西。中文里**用小写英文**呈现，视觉上像类型名，告诉读者"这是 Multica 系统里的特定实体"。
- **概念（concept）** — 不是数据库实体的普通名词。**完整翻译成中文**，CN 用户看不到生硬的英文。

这套规则与 `apps/docs/content/docs/*.zh.mdx` 完全对齐——docs 是已经实战 20+ 篇的 CN voice 标准。

## 不翻 — 实体（小写英文）

| 词 | 中文中的写法 | 例 |
|---|---|---|
| Issue | `issue`（小写） | "把 issue 分配给智能体"、"创建子 issue" |
| Project | `project`（小写） | "归入某个 project" |
| Skill | `skill`（小写） | "为智能体注入 skill" |
| Autopilot | `autopilot`（小写） | "新建 autopilot" |
| Task | `task`（小写） | "排队中的 task" |

## 不翻 — 品牌名 + 通用缩写

| 类别 | 词 |
|---|---|
| 品牌 | **Multica**、GitHub、Slack、Google、Anthropic、OpenAI、Claude、Codex、Cursor、Linear、Jira |
| 缩写 | API、CLI、URL、SDK、OAuth、JWT、SSO、WebSocket、HTTP、JSON、YAML、SQL |

## 完整翻译 — 概念词（必须翻）

| 英 | 中 |
|---|---|
| Workspace | **工作区** |
| Agent | **智能体** |
| Daemon | **守护进程** |
| Runtime | **运行时** |
| Inbox | **收件箱** |
| Comment | **评论** |
| Reply | **回复** |
| Notifications | **通知** |
| Member | **成员** |
| Label | **标签** |
| Settings | **设置** |
| Onboarding | **上手引导** |

## 完整翻译 — 通用业务词

| 英 | 中 |
|---|---|
| Invite / Invitation | 邀请 |
| Search | 搜索 |
| Email | 邮箱（label）/ 邮件（action） |
| Password | 密码 |
| Sign in / Log in | 登录 |
| Sign up | 注册 |
| Sign out / Log out | 退出登录 |
| Save | 保存 |
| Cancel | 取消 |
| Delete | 删除 |
| Confirm | 确认 |
| Continue | 继续 |
| Back | 返回 |
| Edit | 编辑 |
| New | 新建 |
| Create | 创建 |
| Add | 添加 |
| Remove | 移除 |
| Send | 发送 |
| Open | 打开 |
| Close | 关闭 |
| Done | 完成 |
| Loading... | 加载中... |
| Profile | 个人资料 |
| Account | 账号 |
| Appearance | 外观 |
| Theme | 主题 |
| Language | 语言 |
| Light / Dark / System | 浅色 / 深色 / 跟随系统 |
| Active | 活跃 / 启用 |
| Archived | 已归档 |
| Status | 状态 |
| Priority | 优先级 |
| Assignee | 负责人 |
| Reporter | 报告人 |
| Description | 描述 |
| Title | 标题 |
| Date / Time | 日期 / 时间 |
| Today / Yesterday / Tomorrow | 今天 / 昨天 / 明天 |
| Empty | 空 |
| Failed | 失败 |
| Success | 成功 |
| Error | 错误 |
| Warning | 警告 |

## 角色名 + 状态名（lowercase EN，不翻）

角色名和状态枚举值是 schema-level 标识符，保持小写英文：

- 角色：`owner` / `admin` / `member`
- Issue 状态：`backlog` / `todo` / `in_progress` / `in_review` / `done` / `blocked` / `cancelled`

UI 里展示这些 schema 值时，保持英文（必要时用 code-style 包起来）：
- "你需要 owner 权限"、"已切换到 in_progress"。

## 词组组合规则

英文词（实体名 + 品牌名 + 缩写）与中文之间**加单空格**：

- "Create new issue" → "新建 issue"
- "Assign to agent" → "分配给智能体"
- "Open workspace" → "打开工作区"
- "Configure runtime" → "配置运行时"
- "Edit comment" → "编辑评论"
- "Delete label" → "删除标签"
- "Stop daemon" → "停止守护进程"

复数 / 量词：

- `{{count}} issues` → `{{count}} 个 issue`
- `{{count}} agents` → `{{count}} 个智能体`
- `{{count}} workspaces` → `{{count}} 个工作区`
- `{{count}} comments` → `{{count}} 条评论`
- `{{count}} members` → `{{count}} 位成员`
- `{{count}} skills` → `{{count}} 个 skill`

## Key 命名约定

3 层嵌套：`feature.component.action`

```json
{
  "feature_or_component": {
    "subcomponent_or_section": {
      "action_or_label": "..."
    }
  }
}
```

实例：

- `issues.toolbar.batch_update_success`
- `issues.detail.comment_form.placeholder`
- `inbox.empty.title`
- `settings.appearance.language.title`

## 复数处理

- 英文：`key_one` / `key_other`（i18next 标准）
- 中文：**只**填 `_other`（中文不区分单复数）

```json
// en/issues.json
{
  "issue_count_one": "{{count}} issue",
  "issue_count_other": "{{count}} issues"
}

// zh-Hans/issues.json
{
  "issue_count_other": "{{count}} 个 issue"
}
```

## 插值

- 用 `{{var}}` 形式
- 中文翻译可调整位置以符合中文语序

```json
// en
"welcome_message": "Welcome back, {{name}}!"

// zh-Hans
"welcome_message": "欢迎回来，{{name}}！"
```

## 标点 + 排版

- 中文：用全角标点（，。：；！？）
- 引号：用 `"` `"`（直引号），与英文 source 保持一致
- 省略号：用 `...`（三点）而非 `…`（单字符），与英文 source 保持一致
- 中英混排：英文词左右各**加 1 个空格**

## 翻译风格

- **简洁直白**：避免"对于...来说"、"作为..."、"我们的"等翻译腔
- **错误信息**：温和但明确（"无法保存修改" 而非 "保存修改失败了！"）
- **按钮**：动词开头，2-4 字最佳（"取消"、"保存修改"、"立即同步"）
- **Tooltip**：完整短句（"复制链接到剪贴板"）
- **placeholder**：示例性提示（"输入 issue 标题..."）

## 参考实现

- `apps/docs/content/docs/*.zh.mdx` —— **CN voice 的事实标准**，20+ 篇高度一致的实战翻译
- `packages/views/locales/zh-Hans/auth.json` + `editor.json` —— JSON 结构 + selector API 用法参考
- `packages/views/auth/login-page.tsx` —— 组件层 selector API 调用参考
- `packages/views/settings/components/appearance-tab.tsx` —— 含 Language 切换器的参考

## Web-only / Desktop-only 文案位置

- 共享文案放 `{ns}.json` 顶层
- web-only 文案放 `{ns}.json` 的 `web` 段
- desktop-only 文案放 `{ns}.json` 的 `desktop` 段

参考 `auth.json` 的 `web` 段（包含 `prefer_desktop` / `desktop_handoff.*`）。
