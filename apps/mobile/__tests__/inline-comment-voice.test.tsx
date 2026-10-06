/**
 * InlineCommentComposer 语音入口（RUYI-474）——Issue 评论框的三态语音链路，
 * 与 RUYI-449 聊天输入框同型：
 *
 *   1. 空草稿 + 绑定 agent → 麦克风占发送按钮位；输入文本 → 换发送箭头；
 *      清空 → 麦克风恢复。
 *   2. 未绑定 agent → 始终发送箭头（纯文本路径不受影响）。
 *   3. 按麦克风挂载 VoiceSessionOverlay（目标 = 本单 agent）；口述定稿
 *      回填进评论草稿，但绝不自动发送 —— 发布必须用户手动确认。
 *   4. 关闭语音会话 → 回到空态麦克风。
 *
 * VoiceSessionOverlay 本体是 RUYI-449 已验收组件，这里 mock 成 props 捕获器，
 * 被测单元是 InlineCommentComposer 的接线（槽位切换 + overlay 挂载 + 回填）。
 * RNTL 14 的 press/changeText 引发的重渲染异步落盘，所有状态断言一律走
 * findBy* / waitFor，不做同步假设。
 */
import React from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react-native";

import { InlineCommentComposer } from "@/components/issue/inline-comment-composer";

const mockMutateAsync = jest.fn();
const mockVoiceOverlayProps = {
  last: undefined as
    | {
        agentId: string | null;
        workspaceSlug: string;
        onClose: () => void;
        onUserTurn?: (text: string) => void;
      }
    | undefined,
};

jest.mock("@/data/mutations/issues", () => ({
  useCreateComment: () => ({ isPending: false, mutateAsync: mockMutateAsync }),
}));

jest.mock("@/data/queries/quick-replies", () => ({
  quickReplyListOptions: () => ({ queryKey: ["quick-replies"] }),
}));

jest.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: [] }),
}));

jest.mock("@/data/workspace-store", () => {
  const { create } = jest.requireActual("zustand");
  return {
    useWorkspaceStore: create(() => ({
      currentWorkspaceId: "ws-1",
      currentWorkspaceSlug: "ws",
    })),
  };
});

// t() 返回 fallback：断言锚定英文兜底文案（与 Web 侧测试同策略）。
jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key: string, ...rest: unknown[]) => {
      const fallback = rest.find((value) => typeof value === "string");
      return typeof fallback === "string" ? fallback : key;
    },
  }),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({
    colorScheme: "light",
    preference: "system",
    setPreference: jest.fn(),
  }),
}));

jest.mock("react-native-keyboard-controller", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    KeyboardStickyView: ({ children }: { children: React.ReactNode }) =>
      React.createElement(View, null, children),
  };
});

jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

jest.mock("expo-router", () => ({
  router: { push: jest.fn(), back: jest.fn() },
}));

jest.mock("expo-haptics", () => ({
  impactAsync: jest.fn().mockResolvedValue(undefined),
  selectionAsync: jest.fn().mockResolvedValue(undefined),
  ImpactFeedbackStyle: { Light: "light", Medium: "medium" },
}));

jest.mock("@/components/ui/action-sheet", () => ({
  useActionSheet: () => ({ showActionSheetWithOptions: jest.fn() }),
  ActionSheetModal: () => null,
}));

jest.mock("@/components/editor/use-file-attach", () => ({
  useFileAttach: () => ({
    attachments: [],
    pickAndUploadImages: jest.fn(),
    pickAndUploadFiles: jest.fn(),
    removeAttachment: jest.fn(),
    retryAttachment: jest.fn(),
    clearAttachments: jest.fn(),
    restoreAttachments: jest.fn(),
    enqueueAssets: jest.fn(),
    uploading: false,
  }),
}));

jest.mock("@/components/issue/attachment-zone", () => ({
  AttachmentZone: () => null,
}));

jest.mock("@/components/ui/text", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Text } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return { Text, TextClassContext: React.createContext(undefined) };
});

jest.mock("@react-navigation/native", () => ({
  useTheme: () => ({ colors: { text: "#000000", primary: "#000000" } }),
  useFocusEffect: jest.fn(),
}));

jest.mock("@/components/voice/voice-session-overlay", () => ({
  VoiceSessionOverlay: (props: {
    agentId: string | null;
    workspaceSlug: string;
    onClose: () => void;
    onUserTurn?: (text: string) => void;
  }) => {
    mockVoiceOverlayProps.last = props;
    return null;
  },
}));

const PILL_LABEL = "Add a comment, @ to mention…";
const PLACEHOLDER = "Add a comment…";
const MIC_LABEL = "Start voice conversation";
const SEND_LABEL = "Send";
// 展开卡的确定性锚点：工具栏上传按钮只在展开卡内渲染。
const TOOLBAR_UPLOAD_LABEL = "Upload image";

async function renderComposer(voiceAgentId?: string | null) {
  return render(
    <InlineCommentComposer issueId="issue-1" voiceAgentId={voiceAgentId} />,
  );
}

/** 评论框初始为收起 pill，点 pill 展开编辑卡后才露出工具栏发送槽。 */
async function expandComposer() {
  fireEvent.press(screen.getByLabelText(PILL_LABEL));
  await screen.findByLabelText(TOOLBAR_UPLOAD_LABEL);
}

async function typeDraft(text: string) {
  fireEvent.changeText(screen.getByPlaceholderText(PLACEHOLDER), text);
}

async function openVoice() {
  fireEvent.press(screen.getByLabelText(MIC_LABEL));
  await waitFor(() => {
    expect(mockVoiceOverlayProps.last?.agentId).toBe("agent-1");
  });
}

beforeEach(() => {
  mockMutateAsync.mockReset();
  mockMutateAsync.mockResolvedValue({ id: "comment-1" });
  mockVoiceOverlayProps.last = undefined;
});

describe("InlineCommentComposer voice three-state slot", () => {
  it("空草稿 + 绑定 agent → 麦克风占发送位", async () => {
    await renderComposer("agent-1");
    await expandComposer();

    expect(screen.getByLabelText(MIC_LABEL)).toBeTruthy();
    expect(screen.queryByLabelText(SEND_LABEL)).toBeNull();
  });

  it("输入文本 → 换成发送箭头", async () => {
    await renderComposer("agent-1");
    await expandComposer();

    await typeDraft("typed text");

    await waitFor(() => {
      expect(screen.queryByLabelText(MIC_LABEL)).toBeNull();
    });
    expect(screen.getByLabelText(SEND_LABEL)).toBeTruthy();
  });

  it("清空草稿 → 麦克风恢复", async () => {
    await renderComposer("agent-1");
    await expandComposer();
    await typeDraft("typed text");
    await waitFor(() => {
      expect(screen.queryByLabelText(MIC_LABEL)).toBeNull();
    });

    await typeDraft("");

    await waitFor(() => {
      expect(screen.queryByLabelText(SEND_LABEL)).toBeNull();
    });
    expect(screen.getByLabelText(MIC_LABEL)).toBeTruthy();
  });

  it("未绑定 agent → 始终发送箭头，纯文本路径不受影响", async () => {
    await renderComposer(null);
    await expandComposer();

    expect(screen.getByLabelText(SEND_LABEL)).toBeTruthy();
    expect(screen.queryByLabelText(MIC_LABEL)).toBeNull();
  });

  it("按麦克风挂载语音会话，目标为本单绑定 agent", async () => {
    await renderComposer("agent-1");
    await expandComposer();

    await openVoice();

    // 会话挂载期间麦克风隐藏（防重入）。
    expect(screen.queryByLabelText(MIC_LABEL)).toBeNull();
  });

  it("口述定稿回填进评论草稿，不自动发送", async () => {
    await renderComposer("agent-1");
    await expandComposer();
    await openVoice();

    await act(async () => {
      mockVoiceOverlayProps.last?.onUserTurn?.("spoken comment")
    });

    expect(await screen.findByDisplayValue("spoken comment")).toBeTruthy();
    expect(mockMutateAsync).not.toHaveBeenCalled();
    // 草稿非空 → 槽位换回发送箭头，发布权在用户手里。
    expect(await screen.findByLabelText(SEND_LABEL)).toBeTruthy();
  });

  it("会话内多段口述按空行追加，互不粘连", async () => {
    await renderComposer("agent-1");
    await expandComposer();
    await openVoice();

    await act(async () => {
      mockVoiceOverlayProps.last?.onUserTurn?.("first turn")
    });
    expect(await screen.findByDisplayValue("first turn")).toBeTruthy();

    await act(async () => {
      mockVoiceOverlayProps.last?.onUserTurn?.("second turn")
    });
    expect(
      await screen.findByDisplayValue("first turn\n\nsecond turn"),
    ).toBeTruthy();
    expect(mockMutateAsync).not.toHaveBeenCalled();
  });

  it("关闭语音会话 → 回到空态麦克风", async () => {
    await renderComposer("agent-1");
    await expandComposer();
    await openVoice();

    await act(async () => {
      mockVoiceOverlayProps.last?.onClose()
    });

    await waitFor(() => {
      expect(mockVoiceOverlayProps.last?.agentId).toBeNull();
    });
    expect(await screen.findByLabelText(MIC_LABEL)).toBeTruthy();
  });
});
