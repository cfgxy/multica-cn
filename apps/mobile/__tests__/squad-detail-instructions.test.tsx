// @ts-nocheck
import React from "react";
import { render, screen, fireEvent } from "@testing-library/react-native";
import SquadDetailScreen from "@/app/(app)/[workspace]/more/squads/[id]";

/**
 * RUYI-541 — the squad detail page keeps only a TRUNCATED instructions
 * preview; full viewing/editing moved to the dedicated `edit-instructions`
 * window (route contract pinned here). Pins:
 *
 *   - the inline full-length instructions editor is gone (no TextInput
 *     seeded with the payload);
 *   - managers get a recognizable edit affordance that pushes the editor
 *     window;
 *   - readers see the truncated preview with NO edit affordance (squad
 *     permission rule: hide, not disable);
 *   - the unsaved-leaving usePreventRemove guard no longer lives on this
 *     screen — it relocated into the editor window.
 */

const LONG_TEXT =
  "Always start by writing a failing test. ".repeat(40) + "Ship small commits.";

const mockRouterPush = jest.fn();
const mockPreventRemove = jest.fn();

jest.mock("expo-router", () => ({
  router: { push: (...args) => mockRouterPush(...args), back: jest.fn() },
  useLocalSearchParams: () => ({ id: "sq-1", workspace: "ws" }),
  Stack: { Screen: ({ children }) => children },
}));

jest.mock("@react-navigation/native", () => ({
  usePreventRemove: (...args) => mockPreventRemove(...args),
  useNavigation: () => ({ dispatch: jest.fn() }),
}));

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

let mockRole = "owner";

jest.mock("@tanstack/react-query", () => ({
  useQuery: (opts) => {
    if (opts.queryKey[0] === "squads" && opts.queryKey[2] === "detail") {
      return { data: mockSquad, isLoading: false };
    }
    if (opts.queryKey[0] === "squads" && opts.queryKey[2] === "members") {
      return {
        data: [{ member_type: "agent", member_id: "agent-1", role: "leader" }],
        isLoading: false,
      };
    }
    if (opts.queryKey[0] === "squads" && opts.queryKey[2] === "member-status") {
      return { data: { members: [] }, isLoading: false };
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
  squadMembersOptions: (wsId, id) => ({
    queryKey: ["squads", wsId, "members", id],
    queryFn: jest.fn(),
  }),
  squadMemberStatusOptions: (wsId, id) => ({
    queryKey: ["squads", wsId, "member-status", id],
    queryFn: jest.fn(),
  }),
}));

jest.mock("@/data/queries/members", () => ({
  memberListOptions: (wsId) => ({ queryKey: ["members", wsId], queryFn: jest.fn() }),
}));

jest.mock("@/data/mutations/squads", () => ({
  useUpdateSquad: () => ({ mutate: jest.fn(), isPending: false }),
  useRemoveSquadMember: () => ({ mutate: jest.fn(), isPending: false }),
  useUpdateSquadMemberRole: () => ({ mutate: jest.fn(), isPending: false }),
}));

jest.mock("@/lib/avatar", () => ({
  useAvatarUploader: () => ({
    uploading: false,
    showAvatarSheet: jest.fn(),
    modalProps: {},
  }),
}));

jest.mock("@/components/ui/action-sheet", () => ({
  ActionSheetModal: () => null,
}));

jest.mock("@/data/use-actor-name", () => ({
  useActorLookup: () => ({ getName: () => "Agent One" }),
}));

jest.mock("@/components/squads/squad-member-row", () => ({
  SquadMemberRow: () => null,
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (sel) =>
    sel({ currentWorkspaceId: "ws-1", currentWorkspaceSlug: "ws" }),
}));

jest.mock("@/data/auth-store", () => ({
  useAuthStore: (sel) => sel({ user: { id: "user-1" } }),
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual("react-native");
  return { Text };
});

jest.mock("@/components/ui/actor-avatar", () => {
  const React = jest.requireActual("react");
  const { View } = jest.requireActual("react-native");
  return { ActorAvatar: (props) => React.createElement(View, props) };
});

jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (key, fallback) => fallback ?? key }),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));

jest.mock("@/lib/theme", () => ({
  THEME: {
    light: { foreground: "#000", mutedForeground: "#666", warning: "#a00" },
    dark: { foreground: "#fff", mutedForeground: "#999", warning: "#f00" },
  },
}));

jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

describe("SquadDetailScreen instructions preview (RUYI-541)", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockRole = "owner";
  });

  it("renders a truncated preview instead of the inline editor (manager)", async () => {
    await render(<SquadDetailScreen />);
    const preview = screen.getByText(LONG_TEXT);
    expect(preview.props.numberOfLines).toBe(6);
    expect(screen.queryByDisplayValue(LONG_TEXT)).toBeNull();
  });

  it("manager tap pushes the dedicated edit-instructions window", async () => {
    await render(<SquadDetailScreen />);
    await fireEvent.press(screen.getByLabelText("Edit instructions"));
    expect(mockRouterPush).toHaveBeenCalledWith({
      pathname: "/[workspace]/more/squads/[id]/edit-instructions",
      params: { workspace: "ws", id: "sq-1" },
    });
  });

  it("relocated unsaved-leaving guard no longer registers on the detail page", async () => {
    await render(<SquadDetailScreen />);
    expect(mockPreventRemove).not.toHaveBeenCalled();
  });

  it("readers see the truncated preview without any edit affordance", async () => {
    mockRole = "member";
    await render(<SquadDetailScreen />);
    expect(screen.getByText(LONG_TEXT).props.numberOfLines).toBe(6);
    // No edit affordance for readers — but the preview still opens the
    // window in its read-only mode so the full text stays reachable.
    expect(screen.queryByLabelText("Edit instructions")).toBeNull();
    await fireEvent.press(screen.getByText(LONG_TEXT));
    expect(mockRouterPush).toHaveBeenCalledWith({
      pathname: "/[workspace]/more/squads/[id]/edit-instructions",
      params: { workspace: "ws", id: "sq-1" },
    });
  });
});
