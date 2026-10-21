import React from "react";
import { act, fireEvent, render, screen } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { DecisionRequest } from "@multica/core/types";
import { DecisionRequestRow } from "@/components/decision/decision-request-row";
import * as Clipboard from "expo-clipboard";

const mockPush = jest.fn();

jest.mock("expo-router", () => ({ useRouter: () => ({ push: mockPush }) }));
jest.mock("@/data/api", () => ({
  api: {
    listAgents: jest.fn().mockImplementation(() => new Promise(() => undefined)),
    answerDecisionRequest: jest.fn(),
    cancelDecisionRequest: jest.fn(),
    getDecisionRequest: jest.fn().mockResolvedValue({
      request: { id: "request-script", action_params: { script: "echo reviewed", interpreter: "sh", args: ["-e"], sha256: "a".repeat(64) } },
      steps: [],
    }),
  },
}));
jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: { currentWorkspaceId: string }) => unknown) =>
    selector({ currentWorkspaceId: "origin-workspace" }),
}));
jest.mock("@multica/core/workspace/hooks", () => ({ buildActorNameResolver: () => () => "Seed Agent" }));
jest.mock("@/lib/use-t", () => ({ useT: () => ({ t: (_key: string, fallback?: string) => fallback ?? _key }) }));
jest.mock("@/lib/use-color-scheme", () => ({ useColorScheme: () => ({ colorScheme: "light" }) }));
jest.mock("@expo/vector-icons", () => ({ Ionicons: () => null }));
jest.mock("expo-clipboard", () => ({ setStringAsync: jest.fn().mockResolvedValue(undefined) }));
jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});
jest.mock("@/components/ui/button", () => {
  const { Pressable } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Button: ({ variant: _variant, size: _size, ...props }: React.ComponentProps<typeof Pressable> & { variant?: string; size?: string }) => (
    <Pressable {...props} />
  ) };
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
  await render(<QueryClientProvider client={queryClient}><DecisionRequestRow request={request} /></QueryClientProvider>);
}

beforeEach(() => jest.clearAllMocks());

describe("DecisionRequestRow target navigation (RUYI-842)", () => {
  it("navigates to the target workspace without enabling local approval", async () => {
    await renderRow(makeRequest({ target_workspace_slug: "target-space" }));
    await act(async () => fireEvent.press(screen.getByTestId("decision-request-target-link")));
    expect(mockPush).toHaveBeenCalledWith({ pathname: "/[workspace]/(tabs)/decisions", params: { workspace: "target-space" } });
    expect(screen.queryByTestId("decision-request-approve")).toBeNull();
    expect(screen.queryByTestId("decision-request-deny")).toBeNull();
  });

  it("keeps a read-only hint when no safe target route is available", async () => {
    await renderRow(makeRequest());
    expect(screen.getByText("This row is a read-only projection — act in the target workspace")).toBeTruthy();
    expect(screen.queryByTestId("decision-request-target-link")).toBeNull();
  });
});

describe("DecisionRequestRow script review", () => {
  it("shows a read-only snapshot on demand and copies only its hash", async () => {
    const script = "echo reviewed";
    await renderRow(makeRequest({
      id: "request-script",
      workspace_id: "target-workspace",
      role: "target",
      operable: true,
      status: "executed",
      action_type: "script_review",
      action_params: { sha256: "a".repeat(64) },
      authorization_spec: {
        version: 1,
        kind: "script_review",
        action_type: "script_review",
        scope: { target_workspace_id: "target-workspace" },
        parameter_digest: "b".repeat(64),
        source_workspace_id: "source-workspace",
        target_workspace_id: "target-workspace",
        expires_at: "2026-10-22T00:00:00Z",
        public_summary: "Review script snapshot",
        digest: "c".repeat(64),
      },
      execution_result: { receipt_type: "script_review", status: "reviewed", script_sha256: "a".repeat(64) },
    }));

    expect(screen.getByText("Reviewed")).toBeTruthy();
    expect(screen.queryByText(script)).toBeNull();
    fireEvent.press(screen.getByTestId("script-review-copy-hash"));
    expect(Clipboard.setStringAsync).toHaveBeenCalledWith("a".repeat(64));
    await act(async () => fireEvent.press(screen.getByTestId("script-review-toggle")));
    expect(await screen.findByText(script)).toBeTruthy();
    expect(screen.getByText("sh", { exact: false })).toBeTruthy();
    expect(screen.getByText('["-e"]', { exact: false })).toBeTruthy();
    expect(screen.queryByText(/execute/i)).toBeNull();
  });
});
