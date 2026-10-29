import React from "react";
import { Alert } from "react-native";
import { act, fireEvent, render, screen } from "@testing-library/react-native";

const mockAlert = jest.fn();
const mockDeleteRuntime = jest.fn();
const mockUnbindAgentsAndDeleteRuntime = jest.fn();

jest.spyOn(Alert, "alert").mockImplementation(
  ((...args: unknown[]) => mockAlert(...args)) as typeof Alert.alert,
);

jest.mock("expo-router", () => ({
  router: { back: jest.fn() },
  useLocalSearchParams: () => ({ id: "runtime-1" }),
}));

jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

jest.mock("@tanstack/react-query", () => ({
  useQuery: ({ queryKey }: { queryKey: unknown[] }) => {
    if (queryKey[0] === "runtimes") {
      return { data: [mockRuntime], isLoading: false };
    }
    if (queryKey[0] === "members") {
      return { data: [{ user_id: "user-1", role: "owner" }], isLoading: false };
    }
    if (queryKey[0] === "agents") {
      return { data: [], isLoading: false };
    }
    return { data: undefined, isLoading: false };
  },
}));

jest.mock("@/data/queries/runtimes", () => ({
  runtimeListOptions: (wsId: string) => ({ queryKey: ["runtimes", wsId] }),
}));

jest.mock("@/data/queries/members", () => ({
  memberListOptions: (wsId: string) => ({ queryKey: ["members", wsId] }),
}));

jest.mock("@/data/queries/agents", () => ({
  agentListOptions: (wsId: string) => ({ queryKey: ["agents", wsId] }),
}));

jest.mock("@/data/mutations/runtime-management", () => ({
  useUpdateRuntime: () => ({ mutate: jest.fn(), isPending: false }),
  useDeleteRuntime: () => ({ mutate: mockDeleteRuntime, isPending: false }),
  useUnbindAgentsAndDeleteRuntime: () => ({
    mutate: mockUnbindAgentsAndDeleteRuntime,
    isPending: false,
  }),
}));

jest.mock("@/data/auth-store", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: { currentWorkspaceId: string }) => unknown) =>
    selector({ currentWorkspaceId: "workspace-1" }),
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (key: string, fallback: string) => fallback ?? key }),
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});

jest.mock("@/components/ui/button", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    Button: ({ children, ...props }: Record<string, unknown>) =>
      React.createElement(Pressable, { ...props, accessibilityRole: "button" }, children),
  };
});

jest.mock("@/components/ui/text-field", () => ({
  TextField: () => null,
}));

jest.mock("@/components/ui/separator", () => ({
  Separator: () => null,
}));

const mockRuntime = {
  id: "runtime-1",
  workspace_id: "workspace-1",
  daemon_id: "daemon-1",
  name: "Codex (workstation)",
  custom_name: "Release runner",
  runtime_mode: "cli",
  provider: "codex",
  launch_header: "codex",
  status: "online",
  device_info: "",
  metadata: {},
  owner_id: "user-1",
  visibility: "private",
  profile_id: null,
  credential_status: "configured",
  last_seen_at: null,
  created_at: "2026-10-29T00:00:00Z",
  updated_at: "2026-10-29T00:00:00Z",
};

type AlertButton = {
  text?: string;
  style?: string;
  onPress?: () => void;
};

function removalButtons(): AlertButton[] {
  const buttons = mockAlert.mock.calls[0]?.[2];
  if (!Array.isArray(buttons)) throw new Error("Removal confirmation did not provide action buttons");
  return buttons as AlertButton[];
}

import RuntimeNodeDetailPage from "@/app/(app)/[workspace]/more/runtimes/[id]";

describe("RuntimeNodeDetailPage removal confirmation", () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  async function openRemovalConfirmation() {
    await render(<RuntimeNodeDetailPage />);
    await act(async () => {
      fireEvent.press(screen.getByRole("button", { name: "Delete" }));
    });
  }

  it("includes the runtime display label at the start of the removal confirmation", async () => {
    await openRemovalConfirmation();

    expect(mockAlert).toHaveBeenCalledWith(
      "Delete runtime?",
      expect.stringMatching(/^Release runner \(Codex\)\n\nThis action cannot be undone\.$/),
      expect.any(Array),
    );
  });

  it("does not invoke either deletion mutation when the removal is cancelled", async () => {
    await openRemovalConfirmation();

    const cancel = removalButtons().find((button) => button.style === "cancel");
    expect(cancel).toBeDefined();

    await act(async () => {
      cancel?.onPress?.();
    });

    expect(mockDeleteRuntime).not.toHaveBeenCalled();
    expect(mockUnbindAgentsAndDeleteRuntime).not.toHaveBeenCalled();
  });
});
