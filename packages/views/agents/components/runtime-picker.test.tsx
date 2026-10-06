// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { MemberWithUser, RuntimeDevice } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";
import enIssues from "../../locales/en/issues.json";

// ActorAvatar pulls workspace context this unit test doesn't provide.
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

// Provider logos are inline SVGs with no behavior under test.
vi.mock("../../runtimes/components/provider-logo", () => ({
  ProviderLogo: () => null,
}));

import { RuntimePicker } from "./runtime-picker";

const TEST_RESOURCES = {
  en: { common: enCommon, agents: enAgents, issues: enIssues },
};

const ME = "user-me";

const MEMBERS = [{ user_id: ME, name: "Me", role: "member" }] as unknown as MemberWithUser[];

function makeRuntime(overrides: Partial<RuntimeDevice>): RuntimeDevice {
  return {
    id: "rt",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Claude (host.local)",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "host.local · macOS (arm64)",
    metadata: {},
    owner_id: ME,
    visibility: "private",
    last_seen_at: "2026-07-11T00:00:00Z",
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    ...overrides,
  } as RuntimeDevice;
}

const RUNTIMES = [
  makeRuntime({ id: "rt-a", name: "Claude (a.local)" }),
  makeRuntime({ id: "rt-b", name: "Claude (b.local)", provider: "codex" }),
];

function renderPicker(props: Partial<React.ComponentProps<typeof RuntimePicker>> = {}) {
  const onSelect = vi.fn();
  const utils = render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <RuntimePicker
        runtimes={RUNTIMES}
        members={MEMBERS}
        currentUserId={ME}
        selectedRuntimeId="rt-a"
        onSelect={onSelect}
        {...props}
      />
    </I18nProvider>,
  );
  return { ...utils, onSelect };
}

function trigger(container: HTMLElement): HTMLButtonElement {
  const element = container.querySelector<HTMLButtonElement>('[data-slot="popover-trigger"]');
  if (!element) throw new Error("runtime picker trigger not rendered");
  return element;
}

describe("RuntimePicker (creation studio)", () => {
  beforeEach(() => cleanup());
  afterEach(() => cleanup());

  it("opens the runtime list on click", () => {
    const { container } = renderPicker();
    fireEvent.click(trigger(container));
    expect(trigger(container).getAttribute("aria-expanded")).toBe("true");
  });

  // A builder session rebinds its execution runtime on the server. While that
  // request is in flight the selection cannot be honoured yet, so the picker
  // must refuse to open rather than let the user queue a second, conflicting
  // choice (MUL-5163).
  it("cannot be opened while disabled", () => {
    const { container, onSelect } = renderPicker({ disabled: true });
    expect(trigger(container).disabled).toBe(true);
    fireEvent.click(trigger(container));
    expect(trigger(container).getAttribute("aria-expanded")).not.toBe("true");
    expect(onSelect).not.toHaveBeenCalled();
  });

  // Switching the Mine/All tab re-selects the first usable runtime in the new
  // list, so it is a second path into onSelect. Leaving it live while disabled
  // would let a locked picker start a switch the server then has to refuse.
  it("does not select through the Mine/All filter while disabled", () => {
    const { container, onSelect } = renderPicker({
      runtimes: [
        ...RUNTIMES,
        makeRuntime({ id: "rt-other", name: "Claude (other.local)", owner_id: "user-other", visibility: "public" }),
      ],
      disabled: true,
    });
    const filters = [...container.querySelectorAll("button")].filter(
      (button) => button.getAttribute("data-slot") !== "popover-trigger",
    );
    expect(filters.length).toBeGreaterThan(0);
    for (const button of filters) {
      expect(button.disabled).toBe(true);
      fireEvent.click(button);
    }
    expect(onSelect).not.toHaveBeenCalled();
  });

  // §4.5 (RUYI-425): a disabled instance offers no new bindings — it drops
  // out of the option domain (same semantics as agentSlotChoices), and the
  // exclusion itself never touches the stored selection.
  it("excludes disabled instances from the option domain", () => {
    const disabledRuntime = makeRuntime({
      id: "rt-disabled",
      name: "Gemini (disabled.local)",
      provider: "gemini",
      metadata: { disabled: true },
    });
    const { container, onSelect } = renderPicker({
      runtimes: [...RUNTIMES, disabledRuntime],
    });
    fireEvent.click(trigger(container));

    expect(screen.queryByRole("button", { name: /^Gemini/ })).toBeNull();
    expect(onSelect).not.toHaveBeenCalled();
  });

  // Seeding an empty selection must skip disabled instances — otherwise the
  // create dialog auto-binds an instance that §4.5 refuses to serve.
  it("seeds the initial selection past disabled instances", () => {
    const disabledA = makeRuntime({
      id: "rt-a",
      name: "Claude (a.local)",
      metadata: { disabled: true },
    });
    const enabledB = makeRuntime({
      id: "rt-b",
      name: "Claude (b.local)",
      provider: "codex",
    });
    const { onSelect } = renderPicker({
      runtimes: [disabledA, enabledB],
      selectedRuntimeId: "",
    });

    expect(onSelect).toHaveBeenCalledWith("rt-b");
  });

  // keepRuntimeId semantics (agent-slots.ts): the stored binding stays in
  // the list even when disabled, so the form never lies about current state.
  it("keeps a disabled stored selection visible", () => {
    const disabledA = makeRuntime({
      id: "rt-a",
      name: "Claude (a.local)",
      metadata: { disabled: true },
    });
    const { container, onSelect } = renderPicker({
      runtimes: [
        disabledA,
        makeRuntime({ id: "rt-b", name: "Claude (b.local)", provider: "codex" }),
      ],
      selectedRuntimeId: "rt-a",
    });
    fireEvent.click(trigger(container));

    expect(onSelect).not.toHaveBeenCalled();
    // The disabled binding itself is still listed: trigger label + both rows
    // (disabled kept + enabled peer) = 3 "Claude" buttons; excluding it
    // would drop the count to 2.
    expect(screen.getAllByRole("button", { name: /^Claude/ }).length).toBe(3);
  });
});
