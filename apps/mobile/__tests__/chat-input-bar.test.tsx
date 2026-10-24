import { fireEvent, render, screen } from "@testing-library/react-native";
import { ChatInputBar } from "@/components/chat/chat-input-bar";

const mockMentionState = {
  mentions: [] as Array<never>,
  remove: jest.fn(),
  clear: jest.fn(),
  token: null,
};

const mockSkillState = {
  selected: null,
  clear: jest.fn(),
  token: null,
};

jest.mock("@expo/vector-icons", () => ({
  Ionicons: (props: Record<string, unknown>) => {
    const { View } = jest.requireActual<typeof import("react-native")>("react-native");
    return <View {...props} />;
  },
  MaterialCommunityIcons: (props: Record<string, unknown>) => {
    const { View } = jest.requireActual<typeof import("react-native")>("react-native");
    return <View testID="voice-waveform" {...props} />;
  },
}));

jest.mock("expo-router", () => ({
  router: { push: jest.fn() },
}));

jest.mock("expo-haptics", () => ({
  impactAsync: jest.fn(),
  ImpactFeedbackStyle: { Light: "light" },
}));

jest.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: [] }),
}));

jest.mock("@/components/ui/action-sheet", () => ({
  ActionSheetModal: () => null,
  useActionSheet: () => ({ modalProps: {}, show: jest.fn() }),
}));

jest.mock("@/components/issue/attachment-zone", () => ({
  AttachmentZone: () => null,
}));

jest.mock("@/components/chat/plus-panel", () => ({
  PlusPanel: () => null,
}));

jest.mock("@/components/chat/chat-session-model-picker", () => ({
  ChatSessionModelPicker: () => null,
}));

jest.mock("@/components/chat/dictation-bar", () => ({
  DictationBar: () => null,
}));

jest.mock("@/components/editor/use-file-attach", () => ({
  useFileAttach: () => ({
    attachments: [],
    pickAndUploadImages: jest.fn(),
    takeAndUploadPhoto: jest.fn(),
    pickAndUploadFiles: jest.fn(),
    imageSourceModalProps: {},
    removeAttachment: jest.fn(),
    retryAttachment: jest.fn(),
    clearAttachments: jest.fn(),
    restoreAttachments: jest.fn(),
    enqueueAssets: jest.fn(),
    uploading: false,
  }),
}));

jest.mock("@/data/stores/mention-draft-store", () => ({
  useMentionDraftStore: Object.assign(
    <T,>(selector: (state: typeof mockMentionState) => T) => selector(mockMentionState),
    {
      getState: () => ({ ...mockMentionState, setToken: jest.fn(), toggle: jest.fn() }),
    },
  ),
}));

jest.mock("@/data/stores/skill-draft-store", () => ({
  useSkillDraftStore: Object.assign(
    <T,>(selector: (state: typeof mockSkillState) => T) => selector(mockSkillState),
    {
      getState: () => ({ ...mockSkillState, setToken: jest.fn() }),
    },
  ),
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: { currentWorkspaceSlug: string; currentWorkspaceId: string }) => unknown) =>
    selector({ currentWorkspaceSlug: "acme", currentWorkspaceId: "workspace-1" }),
}));

jest.mock("@/data/api", () => ({
  api: { transcribeVoiceAudio: jest.fn() },
}));

jest.mock("@/lib/voice/input-form", () => ({
  resolveInputShape: () => ({ dictation: false, rightSlot: "voice" }),
  voiceGateVerdict: () => "available",
}));

jest.mock("@/lib/voice/use-dictation", () => ({
  useDictation: () => ({
    state: "idle",
    rms: 0,
    entry: "ondevice",
    open: jest.fn(),
    close: jest.fn(),
    press: jest.fn(),
    move: jest.fn(),
    release: jest.fn(),
  }),
}));

jest.mock("@/data/queries/quick-replies", () => ({
  quickReplyListOptions: () => ({}),
}));

jest.mock("@/data/queries/projects", () => ({
  projectListOptions: () => ({}),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));

jest.mock("@/lib/theme", () => ({
  THEME: {
    light: {
      secondary: "#f5f5f5",
      foreground: "#111111",
      mutedForeground: "#666666",
      primaryForeground: "#fafafa",
      brand: "#4263eb",
    },
  },
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (_key: string, fallback: string) => fallback,
  }),
}));

jest.mock("@/components/ui/text", () => ({
  Text: jest.requireActual<typeof import("react-native")>("react-native").View,
}));

describe("ChatInputBar voice action", () => {
  it("uses the send action's primary surface and foreground token", async () => {
    const onOpenVoice = jest.fn();
    await render(
      <ChatInputBar
        value=""
        onChangeText={jest.fn()}
        onSend={jest.fn()}
        onStop={jest.fn()}
        sending={false}
        voiceGate={{ availability: { available: true }, resolving: false }}
        onOpenVoice={onOpenVoice}
      />,
    );

    const voiceButton = screen.getByRole("button", { name: "实时语音对话" });
    expect(voiceButton.props.className).toContain("bg-primary");
    expect(screen.getByTestId("voice-waveform")).toHaveProp("color", "#fafafa");

    fireEvent.press(voiceButton);
    expect(onOpenVoice).toHaveBeenCalledTimes(1);
  });
});
