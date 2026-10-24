// @ts-nocheck
import React from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import EditAgentProfile from "@/app/(app)/[workspace]/more/agents/[id]/edit-profile";

const mockMutate = jest.fn();
let mockConcurrency = 4;

jest.mock("expo-router", () => ({
  router: { back: jest.fn() },
  useLocalSearchParams: () => ({ id: "agent-1", workspace: "ws" }),
}));

jest.mock("react-native-keyboard-controller", () => {
  const React = jest.requireActual("react");
  const { View } = jest.requireActual("react-native");
  return { KeyboardAvoidingView: (props) => React.createElement(View, props) };
});

jest.mock("@tanstack/react-query", () => ({
  useQuery: () => ({
    data: {
      id: "agent-1",
      name: "Coder",
      description: "does things",
      instructions: "be nice",
      avatar_url: null,
      runtime_id: "runtime-1",
      model: "gpt-x",
      thinking_level: null,
      service_tier: null,
      max_concurrent_tasks: mockConcurrency,
      session_max_context_tokens: null,
      session_compact_pct: null,
      runtime_config: {},
    },
  }),
}));

jest.mock("@/data/queries/agents", () => ({
  agentDetailOptions: () => ({ queryKey: ["agents", "detail"] }),
}));

jest.mock("@/data/mutations/agents", () => ({
  useUpdateAgent: () => ({ mutate: mockMutate, isPending: false }),
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector) => selector({ currentWorkspaceId: "ws-1" }),
}));

jest.mock("@/components/ui/sheet-header", () => {
  const React = jest.requireActual("react");
  const { View } = jest.requireActual("react-native");
  return {
    SheetHeader: ({ children }) => React.createElement(View, null, children),
  };
});

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual("react-native");
  return { Text };
});

jest.mock("@/components/ui/actor-avatar", () => ({ ActorAvatar: () => null }));
jest.mock("@/components/ui/action-sheet", () => ({ ActionSheetModal: () => null }));
jest.mock("@/lib/avatar", () => ({
  useAvatarUploader: () => ({
    uploading: false,
    showAvatarSheet: jest.fn(),
    modalProps: {},
  }),
}));
jest.mock("@/lib/attachment-url", () => ({
  resolveAttachmentUrl: (url) => url,
}));
jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (_key, fallback, options) =>
      typeof fallback === "string"
        ? fallback.replace(/\{\{(\w+)\}\}/g, (_match, key) =>
            String(options?.[key] ?? ""),
          )
        : _key,
  }),
}));

beforeEach(() => {
  jest.clearAllMocks();
  mockConcurrency = 4;
});

describe("EditAgentProfile concurrency preservation", () => {
  it.each([
    [0, { name: "Updated" }],
    [-1, { name: "Updated", max_concurrent_tasks: 0 }],
  ])(
    "keeps unlimited concurrency %s when saving the profile",
    async (concurrency, patch) => {
      mockConcurrency = concurrency;
      await render(<EditAgentProfile />);
      await waitFor(() =>
        expect(screen.getByDisplayValue("Coder")).toBeTruthy(),
      );

      await fireEvent.changeText(screen.getByDisplayValue("Coder"), "Updated");
      await fireEvent.press(screen.getByLabelText("Save"));

      expect(mockMutate).toHaveBeenCalledWith(patch, expect.anything());
    },
  );
});
