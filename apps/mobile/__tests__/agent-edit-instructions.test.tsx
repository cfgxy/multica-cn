// @ts-nocheck
import React from "react";
import { Alert } from "react-native";
import { fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import AgentInstructionsEditor from "@/app/(app)/[workspace]/more/agents/[id]/edit-instructions";

/**
 * RUYI-624 — the dedicated agent instructions editor (formSheet), mirroring
 * the squad editor (RUYI-541). Contract pinned here:
 *
 *   - the editor seeds once from the agent payload with the FULL text;
 *   - dirty state arms usePreventRemove (unsaved-leaving guard);
 *   - Save writes { instructions } through useUpdateAgent (whose optimistic
 *     cache patch re-paints the detail page behind the sheet) and pops with
 *     the "Instructions saved" feedback;
 *   - readers (non-managers) get the full text read-only and no Save.
 */

const LONG_TEXT =
  "Always start by writing a failing test. ".repeat(40) + "Ship small commits.";

const mockPreventRemove = jest.fn();
const mockBack = jest.fn();
const mockMutate = jest.fn();

jest.mock("@react-navigation/native", () => ({
  usePreventRemove: (...args) => mockPreventRemove(...args),
  useNavigation: () => ({ dispatch: jest.fn() }),
}));

jest.mock("expo-router", () => ({
  router: { back: (...args) => mockBack(...args), push: jest.fn() },
  useLocalSearchParams: () => ({ id: "agent-1", workspace: "ws" }),
  Stack: { Screen: ({ children }) => children },
}));

jest.mock("react-native-keyboard-controller", () => {
  const React = jest.requireActual("react");
  const { View } = jest.requireActual("react-native");
  return {
    KeyboardAvoidingView: (props) => React.createElement(View, props),
  };
});

jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

const mockAgent = {
  id: "agent-1",
  workspace_id: "ws-1",
  name: "Alpha",
  description: "d",
  instructions: LONG_TEXT,
  avatar_url: null,
  runtime_id: "runtime-1",
  // Owner is NOT me — membership role alone decides management, so the
  // reader case below is a genuine non-manager.
  owner_id: "user-2",
  archived_at: null,
};

// Role of the workspace member row for "me" — flipped per test. The agent
// owner is user-2 (not me), so owner/admin grants management, member does
// not — matching the detail page's canManage derivation (MUL-4223).
let mockRole = "owner";

jest.mock("@tanstack/react-query", () => ({
  useQuery: (opts) => {
    if (opts.queryKey[0] === "agents") {
      return { data: mockAgent, isLoading: false };
    }
    if (opts.queryKey[0] === "members") {
      return {
        data: [{ user_id: "user-1", role: mockRole, name: "Alice" }],
        isLoading: false,
      };
    }
    return { data: undefined, isLoading: false };
  },
}));

jest.mock("@/data/queries/agents", () => ({
  agentDetailOptions: (wsId, id) => ({
    queryKey: ["agents", wsId, "detail", id],
  }),
}));

jest.mock("@/data/queries/members", () => ({
  memberListOptions: (wsId) => ({ queryKey: ["members", wsId] }),
}));

jest.mock("@/data/mutations/agents", () => ({
  useUpdateAgent: () => ({ mutate: mockMutate, isPending: false }),
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (sel) => sel({ currentWorkspaceId: "ws-1" }),
}));

jest.mock("@/data/auth-store", () => ({
  useAuthStore: (sel) => sel({ user: { id: "user-1" } }),
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual("react-native");
  return { Text };
});

jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (key, fallback) => fallback ?? key }),
}));

jest.mock("expo-haptics", () => ({
  notificationAsync: jest.fn().mockResolvedValue(undefined),
  NotificationFeedbackType: { Success: "success" },
}));

describe("AgentInstructionsEditor (RUYI-624)", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockMutate.mockImplementation((_patch, config) => {
      config?.onSuccess?.(mockAgent);
    });
    mockRole = "owner";
  });

  it("manager sees the full instructions seeded into the editor", async () => {
    await render(<AgentInstructionsEditor />);
    await waitFor(() =>
      expect(screen.getByDisplayValue(LONG_TEXT)).toBeTruthy(),
    );
  });

  it("dirty draft arms usePreventRemove; clean draft does not", async () => {
    await render(<AgentInstructionsEditor />);
    await waitFor(() =>
      expect(screen.getByDisplayValue(LONG_TEXT)).toBeTruthy(),
    );
    expect(mockPreventRemove).toHaveBeenLastCalledWith(false, expect.any(Function));
    await fireEvent.changeText(
      screen.getByDisplayValue(LONG_TEXT),
      "next instructions",
    );
    expect(mockPreventRemove).toHaveBeenLastCalledWith(true, expect.any(Function));
  });

  it("Save writes instructions through useUpdateAgent, toasts and pops", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await render(<AgentInstructionsEditor />);
    await waitFor(() =>
      expect(screen.getByDisplayValue(LONG_TEXT)).toBeTruthy(),
    );
    await fireEvent.changeText(
      screen.getByDisplayValue(LONG_TEXT),
      "next instructions",
    );
    await fireEvent.press(screen.getByLabelText("Save"));
    expect(mockMutate).toHaveBeenCalledWith(
      { instructions: "next instructions" },
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
    expect(alertSpy).toHaveBeenCalledWith("Instructions saved");
    expect(mockBack).toHaveBeenCalled();
    alertSpy.mockRestore();
  });

  it("a failed save alerts instead of silently popping", async () => {
    mockMutate.mockImplementation((_patch, config) => {
      config?.onError?.(new Error("boom"));
    });
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await render(<AgentInstructionsEditor />);
    await waitFor(() =>
      expect(screen.getByDisplayValue(LONG_TEXT)).toBeTruthy(),
    );
    await fireEvent.changeText(
      screen.getByDisplayValue(LONG_TEXT),
      "next instructions",
    );
    await fireEvent.press(screen.getByLabelText("Save"));
    expect(alertSpy).toHaveBeenCalledWith("Failed to update agent", "boom");
    expect(mockBack).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });

  it("readers get the full text read-only and no Save affordance", async () => {
    mockRole = "member";
    await render(<AgentInstructionsEditor />);
    await waitFor(() => expect(screen.getByText(LONG_TEXT)).toBeTruthy());
    expect(screen.queryByDisplayValue(LONG_TEXT)).toBeNull();
    expect(screen.queryByLabelText("Save")).toBeNull();
  });
});
