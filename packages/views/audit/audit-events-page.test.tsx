// @vitest-environment jsdom

import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { ApiError } from "@multica/core/api";
import enCommon from "../locales/en/common.json";
import enLayout from "../locales/en/layout.json";

const query = vi.hoisted(() => vi.fn());

vi.mock("@tanstack/react-query", () => ({
  useInfiniteQuery: () => query(),
}));
vi.mock("@multica/core/audit", () => ({
  auditEventsPageOptions: () => ({ queryKey: ["audit"] }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-1" }));
vi.mock("@multica/core/paths", () => ({
  paths: { workspace: () => ({ settings: () => "/acme/settings" }) },
  useCurrentWorkspace: () => ({ slug: "acme" }),
}));
vi.mock("../layout/page-header", () => ({
  PageHeader: ({ children }: { children: ReactNode }) => <header>{children}</header>,
}));
vi.mock("../navigation", () => ({ useNavigation: () => ({ push: vi.fn() }) }));
vi.mock("@multica/ui/components/ui/sheet", () => ({
  Sheet: ({ children, open }: { children: ReactNode; open: boolean }) => open ? <div>{children}</div> : null,
  SheetContent: ({ children }: { children: ReactNode }) => <aside>{children}</aside>,
  SheetHeader: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SheetTitle: ({ children }: { children: ReactNode }) => <h2>{children}</h2>,
}));

import { AuditEventsPage } from "./audit-events-page";

const event = {
  id: "event-1", workspace_id: "workspace-1", domain: "mcp", event_type: "mcp.invocation_started",
  occurred_at: "2026-10-18T10:00:00Z", actor_type: "member", actor_id: "member-1", trigger_kind: null,
  trigger_ref: null, issue_id: null, task_id: null, agent_id: null, runtime_id: null, reason: null,
  invocation_id: "invocation-1", sensitive: false,
  details: { reported_client_name: "Desktop", reported_client_version: "1.2", tool_name: "read_file_chunk", target_type: "host", target_id: "host-1", file_path: "/private/secret" },
};

const resources = { en: { common: enCommon, layout: enLayout } };
function Wrapper({ children }: { children: ReactNode }) {
  return <I18nProvider locale="en" resources={resources}>{children}</I18nProvider>;
}

describe("AuditEventsPage", () => {
  beforeEach(() => {
    query.mockReturnValue({ data: { pages: [{ events: [event], next_cursor: null, next_cursor_id: null }] }, isLoading: false, error: null, hasNextPage: false, isFetchingNextPage: false, fetchNextPage: vi.fn(), refetch: vi.fn() });
  });

  it("renders only the approved server-returned detail fields", () => {
    render(<AuditEventsPage />, { wrapper: Wrapper });

    fireEvent.click(screen.getAllByRole("button", { name: /read_file_chunk/i })[0]!);

    expect(screen.getByText("Desktop 1.2")).toBeInTheDocument();
    expect(screen.getAllByText("host: host-1").length).toBeGreaterThan(0);
    expect(screen.queryByText("/private/secret")).not.toBeInTheDocument();
  });

  it("renders permission denial independently from an empty result", () => {
    query.mockReturnValue({ data: undefined, isLoading: false, error: new ApiError("forbidden", 403, "Forbidden"), hasNextPage: false, isFetchingNextPage: false, fetchNextPage: vi.fn(), refetch: vi.fn() });

    render(<AuditEventsPage />, { wrapper: Wrapper });

    expect(screen.getByRole("alert")).toHaveTextContent("You do not have access to audit records");
    expect(screen.queryByText("No audit records yet.")).not.toBeInTheDocument();
  });
});
