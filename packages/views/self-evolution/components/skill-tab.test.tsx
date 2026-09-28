// @vitest-environment jsdom

import { it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { SkillTab } from "./skill-tab";

const state = vi.hoisted(() => ({ canRestore: false, tokenSamples: 1 }));

vi.mock("@multica/core/api", () => ({
  api: {
    listSkills: () => Promise.resolve([{
      id: "skill-1", name: "review-helper", description: "Review code", workspace_id: "ws-1",
      config: {}, created_at: "2026-09-28T00:00:00Z", updated_at: "2026-09-28T00:00:00Z",
    }]),
    listSkillVersions: () => Promise.resolve([{
      id: "v1", skill_id: "skill-1", version: 1, name: "review-helper",
      description: "Review code", source: "create", created_at: "2026-09-28T00:00:00Z",
      can_restore: state.canRestore,
    }]),
    getSkillVersion: () => Promise.resolve({
      id: "v1", skill_id: "skill-1", version: 1, name: "review-helper", description: "Review code",
      source: "create", content: "# Review", files: [], config: {}, can_restore: state.canRestore,
      created_at: "2026-09-28T00:00:00Z",
    }),
    getSkillUsage: () => Promise.resolve({ total: 1, last_30_days: 1, assigned_agents: 2, since: "2026-09-28T00:00:00Z",
      versions: [{ version: 1, count: 1, runs: Math.max(2, state.tokenSamples), token_samples: state.tokenSamples, median_total_tokens: 100, retried_runs: 1 }],
      recent: [{ task_id: "task-1", issue_id: "issue-1", version: 1, used_at: "2026-09-28T00:00:00Z" }],
    }),
  },
}));

vi.mock("../../navigation", () => ({
  AppLink: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a>,
}));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ skills: () => "/team/skills", issueDetail: (id: string) => `/team/issues/${id}` }),
}));

it("shows version history and observed invocations without member restore access", async () => {
  state.canRestore = false;
  state.tokenSamples = 1;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, "self-evolution": enSelfEvolution } }}>
        <SkillTab wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(screen.getByRole("heading", { name: "review-helper" })).toBeTruthy());
  expect(await screen.findByText("1 explicit invocation")).toBeTruthy();
  expect(screen.getByText("2 runs, 1 with token data")).toBeTruthy();
  expect(screen.getByText("Insufficient samples")).toBeTruthy();
  expect(screen.queryByText(/100 tokens/)).toBeNull();
  expect(screen.getByRole("link", { name: /issue-1/ }).getAttribute("href")).toBe("/team/issues/issue-1");
  expect(screen.queryByRole("button", { name: /Restore/ })).toBeNull();
});

it("shows measured cost only after five token samples", async () => {
  state.tokenSamples = 5;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, "self-evolution": enSelfEvolution } }}>
        <SkillTab wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
  expect(await screen.findByText("Median 100 tokens")).toBeTruthy();
  expect(screen.queryByText("Insufficient samples")).toBeNull();
});
