# Multica i18n 术语表 (Glossary)

> **所有翻译 agent 必读**。任何 PR 翻译都必须遵守此表。
> 如有词条不在表里，按"翻译风格"段落处理；不确定时倾向**不翻**专有词。

## 不翻（产品专有名词 + 工程术语 + 品牌缩写）

| 词 | 原文保留 | 理由 |
|---|---|---|
| Issue | Issue | Multica 核心实体；与 Linear/GitHub 一致 |
| Workspace | Workspace | 产品概念；工程圈惯例 |
| Agent | Agent | 核心定位词（agent-as-teammate）；翻"智能体"破坏产品定位 |
| Skill | Skill | 专有概念（agent 的能力包）；翻"技能"会跟"个人技能"混淆 |
| Autopilot | Autopilot | 产品功能名（类似品牌词） |
| Daemon | Daemon | 技术术语（本地后台进程） |
| Runtime | Runtime | 技术概念 |
| Multica | Multica | 品牌名 |
| CLI / API / URL / SDK / OAuth / JWT / SSO / WebSocket | 原文 | 通用缩写 |
| GitHub / Slack / Google / Anthropic / OpenAI / Claude / Codex | 原文 | 第三方品牌 |

## 翻译（通用业务词）

| 英 | 中 |
|---|---|
| Inbox | 收件箱 |
| Project | 项目 |
| Comment | 评论 |
| Reply | 回复 |
| Label | 标签 |
| Member | 成员 |
| Settings | 设置 |
| Invite | 邀请 |
| Invitation | 邀请 |
| Search | 搜索 |
| Notifications | 通知 |
| Onboarding | 上手引导 |
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

## 词组组合规则

英文术语保留时，与中文之间**加单空格**（中英混排标准）：

- "Create new issue" → "新建 Issue"
- "Assign to agent" → "分配给 Agent"
- "Open workspace" → "打开 Workspace"
- "Configure runtime" → "配置 Runtime"
- "Edit comment" → "编辑评论"
- "Delete label" → "删除标签"

复数 / 量词：

- "{{count}} issues" → "{{count}} 个 Issue"
- "{{count}} agents" → "{{count}} 个 Agent"
- "{{count}} workspaces" → "{{count}} 个 Workspace"
- "{{count}} comments" → "{{count}} 条评论"
- "{{count}} members" → "{{count}} 位成员"

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
  "issue_count_other": "{{count}} 个 Issue"
}
```

## 插值

- 用 `{{var}}` 形式
- 中文翻译可以调整位置以符合中文语序

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

- `packages/views/locales/en/auth.json` + `zh-Hans/auth.json`：登录页（含 web-only 段）
- `packages/views/locales/en/settings.json` + `zh-Hans/settings.json`：Settings Appearance Tab
- `packages/views/auth/login-page.tsx`：使用 selector API 的组件参考实现
- `packages/views/settings/components/appearance-tab.tsx`：含 Language 切换器的参考

## Web-only / Desktop-only 文案位置

- 共享文案放 `{ns}.json` 顶层
- web-only 文案放 `{ns}.json` 的 `web` 段
- desktop-only 文案放 `{ns}.json` 的 `desktop` 段

参考 `auth.json` 的 `web` 段（包含 `prefer_desktop` / `desktop_handoff.*`）。
