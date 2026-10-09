// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { LarkInstallation } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";
import { LarkCapabilityPanel } from "./lark-tab";

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-test",
}));

// RUYI-545: the capabilities field is a three-state wire contract with the
// backend — absent (server predates the feature: hide the panel), []
// (never probed: visible "not checked yet" + recheck entry), null (verdict
// read failed server-side: visible error + retry, never a silent blank).
// The pre-fix UI collapsed [] and null into absent, which is what left
// pre-feature production bots with no permission surface at all.
function renderPanel(capabilities: LarkInstallation["capabilities"]) {
  const installation = {
    id: "018f0000-0000-7000-8000-000000000001",
    workspace_id: "018f0000-0000-7000-8000-000000000002",
    agent_id: "018f0000-0000-7000-8000-000000000003",
    app_id: "cli_test",
    bot_open_id: "ou_test",
    installer_user_id: "018f0000-0000-7000-8000-000000000004",
    status: "active",
    region: "feishu",
    installed_at: "2026-10-01T00:00:00Z",
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    capabilities,
  } as LarkInstallation;
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <I18nProvider locale="en" resources={{ en: { common: enCommon, settings: enSettings } }}>
        <LarkCapabilityPanel installation={installation} canManage />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

afterEach(cleanup);

describe("LarkCapabilityPanel wire states", () => {
  it("renders nothing when the server predates the field (capabilities absent)", () => {
    renderPanel(undefined);
    expect(screen.queryByTestId("lark-capability-panel")).not.toBeInTheDocument();
  });

  it("shows the not-checked hint and the recheck entry for a never-probed bot (capabilities [])", () => {
    renderPanel([]);
    expect(screen.getByTestId("lark-capability-panel")).toBeInTheDocument();
    expect(screen.getByText(enSettings.lark.permissions_not_checked)).toBeInTheDocument();
    expect(screen.getByTestId("lark-permissions-recheck")).toBeInTheDocument();
  });

  it("degrades to a visible error and retry hint instead of vanishing when the verdict read failed (capabilities null)", () => {
    renderPanel(null);
    expect(screen.getByTestId("lark-capability-panel")).toBeInTheDocument();
    expect(screen.getByText(enSettings.lark.permissions_read_failed)).toBeInTheDocument();
    expect(screen.getByTestId("lark-permissions-recheck")).toBeInTheDocument();
  });

  it("renders stored verdicts as capability chips", () => {
    renderPanel([
      {
        capability: "receive_messages",
        status: "granted",
        detail: "",
        required_scopes: [],
        checked_at: "2026-10-08T00:00:00Z",
      },
    ]);
    expect(screen.getByTestId("lark-capability-receive_messages")).toBeInTheDocument();
  });
});
