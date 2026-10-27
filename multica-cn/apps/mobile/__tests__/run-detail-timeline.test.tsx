// Render tests for RunDetailTimeline's group fold line (RUYI-446 rework):
// the folded group row must carry the whole-group duration at its tail (as
// PC's GroupRow does with its DurationCell), while flat call rows and
// expanded member rows keep their existing tail form (clock offset only —
// never a duration). Views are hand-built fixtures: this component renders
// pre-built views, so these pin the render surface, not the data pipeline.
import { render, screen } from "@testing-library/react-native";
import type { RunCallStepView, RunGroupStepView, RunStepView } from "@/lib/run-detail";
import { RunDetailTimeline } from "@/components/issue/run-detail-timeline";

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});
jest.mock("@/components/ui/collapsible", () => {
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  const passthrough = ({ children }: { children?: React.ReactNode }) => (
    <View>{children}</View>
  );
  return {
    Collapsible: passthrough,
    CollapsibleTrigger: passthrough,
    CollapsibleContent: passthrough,
  };
});
jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key: string, params?: { count?: number }) =>
      key.includes("group_calls") ? `${params?.count} calls` : key,
  }),
}));
jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));
jest.mock("@/lib/theme", () => ({
  THEME: { light: { foreground: "#000" }, dark: { foreground: "#fff" } },
}));
jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));
jest.mock("expo-clipboard", () => ({
  setStringAsync: jest.fn(),
}));

function memberCall(key: string, file: string): RunCallStepView {
  return {
    key,
    kind: "call",
    tool: "Read",
    label: "Read",
    summary: file,
    clockLabel: "+0:01",
    durationLabel: "0.7s",
  };
}

const groupView: RunGroupStepView = {
  key: "1",
  kind: "group",
  label: "Read",
  summary: "file0.ts",
  clockLabel: "+0:00",
  durationLabel: "2.1s",
  steps: [memberCall("1", "file0.ts"), memberCall("3", "file1.ts"), memberCall("5", "file2.ts")],
};

describe("RunDetailTimeline group fold line (RUYI-446)", () => {
  it("renders the whole-group duration at the fold line's tail", async () => {
    await render(<RunDetailTimeline views={[groupView]} />);

    expect(screen.getByText("2.1s")).toBeTruthy();
    expect(screen.getByText("3 calls")).toBeTruthy();
    expect(screen.getByText("+0:00")).toBeTruthy();
  });

  it("keeps flat call rows' tail as the clock offset — no duration", async () => {
    const flat: RunStepView = {
      key: "9",
      kind: "call",
      tool: "Read",
      label: "Read",
      summary: "solo.ts",
      clockLabel: "+0:03",
      durationLabel: "0.5s",
    };
    await render(<RunDetailTimeline views={[flat]} />);

    expect(screen.getByText("+0:03")).toBeTruthy();
    expect(screen.queryByText("0.5s")).toBeNull();
  });

  it("keeps expanded member rows' tail as their clock offset — no member duration", async () => {
    await render(<RunDetailTimeline views={[groupView]} />);

    expect(screen.getByText("file1.ts")).toBeTruthy();
    expect(screen.queryByText("0.7s")).toBeNull();
  });
});
