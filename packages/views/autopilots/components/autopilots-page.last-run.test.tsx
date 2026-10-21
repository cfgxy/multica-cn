// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithI18n } from "../../test/i18n";
import type { Autopilot } from "@multica/core/types";
import { LastRunCell } from "./autopilots-page";

const autopilot = (lastRunAt: string | null): Autopilot => ({
  id: "autopilot-1",
  workspace_id: "workspace-1",
  title: "Fixture automation",
  description: null,
  assignee_type: "agent",
  assignee_id: "agent-1",
  status: "active",
  execution_mode: "run_only",
  issue_title_template: null,
  created_by_type: "member",
  created_by_id: "member-1",
  last_run_at: lastRunAt,
  created_at: "2026-10-01T00:00:00Z",
  updated_at: "2026-10-01T00:00:00Z",
});

describe("Autopilot last-run list cell", () => {
  it("renders an existing last_run_at instead of the empty marker", () => {
    renderWithI18n(
      <LastRunCell autopilot={autopilot(new Date(Date.now() - 7_200_000).toISOString())} />,
    );

    expect(screen.queryByText("—")).toBeNull();
    expect(screen.getByText(/\d+h ago/)).toBeTruthy();
  });

  it("keeps the empty marker when the automation has never run", () => {
    renderWithI18n(<LastRunCell autopilot={autopilot(null)} />);

    expect(screen.getByText("—")).toBeTruthy();
  });
});
