import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { IssueDecision } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { DecisionCardsSection } from "./decision-cards-section";
import { DecisionTimelineRow } from "./decision-timeline-row";

vi.mock("./decision-card", () => ({
  DecisionCard: ({ decision }: { decision: IssueDecision }) => (
    <div data-testid="decision-card-detail">{decision.question}{decision.context?.why_now}</div>
  ),
}));

function decision(id: string, status: IssueDecision["status"]): IssueDecision {
  return {
    id,
    issue_id: "issue-1",
    source_comment_id: null,
    question: `Question ${id}?`,
    context: { why_now: "Background" },
    options: [{ label: "A" }, { label: "B" }],
    multi_select: false,
    recommended_indices: [],
    status,
    selected_indices: [],
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_comment_id: null,
    created_by_type: "agent",
    created_by_id: "agent-1",
    created_at: "2026-10-21T00:00:00Z",
    updated_at: "2026-10-21T00:00:00Z",
  };
}

describe("decision entries", () => {
  it("keeps the timeline compact and opens the complete card in a dialog", async () => {
    renderWithI18n(<DecisionTimelineRow decision={decision("d-1", "open")} />);

    expect(screen.getByTestId("decision-summary-d-1")).toHaveTextContent("Question d-1?");
    expect(screen.queryByText("Background")).toBeNull();
    fireEvent.click(screen.getByTestId("decision-summary-d-1"));
    expect(await screen.findByTestId("decision-card-detail")).toHaveTextContent("Background");
  });

  it("lists open, answered and cancelled cards in the sidebar entry", async () => {
    renderWithI18n(
      <DecisionCardsSection decisions={[
        decision("open", "open"),
        decision("answered", "answered"),
        decision("cancelled", "cancelled"),
      ]} />,
    );

    fireEvent.click(screen.getByTestId("decision-cards-sidebar-entry"));
    expect(await screen.findByTestId("decision-list-row-open")).toBeInTheDocument();
    expect(screen.getByTestId("decision-list-row-answered")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("decision-list-row-cancelled"));
    expect(await screen.findByTestId("decision-card-detail")).toHaveTextContent("Question cancelled?");
  });

  // RUYI-861 named regression: the DialogContent base emits `sm:max-w-sm`
  // (384px) inside the media block Tailwind v4 writes AFTER the plain
  // `max-w-*` utilities, so a non-important `max-w-2xl` on the caller was
  // shadowed and the card rendered 384px-wide. Pin the width contract —
  // the important breakpoint-scoped width clamped to the viewport gutters —
  // on the timeline entry that owns it.
  it("reads the card at the issue body column width, not the base 384px dialog", async () => {
    renderWithI18n(<DecisionTimelineRow decision={decision("d-1", "open")} />);
    fireEvent.click(screen.getByTestId("decision-summary-d-1"));
    const content = (await screen.findByTestId("decision-card-detail"))
      .closest('[data-slot="dialog-content"]');
    expect(content?.className).toContain("sm:!max-w-[min(56rem,calc(100%_-_2rem))]");
  });
});
