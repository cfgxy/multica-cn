// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";
import enAutopilots from "../../../locales/en/autopilots.json";
import type { AgentWebhook } from "@multica/core/types";

// The tab's data layer is stubbed at the module boundary: the query hook
// branches on the query key (members vs webhooks), and the api client's
// webhook methods are recorded spies. This keeps the assertions on the tab's
// own logic — gating, masking, states, limit — per the established pattern in
// agent-mcp-tab.test.tsx.
const webhooksRef = vi.hoisted(() => ({
  current: [] as AgentWebhook[],
}));
const membersRef = vi.hoisted(() => ({
  current: [
    { user_id: "user-1", role: "owner" },
  ] as { user_id: string; role: string }[],
}));
const userRef = vi.hoisted(() => ({ current: { id: "user-1" } as { id: string } | null }));
const queryStateRef = vi.hoisted(() => ({
  isLoading: false,
  isError: false,
}));
const apiSpies = vi.hoisted(() => ({
  create: vi.fn(),
  update: vi.fn(),
  setEnabled: vi.fn(),
  rotate: vi.fn(),
  remove: vi.fn(),
}));

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
      const key = JSON.stringify(opts.queryKey);
      if (key.includes("webhooks")) {
        if (queryStateRef.isLoading)
          return {
            data: undefined,
            isPending: true,
            isLoading: true,
            isError: false,
            isSuccess: false,
            refetch: vi.fn(),
          };
        if (queryStateRef.isError)
          return {
            data: undefined,
            isPending: false,
            isLoading: false,
            isError: true,
            isSuccess: false,
            refetch: vi.fn(),
          };
        return {
          data: webhooksRef.current,
          isPending: false,
          isLoading: false,
          isError: false,
          isSuccess: true,
          refetch: vi.fn(),
        };
      }
      if (key.includes("members")) return { data: membersRef.current, isLoading: false, isError: false };
      return { data: undefined, isLoading: false, isError: false };
    },
  };
});

vi.mock("@multica/core/api", () => ({
  api: {
    getBaseUrl: () => "https://api.example",
    createAgentWebhook: apiSpies.create,
    updateAgentWebhook: apiSpies.update,
    setAgentWebhookEnabled: apiSpies.setEnabled,
    rotateAgentWebhook: apiSpies.rotate,
    deleteAgentWebhook: apiSpies.remove,
  },
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } | null }) => unknown) => sel({ user: userRef.current }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: (wsId: string) => ({ queryKey: ["workspaces", wsId, "members"] }),
}));

vi.mock("@multica/ui/lib/clipboard", () => ({
  copyText: vi.fn().mockResolvedValue(true),
}));

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WebhooksTab } from "./webhooks-tab";

const TEST_RESOURCES = {
  en: { common: enCommon, agents: enAgents, autopilots: enAutopilots },
};

function webhookFixture(overrides: Partial<AgentWebhook> = {}): AgentWebhook {
  return {
    id: "wh-1",
    agent_id: "agent-1",
    name: "GitHub push",
    prompt: "Check the latest commits and summarize changes.",
    enabled: true,
    created_at: "2026-09-03T00:00:00Z",
    updated_at: "2026-09-03T00:00:00Z",
    webhook_path_masked: "/api/webhooks/agents/••••••••••••",
    webhook_token: "awt_secrettoken",
    webhook_path: "/api/webhooks/agents/awt_secrettoken",
    webhook_url: null,
    ...overrides,
  };
}

function renderTab() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider resources={TEST_RESOURCES} locale="en">
        <WebhooksTab agent={{ id: "agent-1", owner_id: "user-1" }} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

function toManager() {
  userRef.current = { id: "user-1" };
  membersRef.current = [{ user_id: "user-1", role: "owner" }];
}

function toViewer() {
  userRef.current = { id: "user-2" };
  membersRef.current = [
    { user_id: "user-1", role: "owner" },
    { user_id: "user-2", role: "member" },
  ];
}

beforeEach(() => {
  apiSpies.create.mockReset();
  apiSpies.update.mockReset();
  apiSpies.setEnabled.mockReset();
  apiSpies.rotate.mockReset();
  apiSpies.remove.mockReset();
  queryStateRef.isLoading = false;
  queryStateRef.isError = false;
  toManager();
});

describe("WebhooksTab — walkthrough items 1/3/5/6/10", () => {
  it("renders rows with name, prompt preview and a masked URL that never exposes the token", () => {
    webhooksRef.current = [webhookFixture()];
    renderTab();

    expect(screen.getByText("GitHub push")).toBeInTheDocument();
    expect(
      screen.getByText(/Check the latest commits and summarize/),
    ).toBeInTheDocument();
    // Masked by default — the raw token must not be anywhere in the DOM.
    expect(screen.queryByText(/awt_secrettoken/)).not.toBeInTheDocument();
    expect(screen.getAllByText(/••••••••••••/).length).toBeGreaterThan(0);
  });

  it("shows the empty state with an add CTA for managers", () => {
    webhooksRef.current = [];
    renderTab();

    expect(screen.getByText("No webhooks yet")).toBeInTheDocument();
    const addButtons = screen.getAllByRole("button", { name: /add webhook/i });
    expect(addButtons.length).toBeGreaterThan(0);
  });

  it("renders the read-only empty text for non-managers with no CTA", () => {
    toViewer();
    webhooksRef.current = [];
    renderTab();

    expect(screen.getByText("This agent has no webhooks configured.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /add webhook/i })).not.toBeInTheDocument();
  });

  it("gives non-managers no write controls and a masked, token-free URL", () => {
    toViewer();
    webhooksRef.current = [webhookFixture()];
    renderTab();

    // Masked preview resolves against the API base but never the token.
    expect(screen.queryByText(/awt_secrettoken/)).not.toBeInTheDocument();
    expect(screen.getByText(/api\.example\.\/api\/webhooks\/agents\/••••••••••••|api\.example/)).toBeInTheDocument();
    // No switches, no rotate/delete buttons, no add button.
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /regenerate link/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^delete$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /add webhook/i })).not.toBeInTheDocument();
  });

  it("toggling the switch calls setAgentWebhookEnabled with the row id", async () => {
    const user = userEvent.setup();
    webhooksRef.current = [webhookFixture()];
    apiSpies.setEnabled.mockResolvedValue(webhookFixture());
    renderTab();

    await user.click(screen.getByRole("switch", { name: "GitHub push" }));
    await waitFor(() => {
      expect(apiSpies.setEnabled).toHaveBeenCalledWith("agent-1", "wh-1", false);
    });
  });

  it("shows the disabled badge when the webhook is off", () => {
    webhooksRef.current = [webhookFixture({ enabled: false })];
    renderTab();

    expect(screen.getByText("Disabled")).toBeInTheDocument();
  });

  it("disables the add button with an inline hint at the 20-webhook limit", () => {
    webhooksRef.current = Array.from({ length: 20 }, (_, i) =>
      webhookFixture({ id: `wh-${i}`, name: `hook ${i}` }),
    );
    renderTab();

    expect(screen.getByText(/limit reached \(max 20 per agent\)/i)).toBeInTheDocument();
    for (const btn of screen.getAllByRole("button", { name: /add webhook/i })) {
      expect(btn).toBeDisabled();
    }
  });

  it("renders the loading skeleton before data arrives", () => {
    queryStateRef.isLoading = true;
    renderTab();

    expect(screen.getByTestId("webhooks-loading")).toBeInTheDocument();
  });

  it("renders the error state with a retry button", () => {
    queryStateRef.isError = true;
    renderTab();

    expect(screen.getByText("Couldn't load webhooks")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});

describe("WebhooksTab — create flow", () => {
  it("creates a webhook and shows the success panel with the amber warning", async () => {
    const user = userEvent.setup();
    webhooksRef.current = [];
    apiSpies.create.mockResolvedValue(webhookFixture());
    renderTab();

    const addButton = screen.getAllByRole("button", { name: /add webhook/i })[0];
    if (!addButton) throw new Error("add button not found");
    await user.click(addButton);
    const nameInput = screen.getByLabelText("Name");
    const promptInput = screen.getByLabelText("Prompt");
    if (!(nameInput instanceof HTMLInputElement)) throw new Error("name input not found");
    if (!(promptInput instanceof HTMLTextAreaElement)) throw new Error("prompt textarea not found");
    await user.type(nameInput, "CI hook");
    await user.type(promptInput, "Summarize the workflow result.");
    await user.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => {
      expect(apiSpies.create).toHaveBeenCalledWith("agent-1", {
        name: "CI hook",
        prompt: "Summarize the workflow result.",
      });
    });
    expect(await screen.findByText("Webhook created")).toBeInTheDocument();
    // Match the panel's amber warning specifically — the tab behind the
    // dialog renders a similar security hint.
    expect(screen.getByText(/do not share it publicly/)).toBeInTheDocument();
  });

  it("blocks submission when the prompt is blank", async () => {
    const user = userEvent.setup();
    webhooksRef.current = [];
    renderTab();

    const addButton = screen.getAllByRole("button", { name: /add webhook/i })[0];
    if (!addButton) throw new Error("add button not found");
    await user.click(addButton);
    const blankNameInput = screen.getByLabelText("Name");
    if (!(blankNameInput instanceof HTMLInputElement)) throw new Error("name input not found");
    await user.type(blankNameInput, "CI hook");
    expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();
    expect(apiSpies.create).not.toHaveBeenCalled();
  });
});
