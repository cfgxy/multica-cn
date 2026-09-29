// @vitest-environment jsdom

import { it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { SkillTab } from "./skill-tab";

const state = vi.hoisted(() => ({ canRestore: false, tokenSamples: 1, versionCount: 1, measured: true, invoked: true, effectSamples: 6 }));

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
    }, ...(state.versionCount > 1 ? [{
      id: "v2", skill_id: "skill-1", version: 2, name: "review-helper",
      description: "Review code", source: "edit", created_at: "2026-09-29T00:00:00Z",
      can_restore: state.canRestore,
    }] : [])].reverse()),
    getSkillVersion: () => Promise.resolve({
      id: "v1", skill_id: "skill-1", version: 1, name: "review-helper", description: "Review code",
      source: "create", content: "# Review", files: [], config: {}, can_restore: state.canRestore,
      created_at: "2026-09-28T00:00:00Z",
    }),
    getSkillUsage: () => Promise.resolve({ total: state.invoked ? 1 : 0, last_30_days: state.invoked ? 1 : 0, assigned_agents: 2, since: state.invoked ? "2026-09-28T00:00:00Z" : undefined,
      versions: [
        { version: 1, count: state.invoked ? 1 : 0, ...(state.measured ? {
          runs: Math.max(2, state.tokenSamples), token_samples: state.tokenSamples, median_total_tokens: 100, retried_runs: 1,
        } : {}) },
        ...(state.versionCount > 1 && state.invoked ? [{ version: 2, count: 5, runs: 5, token_samples: 5, median_total_tokens: 150, retried_runs: 2 }] : []),
      ],
      recent: state.invoked ? [{ task_id: "task-1", issue_id: "issue-1", version: 1, used_at: "2026-09-28T00:00:00Z" }] : [],
    }),
    getSkillEffect: () => Promise.resolve({
      since: "2026-09-28T00:00:00Z",
      use_group: { runs: state.effectSamples, token_samples: state.effectSamples, median_total_tokens: 200, retried_runs: 1, reviewed_issues: 5, first_pass_issues: 4 },
      control_group: { runs: state.effectSamples, token_samples: state.effectSamples, median_total_tokens: 400, retried_runs: 3, reviewed_issues: 5, first_pass_issues: 2 },
      perplexity: { scored: false },
      version_events: state.versionCount > 1 ? [{
        version: 2, from_version: 1, source: "edit", created_at: "2026-09-29T00:00:00Z",
        before: { runs: 1, token_samples: 1, median_total_tokens: 150, retried_runs: 0, reviewed_issues: 0, first_pass_issues: 0 },
        after: { runs: 1, token_samples: 1, median_total_tokens: 180, retried_runs: 0, reviewed_issues: 0, first_pass_issues: 0 },
        window_days: 30, window_uses: 20, before_mode: "days", after_mode: "days",
      }] : [],
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
  state.versionCount = 1;
  state.measured = true;
  state.invoked = true;
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
  state.versionCount = 1;
  state.measured = true;
  state.invoked = true;
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

it("compares two observed versions only after both sides reach the sample floor", async () => {
  state.versionCount = 2;
  state.tokenSamples = 5;
  state.measured = true;
  state.invoked = true;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, "self-evolution": enSelfEvolution } }}>
        <SkillTab wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
  expect(await screen.findByText("+50 token")).toBeTruthy();
  expect(screen.getByText("+20 %")).toBeTruthy();
  expect(screen.getByText(/not evidence that the skill caused a change/)).toBeTruthy();
});

it("does not infer a direction from an under-sampled version", async () => {
  state.versionCount = 2;
  state.tokenSamples = 1;
  state.measured = true;
  state.invoked = true;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, "self-evolution": enSelfEvolution } }}>
        <SkillTab wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
  await screen.findByText("Version measurements");
  expect(screen.getAllByText("Insufficient samples").length).toBeGreaterThan(0);
  expect(screen.queryByText(/\+50 token/)).toBeNull();
});

it("separates zero invocations from statistics omitted by an older server", async () => {
  state.versionCount = 2;
  state.invoked = false;
  state.measured = false;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, "self-evolution": enSelfEvolution } }}>
        <SkillTab wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(screen.getAllByText("No explicit invocations recorded. Earlier use is unknown.").length).toBeGreaterThan(0));
  expect(screen.getAllByText("Run data not collected").length).toBeGreaterThan(0);
  expect(screen.queryByText(/\+50 token/)).toBeNull();
});

it("shows use-group vs control-group deltas once both sides reach the sample floor", async () => {
  state.versionCount = 1;
  state.tokenSamples = 5;
  state.measured = true;
  state.invoked = true;
  state.effectSamples = 6;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, "self-evolution": enSelfEvolution } }}>
        <SkillTab wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
  expect(await screen.findByText("Use group vs control group")).toBeTruthy();
  expect(screen.getByText("+200 token")).toBeTruthy();
  expect(screen.getByText("+33 %")).toBeTruthy();
  expect(screen.getByText("-40 %")).toBeTruthy();
  expect(screen.getByText("Not scored")).toBeTruthy();
  expect(screen.getByText("Same prompt both sides")).toBeTruthy();
  expect(screen.queryByText("Insufficient samples")).toBeNull();
});

it("keeps the use-vs-control deltas silent when either group is under-sampled", async () => {
  state.versionCount = 1;
  state.tokenSamples = 5;
  state.measured = true;
  state.invoked = true;
  state.effectSamples = 2;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, "self-evolution": enSelfEvolution } }}>
        <SkillTab wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
  expect(await screen.findByText("Use group vs control group")).toBeTruthy();
  expect(screen.getAllByText("Insufficient samples").length).toBe(2);
  expect(screen.queryByText("+200 token")).toBeNull();
  expect(screen.getByText("No version events yet (the first version is not an event).")).toBeTruthy();
});

it("renders the version event timeline with per-side windows", async () => {
  state.versionCount = 2;
  state.tokenSamples = 5;
  state.measured = true;
  state.invoked = true;
  state.effectSamples = 6;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, "self-evolution": enSelfEvolution } }}>
        <SkillTab wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
  expect(await screen.findByText(/v1 → v2/)).toBeTruthy();
  expect(screen.getByText(/Before: 1 runs · Median 150 tokens · 30-day window/)).toBeTruthy();
  expect(screen.getByText(/After: 1 runs · Median 180 tokens · 30-day window/)).toBeTruthy();
});
