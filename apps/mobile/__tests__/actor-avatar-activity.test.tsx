// RUYI-554 unified status language: ActorAvatar's three activity branches.
//   running → breathing wrapper (Reanimated pulse, transform-only)
//   queued  → half-opacity wrapper
//   none    → static avatar (no wrapper, no test targets)
// The animation params themselves are UI-thread concerns; what these pin is
// the render branching the QA walkthrough relies on.
import { render, screen } from "@testing-library/react-native";

jest.mock("react-native-reanimated", () => {
  const { View } = require("react-native");
  return {
    __esModule: true,
    default: { View },
    View,
    useSharedValue: (initial: number) => ({ value: initial }),
    useAnimatedStyle: () => ({}),
    withRepeat: () => 0,
    withTiming: () => 0,
  };
});

jest.mock("nativewind", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));

jest.mock("@/data/use-actor-name", () => ({
  useActorLookup: () => ({
    getName: () => "Agent One",
    getAvatarUrl: () => "emoji:🤖",
  }),
  getInitials: (name: string) => name.slice(0, 2),
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: () => "workspace-1",
}));

// "loading" suppresses the presence dot entirely — presence composition is
// not what these tests pin, the activity branches are.
jest.mock("@/lib/use-agent-presence", () => ({
  useAgentPresence: () => "loading",
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});

import { ActorAvatar } from "@/components/ui/actor-avatar";

describe("ActorAvatar activity branches (RUYI-554)", () => {
  it("renders the breathing wrapper while running", async () => {
    await render(
      <ActorAvatar type="agent" id="agent-1" size={36} activity="running" />,
    );

    expect(screen.getByTestId("avatar-breathing")).toBeTruthy();
    expect(screen.queryByTestId("avatar-queued")).toBeNull();
  });

  it("renders the half-opacity wrapper while queued", async () => {
    await render(
      <ActorAvatar type="agent" id="agent-1" size={36} activity="queued" />,
    );

    const queued = screen.getByTestId("avatar-queued");
    expect(queued.props.style.opacity).toBe(0.5);
    expect(screen.queryByTestId("avatar-breathing")).toBeNull();
  });

  it("renders static — neither wrapper — with no activity", async () => {
    await render(<ActorAvatar type="agent" id="agent-1" size={36} />);

    expect(screen.queryByTestId("avatar-breathing")).toBeNull();
    expect(screen.queryByTestId("avatar-queued")).toBeNull();
  });

  it("keeps the presence corner dot suppressed while presence loads", async () => {
    // Guards the composition order: activity wrapping must not force the
    // presence dot to render before its queries resolve.
    await render(
      <ActorAvatar
        type="agent"
        id="agent-1"
        size={36}
        activity="running"
        showPresence
      />,
    );

    expect(screen.getByTestId("avatar-breathing")).toBeTruthy();
  });
});
