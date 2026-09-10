import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import type { Agent } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { AgentDetailInspector } from "./agent-detail-inspector";

vi.mock("@tanstack/react-query", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-query")>()),
  useQuery: () => ({ data: undefined, isSuccess: false }),
}));

vi.mock("../../common/avatar-upload-control", () => ({
  AvatarUploadControl: () => <div data-testid="avatar-upload" />,
}));

vi.mock("./inspector/model-picker", () => ({
  ModelPicker: () => <div data-testid="model-picker" />,
}));

vi.mock("./inspector/runtime-picker", () => ({
  RuntimePicker: () => <div data-testid="runtime-picker" />,
}));

vi.mock("./inspector/thinking-prop-row", () => ({
  ThinkingSettingField: () => <div data-testid="thinking-field" />,
}));

vi.mock("./inspector/service-tier-setting-field", () => ({
  ServiceTierSettingField: () => <div data-testid="service-tier-field" />,
}));

function makeAgent(overrides: Partial<Agent> = {}) {
  return {
    id: "agent-1",
    workspace_id: "workspace-1",
    name: "Lambda",
    description: "Test agent",
    runtime_id: "runtime-1",
    session_max_context_tokens: 400_000,
    session_compact_pct: 80,
    ...overrides,
  } as Agent;
}

function renderInspector(agent: Agent, onUpdate = vi.fn(async () => {})) {
  renderWithI18n(
    <AgentDetailInspector
      agent={agent}
      runtime={null}
      runtimes={[]}
      members={[]}
      currentUserId="user-1"
      canEdit
      onUpdate={onUpdate}
    />,
  );
  return onUpdate;
}

const ceilingLabel = "Context limit";
const percentLabel = "Switch at";

describe("AgentDetailInspector session context gate", () => {
  afterEach(() => {
    cleanup();
  });

  it("renders the saved settings", () => {
    renderInspector(makeAgent());

    expect(screen.getByLabelText(ceilingLabel)).toHaveValue(400_000);
    expect(screen.getByLabelText(percentLabel)).toHaveValue(80);
  });

  it("saves an in-range ceiling", async () => {
    const onUpdate = renderInspector(makeAgent());

    const input = screen.getByLabelText(ceilingLabel);
    fireEvent.change(input, { target: { value: "200000" } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledWith("agent-1", {
        session_max_context_tokens: 200_000,
      });
    });
  });

  // Zero is the documented off switch and sits outside the [min, max] range,
  // so a field that only accepted the range would make the gate impossible to
  // turn off from the UI.
  it("accepts zero as the off switch", async () => {
    const onUpdate = renderInspector(makeAgent());

    const input = screen.getByLabelText(ceilingLabel);
    fireEvent.change(input, { target: { value: "0" } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledWith("agent-1", {
        session_max_context_tokens: 0,
      });
    });
  });

  // Reverting rather than clamping matters: clamping would save a number the
  // operator never typed, and they would leave believing they set something else.
  // The frozen range is 0 or [100,000, 2,000,000]; 99,999 and 2,000,001 are the
  // values one step outside each bound, which an off-by-one would let through.
  it.each([
    ["one below the minimum", "99999"],
    ["one above the maximum", "2000001"],
    ["not an integer", "12.5"],
    ["empty", ""],
  ])("reverts a ceiling that is %s without saving", async (_name, typed) => {
    const onUpdate = renderInspector(makeAgent());

    const input = screen.getByLabelText(ceilingLabel);
    fireEvent.change(input, { target: { value: typed } });
    fireEvent.blur(input);

    expect(onUpdate).not.toHaveBeenCalled();
    await waitFor(() => expect(input).toHaveValue(400_000));
  });

  it.each([
    ["below the minimum", "9"],
    ["above 100", "101"],
  ])("reverts a percentage that is %s without saving", async (_name, typed) => {
    const onUpdate = renderInspector(makeAgent());

    const input = screen.getByLabelText(percentLabel);
    fireEvent.change(input, { target: { value: typed } });
    fireEvent.blur(input);

    expect(onUpdate).not.toHaveBeenCalled();
    await waitFor(() => expect(input).toHaveValue(80));
  });

  it.each([
    ["the minimum", "100000", 100_000],
    ["the maximum", "2000000", 2_000_000],
  ])("saves a ceiling at %s of the frozen range", async (_name, typed, saved) => {
    const onUpdate = renderInspector(makeAgent());

    const input = screen.getByLabelText(ceilingLabel);
    fireEvent.change(input, { target: { value: typed } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledWith("agent-1", {
        session_max_context_tokens: saved,
      });
    });
  });

  // The hint has to show the token count the switch actually happens at, not
  // pct×ceiling: at the smallest legal ceiling and percentage the arithmetic
  // gives 10,000 while the interlock fires at 50,000, and an operator reading
  // the raw product would be tuning against a number the server never uses.
  // The assertions read the interpolated figure, which i18next renders
  // unformatted — the literal "50,000" in the sentence is the copy, not the
  // value, and matching it would pass even with the floor removed.
  it("shows the effective threshold, including the 50,000 floor", () => {
    renderInspector(
      makeAgent({ session_max_context_tokens: 100_000, session_compact_pct: 10 }),
    );

    expect(screen.getByText(/actually at 50000 tokens/)).toBeInTheDocument();
    expect(screen.queryByText(/actually at 10000 tokens/)).toBeNull();
  });

  it("shows the plain arithmetic when it is above the floor", () => {
    renderInspector(makeAgent());

    expect(screen.getByText(/actually at 320000 tokens/)).toBeInTheDocument();
  });

  // With no ceiling there is nothing to take a percentage of, so leaving the
  // field live would invite tuning a threshold that can never fire.
  it("disables the percentage while the gate is off", () => {
    renderInspector(makeAgent({ session_max_context_tokens: 0 }));

    expect(screen.getByLabelText(percentLabel)).toBeDisabled();
    expect(screen.getByLabelText(ceilingLabel)).toBeEnabled();
  });

  // A server predating this feature omits both fields. Rendering the defaults
  // would claim a 400K ceiling is in force when the server enforces nothing.
  it("reports the feature as unsupported when the server omits the fields", () => {
    renderInspector(
      makeAgent({
        session_max_context_tokens: undefined,
        session_compact_pct: undefined,
      }),
    );

    expect(screen.queryByLabelText(percentLabel)).toBeNull();
    expect(
      screen.getByText("This server does not support session context settings yet."),
    ).toBeInTheDocument();
  });
});
