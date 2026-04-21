import { api } from "@multica/core/api";
import type { QuestionnaireAnswers } from "@multica/core/onboarding";
import type { Agent, Workspace } from "@multica/core/types";

interface WelcomeIssueSpec {
  title: string;
  description: string;
}

interface SubIssueSpec {
  title: string;
  description: string;
}

interface BootstrapResult {
  firstIssueId: string | null;
  projectId: string | null;
}

export function buildWelcomeIssue(
  q: QuestionnaireAnswers,
  userName: string,
): WelcomeIssueSpec {
  const name = userName.trim() || "there";
  switch (q.use_case) {
    case "coding":
      return {
        title: "Welcome me and show me what you can do",
        description: `Hi, I'm ${name}. I'll use you mostly for coding work. Introduce yourself and suggest 3 concrete coding tasks I could try.`,
      };
    case "planning":
      return {
        title: "Help me plan my first project",
        description: `Hi, I'm ${name}. I want you to help me plan and break down work. Introduce yourself and suggest 3 types of projects we could tackle.`,
      };
    case "writing_research":
      return {
        title: "Show me how you help with research and writing",
        description: `Hi, I'm ${name}. I'll use you for research and writing. Introduce yourself and give me 3 examples of how you can help — drafting, summarizing, analysis, etc.`,
      };
    case "explore":
      return {
        title: "What can you do?",
        description: `Hi. I'm exploring what Multica can do. Give me a quick tour of what you can help with and suggest 3 concrete things to try.`,
      };
    case "other":
      return {
        title: "Help me with what I had in mind",
        description: `Hi, I'm ${name}. I told Multica I want to use you for "${q.use_case_other ?? ""}". Introduce yourself and give me 3 concrete ways you could help with that.`,
      };
    default:
      return {
        title: "Introduce yourself",
        description: `Hi, I'm ${name}. Introduce yourself and tell me what you can help with.`,
      };
  }
}

// Path A — user created an agent. Sub-issues are "watch your agent do X".
export function buildAgentGuidedSubIssues(
  q: QuestionnaireAnswers,
): SubIssueSpec[] {
  const core: SubIssueSpec[] = [
    {
      title: "Chat with your agent without creating an issue",
      description:
        "Some tasks are quick back-and-forth — you don't need a full issue. Open the chat panel from the top-right and try asking your agent a question.",
    },
    {
      title: "Assign a real task to your agent",
      description:
        "You've seen your agent reply in your welcome issue. Now try assigning them something you actually need done. Create a new issue, describe the task, assign it.",
    },
    {
      title: "Write your Workspace Context",
      description:
        "Workspace Context is the shared system prompt every agent in this workspace sees. Tell them who you are, what you're building, and how they should behave. Go to Workspace settings → Context.",
    },
    {
      title: "Create a second agent with a different role",
      description:
        "Multica's real power is running a small team of specialized agents. Create a Planning agent to complement your Coding agent, or a Writing agent to draft content. Go to Agents → New agent.",
    },
    {
      title: "Configure your agent's skills",
      description:
        "Skills let you give your agent specific tools and capabilities. Go to your agent's settings and try toggling a skill.",
    },
    {
      title: "Set up an Autopilot for recurring work",
      description:
        "Autopilot creates issues on a schedule — daily standups, weekly triage, monthly reports. Your agent picks them up automatically. Go to Autopilots → New autopilot.",
    },
  ];

  const result: SubIssueSpec[] = [...core];

  if (q.team_size === "team") {
    result.unshift({
      title: "Invite your teammates",
      description:
        "Multica works best with a small team. Go to Workspace settings → Members and invite your collaborators.",
    });
  }

  if (q.role === "developer" || q.use_case === "coding") {
    const insertIndex = result.findIndex((s) =>
      s.title.startsWith("Assign a real task"),
    );
    const target: SubIssueSpec = {
      title: "Connect a repo to your workspace",
      description:
        "Link your GitHub repo so agents can read and write code directly. Go to Workspace settings → Repos.",
    };
    if (insertIndex >= 0) {
      result.splice(insertIndex + 1, 0, target);
    } else {
      result.push(target);
    }
  }

  return result;
}

// Path B — user has no agent yet. Sub-issues are "try this yourself".
export function buildSelfServeSubIssues(
  q: QuestionnaireAnswers,
): SubIssueSpec[] {
  const core: SubIssueSpec[] = [
    {
      title: "Create your first issue",
      description:
        "Describe something you actually want to get done. Click New issue, give it a title, write the details, and save it. This is how work flows in Multica.",
    },
    {
      title: "Configure a runtime",
      description:
        "A runtime is where your agents actually run. Download the Multica desktop app for a bundled runtime, or install the CLI with `curl -fsSL install.multica.ai | bash`. Come back once it's connected.",
    },
    {
      title: "Create your first agent",
      description:
        "Once a runtime is connected, go to Agents → New agent to create one. Pick a provider (Claude / Codex / Cursor / …) and give it a role. Then come back and assign it an issue.",
    },
    {
      title: "Write your Workspace Context",
      description:
        "Workspace Context is the shared system prompt every agent in this workspace will see. Tell them who you are, what you're building, and how they should behave. Go to Workspace settings → Context.",
    },
    {
      title: "Explore the Agents page",
      description:
        "See what agents look like — their provider, runtime, skills, and instructions — so you know what you're configuring before you build one.",
    },
  ];

  const result: SubIssueSpec[] = [...core];

  if (q.team_size === "team") {
    result.unshift({
      title: "Invite your teammates",
      description:
        "Multica works best with a small team. Go to Workspace settings → Members and invite your collaborators.",
    });
  }

  if (q.role === "developer" || q.use_case === "coding") {
    result.push({
      title: "Connect a repo to your workspace",
      description:
        "Once you have an agent, link your GitHub repo so it can read and write code directly. Go to Workspace settings → Repos.",
    });
  }

  return result;
}

export async function runOnboardingBootstrap({
  agent,
  workspace,
  questionnaire,
  userName,
}: {
  agent: Agent | null;
  workspace: Workspace;
  questionnaire: QuestionnaireAnswers;
  userName: string;
}): Promise<BootstrapResult> {
  let firstIssueId: string | null = null;

  // Path A — welcome issue is critical. If this fails the caller surfaces
  // an error + retry. Path B skips this entirely.
  if (agent) {
    const welcome = buildWelcomeIssue(questionnaire, userName);
    const firstIssue = await api.createIssue({
      title: welcome.title,
      description: welcome.description,
      assignee_type: "agent",
      assignee_id: agent.id,
    });
    firstIssueId = firstIssue.id;
  }

  // Project + sub-issues are nice-to-have in both paths. Failures don't
  // block the user from landing in their workspace.
  let projectId: string | null = null;
  try {
    const project = await api.createProject({
      title: "Getting Started",
      description:
        "A few things to try in Multica. Work through them at your own pace.",
      icon: "👋",
    });
    projectId = project.id;

    const subIssues = agent
      ? buildAgentGuidedSubIssues(questionnaire)
      : buildSelfServeSubIssues(questionnaire);

    await Promise.allSettled(
      subIssues.map((s) =>
        api.createIssue({
          title: s.title,
          description: s.description,
          project_id: project.id,
        }),
      ),
    );
  } catch (err) {
    // eslint-disable-next-line no-console
    console.warn("Onboarding project bootstrap failed", err);
  }

  void workspace;

  return {
    firstIssueId,
    projectId,
  };
}
