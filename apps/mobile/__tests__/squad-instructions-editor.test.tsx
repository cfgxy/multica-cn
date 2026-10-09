// @ts-nocheck
import React from "react";
import { Alert } from "react-native";
import { fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import SquadInstructionsEditor from "@/app/(app)/[workspace]/more/squads/[id]/edit-instructions";

/**
 * RUYI-541 — the dedicated squad instructions editor (formSheet). The squad
 * detail page only keeps a truncated preview; full viewing and editing move
 * into this window. Contract pinned here:
 *
 *   - the editor seeds once from the squad payload with the FULL text;
 *   - dirty state drives usePreventRemove (unsaved-leaving guard relocated
 *     from the detail page — RUYI-418 S2 semantics must survive the move);
 *   - Save writes { instructions } through useUpdateSquad (whose optimistic
 *     cache patch re-paints the detail page behind the sheet) and pops with
 *     the existing "Instructions saved" feedback;
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
  useLocalSearchParams: () => ({ id: "sq-1", workspace: "ws" }),
  Stack: { Screen: ({ children }) => children },
}));

jest.mock("react-native-keyboard-controller", () => {
  const React = jest.requireActual("react");
  const { View } = jest.requireActual("react-native");
  return {
    KeyboardAvoidingView: (props) => React.createElement(View, props),
  };
});

const mockSquad = {
  id: "sq-1",
  name: "Alpha Squad",
  description: "detail squad",
  instructions: LONG_TEXT,
  creator_id: "user-2",
  leader_id: "agent-1",
  created_at: "2026-01-01T00:00:00Z",
  archived_at: null,
  member_count: 1,
};

// Role of the workspace member row for "me" — flipped per test. The squad
// creator is user-2 (not me), so owner/admin grants management, member does
// not — matching the detail page's canManage derivation (MUL-4223).
let mockRole = "owner";

jest.mock("@tanstack/react-query", () => ({
  useQuery: (opts) => {
    if (opts.queryKey[0] === "squads") {
      return { data: mockSquad, isLoading: false };
    }
    if (opts.queryKey[0] === "members") {
      return {
        data: [{ user_id: "user-1", role: mockRole }],
        isLoading: false,
      };
    }
    return { data: undefined, isLoading: false };
  },
}));

jest.mock("@/data/queries/squads", () => ({
  squadDetailOptions: (wsId, id) => ({
    queryKey: ["squads", wsId, "detail", id],
    queryFn: jest.fn(),
  }),
}));

jest.mock("@/data/queries/members", () => ({
  memberListOptions: (wsId) => ({ queryKey: ["members", wsId], queryFn: jest.fn() }),
}));

jest.mock("@/data/mutations/squads", () => ({
  useUpdateSquad: () => ({ mutate: mockMutate, isPending: false }),
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

describe("SquadInstructionsEditor", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockMutate.mockImplementation((_patch, config) => {
      config?.onSuccess?.(mockSquad);
    });
    mockRole = "owner";
  });

  it("manager sees the full instructions seeded into the editor", async () => {
    await render(<SquadInstructionsEditor />);
    await waitFor(() =>
      expect(screen.getByDisplayValue(LONG_TEXT)).toBeTruthy(),
    );
  });

  it("dirty draft arms usePreventRemove; clean draft does not", async () => {
    await render(<SquadInstructionsEditor />);
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

  it("Save writes instructions through useUpdateSquad, toasts and pops", async () => {
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await render(<SquadInstructionsEditor />);
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

  it("readers get the full text read-only and no Save affordance", async () => {
    mockRole = "member";
    await render(<SquadInstructionsEditor />);
    await waitFor(() => expect(screen.getByText(LONG_TEXT)).toBeTruthy());
    expect(screen.queryByDisplayValue(LONG_TEXT)).toBeNull();
    expect(screen.queryByLabelText("Save")).toBeNull();
  });
});
