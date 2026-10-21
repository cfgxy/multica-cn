import React from "react";
import { act, fireEvent, render, screen } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { DecisionRequest } from "@multica/core/types";
import { DecisionRequestRow } from "@/components/decision/decision-request-row";

const mockPush = jest.fn();

jest.mock("expo-router", () => ({
  useRouter: () => ({ push: mockPush }),
}));

jest.mock("@/data/api", () => ({
  api: {
    listAgents: jest.fn().mockImplementation(() => new Promise(() => undefined)),
    answerDecisionRequest: jest.fn(),
    cancelDecisionRequest: jest.fn(),
  },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: { currentWorkspaceId: string }) => unknown) =>
    selector({ currentWorkspaceId: "origin-workspace" }),
}));

jest.mock("@multica/core/workspace/hooks", () => ({
  buildActorNameResolver: () => () => "Seed Agent",
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (_key: string, fallback?: string) => fallback ?? _key }),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));

jest.mock("@expo/vector-icons", () => ({ Ionicons: () => null }));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});

jest.mock("@/components/ui/button", () => {
  const { Pressable } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    Button: ({ variant: _variant, size: _size, ...props }: React.ComponentProps<typeof Pressable> & { variant?: string; size?: string }) => (
      <Pressable {...props} />
    ),
  };
});

jest.mock("@/lib/time-ago", () => ({ timeAgo: () => "2h" }));
jest.mock("@/lib/utils", () => ({ cn: (...classes: unknown[]) => classes.filter(Boolean).join(" ") }));

function makeRequest(overrides: Partial<DecisionRequest> = {}): DecisionRequest {
  return {
    id: "request-origin",
    request_group_id: "group-1",
    workspace_id: "origin-workspace",
    role: "origin",
    operable: false,
    status: "pending",
    action_type: "workspace_info_read",
    risk_tier: "read",
    title: "Read workspace info",
    detail: "",
    origin_workspace_id: "origin-workspace",
    origin_agent_id: "agent-1",
    origin_agent_name: "Seed Agent",
    origin_task_id: null,
    origin_issue_id: null,
    origin_issue_title: null,
    operator_tier: "owner",
    named_approver_ids: [],
    approve_label: null,
    deny_label: null,
    expires_at: "2026-10-22T00:00:00Z",
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_source: null,
    executed_at: null,
    execution_error: null,
    created_at: "2026-10-21T00:00:00Z",
    updated_at: "2026-10-21T00:00:00Z",
    ...overrides,
  };
}

async function renderRow(request: DecisionRequest) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await render(
    <QueryClientProvider client={queryClient}>
      <DecisionRequestRow request={request} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  jest.clearAllMocks();
});

describe("DecisionRequestRow target navigation (RUYI-842)", () => {
  it("navigates to the accessible target workspace decisions tab without enabling local approval", async () => {
    await renderRow(makeRequest({ target_workspace_slug: "target-space" }));

    await act(async () => {
      fireEvent.press(screen.getByTestId("decision-request-target-link"));
    });

    expect(mockPush).toHaveBeenCalledWith({
      pathname: "/[workspace]/(tabs)/decisions",
      params: { workspace: "target-space" },
    });
    expect(screen.queryByTestId("decision-request-approve")).toBeNull();
    expect(screen.queryByTestId("decision-request-deny")).toBeNull();
  });

  it("keeps the read-only hint when the response has no safe target route", async () => {
    await renderRow(makeRequest());

    expect(screen.getByText("This row is a read-only projection — act in the target workspace")).toBeTruthy();
    expect(screen.queryByTestId("decision-request-target-link")).toBeNull();
  });
});
