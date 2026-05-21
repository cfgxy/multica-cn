package service

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/issueguard"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// NoRuntimeIssueTitle is the canonical title of the install-runtime onboarding
// seed. Exported so legacy callers (test fixtures, analytics dashboards) can
// dedupe against it by title.
const NoRuntimeIssueTitle = "Connect a runtime to start using agents"

// WorkspaceContentService is the single entry point for seeding workspace-level
// onboarding content (currently: the install-runtime issue). Every prior
// caller (CreateWorkspace, AcceptInvitation, CompleteOnboarding,
// BootstrapOnboardingNoRuntime) now goes through this service so the seeding
// behaviour has one definition and one test surface.
type WorkspaceContentService struct{}

func NewWorkspaceContentService() *WorkspaceContentService {
	return &WorkspaceContentService{}
}

// EnsureInstallRuntimeIssue gates on "workspace has no runtime yet" — used by
// completion paths that aren't an explicit runtime skip (CreateWorkspace,
// AcceptInvitation, CompleteOnboarding). When the workspace already has a
// runtime registered, this is a no-op.
//
// Must run inside the caller's transaction (`q` is typically a `qtx`).
func (s *WorkspaceContentService) EnsureInstallRuntimeIssue(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	userID pgtype.UUID,
	language pgtype.Text,
) (db.Issue, bool, error) {
	runtimes, err := q.ListAgentRuntimes(ctx, workspaceID)
	if err != nil {
		return db.Issue{}, false, err
	}
	if len(runtimes) > 0 {
		return db.Issue{}, false, nil
	}
	return s.SeedInstallRuntimeIssue(ctx, q, workspaceID, userID, language)
}

// SeedInstallRuntimeIssue is the unconditional seeder used by the explicit
// runtime-skip path. Dedupes against existing active issues with the same
// title via pg_advisory_xact_lock so concurrent callers can't produce two
// copies. Must run inside a transaction.
func (s *WorkspaceContentService) SeedInstallRuntimeIssue(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	userID pgtype.UUID,
	language pgtype.Text,
) (db.Issue, bool, error) {
	var emptyUUID pgtype.UUID
	existing, foundIssue, err := issueguard.LockAndFindActiveDuplicate(
		ctx, q, workspaceID, emptyUUID, emptyUUID, NoRuntimeIssueTitle, false,
	)
	if err != nil {
		return db.Issue{}, false, err
	}
	if foundIssue {
		return existing, false, nil
	}

	issueNumber, err := q.IncrementIssueCounter(ctx, workspaceID)
	if err != nil {
		return db.Issue{}, false, err
	}
	issue, err := q.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID:   workspaceID,
		Title:         NoRuntimeIssueTitle,
		Description:   strOrNullText(noRuntimeIssueDescription(language)),
		Status:        "todo",
		Priority:      "high",
		AssigneeType:  pgtype.Text{String: "member", Valid: true},
		AssigneeID:    userID,
		CreatorType:   "member",
		CreatorID:     userID,
		ParentIssueID: emptyUUID,
		Position:      0,
		Number:        issueNumber,
		ProjectID:     emptyUUID,
	})
	if err != nil {
		return db.Issue{}, false, err
	}
	return issue, true, nil
}

// noRuntimeIssueDescription picks the EN or ZH copy based on the user's
// language preference. ZH selected on any "zh*" prefix (zh, zh-CN, zh-Hans).
func noRuntimeIssueDescription(language pgtype.Text) string {
	if language.Valid && strings.HasPrefix(language.String, "zh") {
		return zhNoRuntimeIssueDescription()
	}
	return enNoRuntimeIssueDescription()
}

func enNoRuntimeIssueDescription() string {
	return strings.Join([]string{
		"Welcome to Multica.",
		"",
		"Agents need a runtime before they can execute work. You can still use Multica as a lightweight project-management workspace while you install one.",
		"",
		"## Try Multica first",
		"",
		"Before the runtime is ready, you can:",
		"",
		"1. Create a project for your current work.",
		"2. Create a few issues and move them across backlog, todo, in_progress, and done.",
		"3. Add priorities, labels, comments, and subscriptions.",
		"4. Use Inbox to track assignments and mentions.",
		"",
		"That gives you the project-management layer first. Once a runtime is connected, agents can start working from the same issues.",
		"",
		"## Install your first agent runtime",
		"",
		"Full guide: https://multica.ai/docs/install-agent-runtime",
		"",
		"For English users, the fastest first path is Codex:",
		"",
		"1. Make sure Node.js is installed.",
		"2. Install Codex:",
		"   npm i -g @openai/codex",
		"3. Sign in:",
		"   codex",
		"4. Confirm your terminal can find it:",
		"   which codex",
		"   codex --version",
		"5. Restart the Multica daemon:",
		"   multica daemon restart",
		"   If you use the desktop app, restarting the app is enough.",
		"6. Return to Runtimes and refresh. You should see a Codex runtime online.",
		"7. Create your first agent from that runtime, then assign an issue to the agent and set status to todo.",
		"",
		"Codex reference: https://developers.openai.com/codex/cli",
		"",
		"When the runtime is connected, you can create Multica Helper for a guided first run.",
	}, "\n")
}

func zhNoRuntimeIssueDescription() string {
	return strings.Join([]string{
		"欢迎来到 Multica。",
		"",
		"智能体需要先连上运行时才能执行工作。运行时还没准备好时，你也可以先把 Multica 当作轻量项目管理工具体验起来。",
		"",
		"## 先体验项目管理功能",
		"",
		"运行时安装前，你可以先做这些事：",
		"",
		"1. 为当前工作创建一个项目。",
		"2. 新建几个 issue，并在 backlog、todo、in_progress、done 之间流转。",
		"3. 给 issue 加优先级、标签、评论和订阅。",
		"4. 用收件箱追踪分配给你的事项和 @mention。",
		"",
		"这样你先熟悉项目管理层。连上运行时后，智能体会直接在这些 issue 上开始工作。",
		"",
		"## 安装第一个 Agent 运行时",
		"",
		"完整文档：https://multica.ai/docs/install-agent-runtime",
		"",
		"中文用户建议先装 Kimi CLI：",
		"",
		"1. 在 macOS / Linux 终端安装 Kimi CLI：",
		"   curl -LsSf https://code.kimi.com/install.sh | bash",
		"   Windows PowerShell：",
		"   Invoke-RestMethod https://code.kimi.com/install.ps1 | Invoke-Expression",
		"2. 确认终端能找到 Kimi：",
		"   kimi --version",
		"3. 在你想让 Kimi 工作的项目目录里启动一次：",
		"   kimi",
		"4. 首次启动后输入 /login，按提示完成 Kimi Code 或 API key 配置。",
		"5. 重启 Multica 守护进程：",
		"   multica daemon restart",
		"   如果你用桌面端，重启 app 即可。",
		"6. 回到 Runtimes 页面刷新。你应该能看到一个在线的 Kimi 运行时。",
		"7. 用这个运行时创建第一个智能体，再把一个 issue 分配给它，并把状态切到 todo。",
		"",
		"Kimi CLI 官方文档：https://moonshotai.github.io/kimi-cli/zh/guides/getting-started.html",
		"",
		"运行时连上后，你就可以创建 Multica Helper，开始一次有智能体参与的上手引导。",
	}, "\n")
}

// strOrNullText converts an empty string to SQL NULL, non-empty to Valid text.
// Local copy so service package doesn't need to depend on handler.
func strOrNullText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}
