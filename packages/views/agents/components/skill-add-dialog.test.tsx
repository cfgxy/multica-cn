// @vitest-environment jsdom

import { it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { toast } from "sonner";
import type { Agent } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";
import { SkillAddDialog } from "./skill-add-dialog";

const state = vi.hoisted(() => ({ importStatus: "completed" }));

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listSkills: () => Promise.resolve([
      {
        id: "skill-patent", name: "patent-drafter", description: "Draft patents", workspace_id: "ws-1",
        config: {}, created_by: "user-1", created_at: "2026-09-28T00:00:00Z", updated_at: "2026-09-28T00:00:00Z",
      },
      {
        id: "skill-review", name: "review-helper", description: "Review code", workspace_id: "ws-1",
        config: {}, created_by: "user-1", created_at: "2026-09-28T00:00:00Z", updated_at: "2026-09-28T00:00:00Z",
      },
    ]),
    listSkillCatalog: () => Promise.resolve([
      {
        kind: "skill", id: "skill-patent", name: "patent-drafter", description: "Draft patents",
        source: "workspace", created_by: "user-1", created_at: "2026-09-28T00:00:00Z", updated_at: "2026-09-28T00:00:00Z",
      },
      {
        kind: "skill", id: "skill-review", name: "review-helper", description: "Review code",
        source: "runtime", runtime_id: "rt-1", created_at: "2026-09-28T00:00:00Z", updated_at: "2026-09-28T00:00:00Z",
      },
      {
        kind: "discovery", name: "deploy-helper", description: "Deploy the stack", source: "runtime",
        runtime_id: "rt-1", key: "deploy-helper", source_path: "/skills/deploy-helper",
        last_seen_at: "2026-09-29T12:00:00Z",
      },
    ]),
    setAgentSkills: vi.fn(() => Promise.resolve({ skills: [] })),
    initiateImportLocalSkill: () => Promise.resolve({ id: "req-1", status: state.importStatus }),
    getImportLocalSkillResult: () => Promise.resolve({ id: "req-1", status: state.importStatus }),
  },
}));

const baseAgent: Agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  runtime_id: "runtime-1",
  name: "Agent",
  description: "",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  visibility: "workspace",
  permission_mode: "public_to",
  invocation_targets: [{ target_type: "workspace", target_id: null }],
  status: "idle",
  max_concurrent_tasks: 1,
  model: "",
  owner_id: "user-1",
  skills: [{ id: "skill-attached", name: "attached-skill", description: "Already attached" }],
  created_at: "2026-06-30T00:00:00Z",
  updated_at: "2026-06-30T00:00:00Z",
  archived_at: null,
  archived_by: null,
};

function renderDialog() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, agents: enAgents } }}>
        <SkillAddDialog agent={baseAgent} open onOpenChange={() => {}} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  state.importStatus = "completed";
});

it("offers every authored workspace skill regardless of origin and explains unimported discoveries", async () => {
  renderDialog();
  expect(await screen.findByRole("button", { name: /patent-drafter/ })).toBeTruthy();
  expect(screen.getByRole("button", { name: /review-helper/ })).toBeTruthy();
  expect(screen.queryByText("attached-skill")).toBeNull();
  expect(screen.getAllByText("Runtime").length).toBeGreaterThanOrEqual(2);
  expect(screen.getByText("Discovered on runtimes")).toBeTruthy();
  expect(screen.getByText(/Importing creates an assignable workspace skill/)).toBeTruthy();
  expect(screen.getByText("deploy-helper")).toBeTruthy();
  expect(screen.getAllByRole("button", { name: "Import" }).length).toBe(1);
});

it("attaches a selected skill on confirm", async () => {
  renderDialog();
  fireEvent.click(await screen.findByRole("button", { name: /review-helper/ }));
  fireEvent.click(screen.getByRole("button", { name: "Add 1 skill" }));
  await waitFor(() => expect(toast.error).not.toHaveBeenCalled());
  const { api } = await import("@multica/core/api");
  expect(api.setAgentSkills).toHaveBeenCalledWith("agent-1", {
    skill_ids: ["skill-attached", "skill-review"],
  });
});

it("imports a discovered skill through the runtime flow and reports success", async () => {
  renderDialog();
  fireEvent.click(await screen.findByRole("button", { name: "Import" }));
  await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Skill imported and now selectable."));
  expect(toast.error).not.toHaveBeenCalled();
});
