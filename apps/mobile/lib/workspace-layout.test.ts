// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
  isServerSwitching: true,
  useQuery: vi.fn(),
  redirect: vi.fn(),
  setCurrentWorkspace: vi.fn(),
}));

vi.mock("react", () => ({ useEffect: vi.fn() }));
vi.mock("react-native", () => ({ Platform: { OS: "ios" } }));
vi.mock("expo-router", () => ({
  Redirect: state.redirect,
  Stack: () => null,
  useLocalSearchParams: () => ({ workspace: "acme" }),
}));
vi.mock("@react-navigation/native", () => ({ useIsFocused: () => true }));
vi.mock("@tanstack/react-query", () => ({ useQuery: state.useQuery }));
vi.mock("i18next", () => ({ default: { t: vi.fn() } }));
vi.mock("@/data/auth-store", () => ({
  useAuthStore: (selector: (store: { isServerSwitching: boolean }) => unknown) =>
    selector({ isServerSwitching: state.isServerSwitching }),
}));
vi.mock("@/data/queries/workspaces", () => ({
  workspaceListOptions: () => ({ queryKey: ["workspaces"], queryFn: vi.fn() }),
}));
vi.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (
    selector: (store: { setCurrentWorkspace: typeof state.setCurrentWorkspace }) => unknown,
  ) => selector({ setCurrentWorkspace: state.setCurrentWorkspace }),
}));
vi.mock("@/data/realtime/realtime-provider", () => ({ RealtimeProvider: () => null }));
vi.mock("@/data/realtime/use-inbox-realtime", () => ({ useInboxRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-notification-realtime", () => ({ useNotificationRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-issues-realtime", () => ({ useIssuesRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-my-issues-realtime", () => ({ useMyIssuesRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-chat-sessions-realtime", () => ({ useChatSessionsRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-projects-realtime", () => ({ useProjectsRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-pins-realtime", () => ({ usePinsRealtime: vi.fn() }));
vi.mock("@/data/realtime/use-presence-realtime", () => ({ usePresenceRealtime: vi.fn() }));
vi.mock("@/lib/use-workspace-presence-prefetch", () => ({ useWorkspacePresencePrefetch: vi.fn() }));
vi.mock("@/components/ui/modal-close-button", () => ({ ModalCloseButton: () => null }));
vi.mock("@/data/stores/new-issue-draft-store", () => ({ useNewIssueDraftResetOnWorkspaceChange: vi.fn() }));
vi.mock("@/data/stores/new-project-draft-store", () => ({ useNewProjectDraftResetOnWorkspaceChange: vi.fn() }));
vi.mock("@/data/stores/chat-session-picker-store", () => ({ useChatSessionPickerResetOnWorkspaceChange: vi.fn() }));

import WorkspaceLayout from "../app/(app)/[workspace]/_layout";

describe("WorkspaceLayout", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    state.isServerSwitching = true;
    state.useQuery.mockReturnValue({ data: undefined, isLoading: false });
  });

  it("keeps the current route while a failed server restore has no workspace data", () => {
    expect(WorkspaceLayout()).toBeNull();
    expect(state.useQuery).toHaveBeenCalledWith(
      expect.objectContaining({ enabled: false }),
    );
    expect(state.redirect).not.toHaveBeenCalled();
  });
});
