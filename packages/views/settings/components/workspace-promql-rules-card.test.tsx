// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type {
  WorkspacePromQLRule,
  WorkspacePromQLRulePreview,
  WorkspacePromQLRules,
} from "@multica/core/types";
import enSettings from "../../locales/en/settings.json";
import { WorkspacePromQLRulesCard } from "./workspace-promql-rules-card";

const TEST_RESOURCES = { en: { settings: enSettings } };

const m = vi.hoisted(() => ({
  response: null as unknown as WorkspacePromQLRules,
  save: vi.fn(),
  preview: vi.fn(),
}));

vi.mock("@multica/core/workspace", () => ({
  workspacePromQLRulesOptions: (wsId: string) => ({
    queryKey: ["workspace-promql-rules", wsId],
    queryFn: () => Promise.resolve(m.response),
  }),
  useSaveWorkspacePromQLRules: () => ({ mutateAsync: m.save, isPending: false }),
  usePreviewWorkspacePromQLRule: () => ({
    mutateAsync: m.preview,
    isPending: false,
  }),
}));

function ruleFixture(overrides: Partial<WorkspacePromQLRule> = {}): WorkspacePromQLRule {
  return {
    id: "d83e82a2-3109-4a65-b272-c437a52a0f83",
    name: "Task failures",
    enabled: true,
    metric: "failures",
    grouping: "workspace",
    condition: "threshold",
    comparison: ">=",
    threshold: 5,
    recovery_threshold: 3,
    lookback_seconds: 300,
    evaluation_seconds: 30,
    for_seconds: 60,
    no_data_policy: "keep",
    query_error_policy: "resolve",
    state: {
      firing: true,
      last_evaluated_at: "2026-10-24T12:00:00Z",
      values: [{ labels: { workspace_id: "workspace-1" }, value: 7 }],
    },
    ...overrides,
  };
}

function mount(canManage: boolean) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <WorkspacePromQLRulesCard wsId="workspace-1" canManage={canManage} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  m.response = {
    rules: [ruleFixture()],
    prometheus_configured: true,
  };
  m.save.mockReset();
  m.preview.mockReset();
  m.preview.mockResolvedValue({
    query: 'sum(increase(multica_agent_task_failures_total{workspace_id="workspace-1"}[300s]))',
    samples: [{ labels: { workspace_id: "workspace-1" }, value: 7 }],
    matches: true,
  } satisfies WorkspacePromQLRulePreview);
  m.save.mockImplementation(async (rules) => ({
    rules: rules.map((rule: Omit<WorkspacePromQLRule, "state">) => ({
      ...rule,
      state: { firing: false },
    })),
    prometheus_configured: true,
  }));
});

describe("WorkspacePromQLRulesCard", () => {
  it("lets members inspect rule status without edit or preview controls", async () => {
    mount(false);

    expect(await screen.findByText("Firing")).toBeTruthy();
    expect(screen.getByDisplayValue("Task failures").hasAttribute("disabled")).toBe(true);
    expect(screen.queryByRole("button", { name: "Add rule" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Save rules" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Preview current result" })).toBeNull();
  });

  it("previews the server-generated query and current samples", async () => {
    const user = userEvent.setup();
    mount(true);

    await user.click(await screen.findByRole("button", { name: "Preview current result" }));

    expect(await screen.findByText("Current result matches this rule.")).toBeTruthy();
    expect(
      screen.getByText(
        'sum(increase(multica_agent_task_failures_total{workspace_id="workspace-1"}[300s]))',
      ),
    ).toBeTruthy();
    expect(m.preview).toHaveBeenCalledWith(
      expect.objectContaining({
        metric: "failures",
        grouping: "workspace",
        condition: "threshold",
      }),
    );
  });

  it("saves the explicit rule draft after an administrator changes it", async () => {
    const user = userEvent.setup();
    m.response = {
      rules: [
        ruleFixture(),
        ruleFixture({
          id: "4be045a3-3503-497e-8691-7c0ddc49420f",
          name: "Queue pressure",
        }),
      ],
      prometheus_configured: true,
    };
    mount(true);

    const enabled = await screen.findByRole("switch", { name: "Enable rule: Task failures" });
    await user.click(enabled);
    await user.click(screen.getByRole("button", { name: "Save rules" }));

    await waitFor(() => expect(m.save).toHaveBeenCalledTimes(1));
    expect(m.save).toHaveBeenCalledWith([
      expect.objectContaining({ id: "d83e82a2-3109-4a65-b272-c437a52a0f83", enabled: false }),
      expect.objectContaining({ id: "4be045a3-3503-497e-8691-7c0ddc49420f", enabled: true }),
    ]);
  });
});
