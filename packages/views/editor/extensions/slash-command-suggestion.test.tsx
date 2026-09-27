import { act, render } from "@testing-library/react";
import { createRef, type ReactNode } from "react";
import { beforeAll, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { workspaceKeys } from "@multica/core/workspace/queries";
import type { Agent, MemberWithUser } from "@multica/core/types";
import type { QueryClient } from "@tanstack/react-query";
import enEditor from "../../locales/en/editor.json";

const TEST_RESOURCES = {
  en: { editor: enEditor },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

vi.mock("@multica/core/platform", () => ({
  getCurrentWsId: () => "ws-1",
}));

const authState = { user: { id: "u1" } as { id: string } | null };
vi.mock("@multica/core/auth", () => ({
  useAuthStore: { getState: () => authState },
}));

const chatState = { selectedAgentId: "agent-1" as string | null };
vi.mock("@multica/core/chat", () => ({
  useChatStore: { getState: () => chatState },
}));

import {
  SlashCommandList,
  type SlashCommandListRef,
  createSlashCommandSuggestion,
  type SlashCommandItem,
  buildBuiltinCommandItems,
  BUILTIN_COMMANDS,
  createBuiltinCommandSuggestion,
  QUICK_ACTION_ITEM_PREFIX,
  buildIssueCommandItems,
} from "./slash-command-suggestion";
import { createEditorExtensions } from "./index";

function agent(overrides: Partial<Agent>): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "runtime-1",
    name: "Agent",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    visibility: "workspace",
    permission_mode: "public_to",
    invocation_targets: [{ target_type: "workspace", target_id: null }],
    status: "idle",
    max_concurrent_tasks: 1,
    model: "",
    owner_id: null,
    skills: [],
    created_at: "",
    updated_at: "",
    archived_at: null,
    archived_by: null,
    ...overrides,
  };
}

function fakeQc(data: {
  members?: Array<Pick<MemberWithUser, "user_id" | "name" | "role">>;
  agents?: Agent[];
}): QueryClient {
  const map = new Map<string, unknown>();
  map.set(JSON.stringify(workspaceKeys.members("ws-1")), data.members ?? []);
  map.set(JSON.stringify(workspaceKeys.agents("ws-1")), data.agents ?? []);
  return {
    getQueryData: (key: readonly unknown[]) => map.get(JSON.stringify(key)),
  } as unknown as QueryClient;
}

function items(qc: QueryClient, query = ""): SlashCommandItem[] {
  const config = createSlashCommandSuggestion(qc);
  return config.items!({
    query,
    editor: {} as never,
    signal: new AbortController().signal,
  }) as SlashCommandItem[];
}

describe("issue skill and command menu", () => {
  it("keeps slash suggestions disabled when the editor switch is off", () => {
    const disabled = createEditorExtensions({ enableSlashCommands: false, slashCommandMode: "command", queryClient: fakeQc({}) });
    const slash = disabled.find((extension) => extension.name === "slashCommand");
    expect(slash?.options.suggestion.allow({} as never)).toBe(false);
  });
  it("lists workspace skills first with enabled supporting agents, then /note and quick actions", () => {
    const qc = fakeQc({ agents: [
      agent({ id: "a1", skills: [{ id: "s1", name: "Review", description: "Check changes" }] }),
      agent({ id: "a2", skills: [{ id: "s1", name: "Review", description: "Check changes", enabled: false }] }),
    ] });
    const result = buildIssueCommandItems(qc, "", [{ id: "s1", name: "Review", description: "Check changes" }], "a1", [{ id: "q1", name: "fix" }]);
    expect(result.map((item) => item.id)).toEqual(["s1", "quick-action:q1", "note"]);
    expect(result[0]?.supportingAgents?.map((a) => a.id)).toEqual(["a1"]);
    expect(result[0]?.assignedAgentId).toBe("a1");
    expect(buildIssueCommandItems(qc, "fix", [], null, [{ id: "q1", name: "fix" }]).map((item) => item.id)).toEqual(["quick-action:q1"]);
  });

  it("keeps /note available with large skill and quick-action catalogs", () => {
    const qc = fakeQc({ agents: [] });
    const skills = Array.from({ length: 40 }, (_, n) => ({ id: `s${n}`, name: `Skill ${n}`, description: "" }));
    const actions = Array.from({ length: 30 }, (_, n) => ({ id: `q${n}`, name: `Action ${n}` }));
    const result = buildIssueCommandItems(qc, "", skills, null, actions);
    expect(result).toHaveLength(20);
    expect(result[0]?.kind).toBe("skill");
    expect(result.at(-1)?.id).toBe("note");
    expect(buildBuiltinCommandItems("", actions).at(-1)?.id).toBe("note");
  });

  it("distinguishes all active agents from a partial or empty support set", () => {
    const skill = { id: "s1", name: "Review", description: "Inspect changes" };
    const supports = (id: string) => [{ id, name: "Review", description: "" }];
    const qc = fakeQc({ agents: [
      agent({ id: "a1", skills: supports("s1") }),
      agent({ id: "a2", skills: supports("s1") }),
      agent({ id: "a3", archived_at: "2026-01-01", skills: [] }),
    ] });
    const all = buildIssueCommandItems(qc, "", [skill], "a1")[0];
    expect(all?.allAgentsSupport).toBe(true);
    expect(all?.supportingAgents?.map((a) => a.id)).toEqual(["a1", "a2"]);

    const partial = fakeQc({ agents: [
      agent({ id: "a1", skills: supports("s1") }),
      agent({ id: "a2", skills: [] }),
      agent({ id: "a3", skills: [] }),
      agent({ id: "a4", skills: [] }),
      agent({ id: "a5", skills: [] }),
    ] });
    expect(buildIssueCommandItems(partial, "", [skill], "a1")[0]?.allAgentsSupport).toBe(false);
    expect(buildIssueCommandItems(fakeQc({ agents: [] }), "", [skill], null)[0]?.allAgentsSupport).toBe(false);
  });
});

describe("slash command suggestion items", () => {
  it("returns all active agent skills when query is empty", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [
            { id: "s1", name: "deploy", description: "Ship changes" },
            { id: "s2", name: "review", description: "Review code" },
          ],
        }),
      ],
    });

    expect(items(qc).map((i) => i.label)).toEqual(["deploy", "review"]);
  });

  it("filters skills by name case-insensitively", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [
            { id: "s1", name: "Deploy", description: "" },
            { id: "s2", name: "Review", description: "" },
          ],
        }),
      ],
    });

    expect(items(qc, "dep").map((i) => i.id)).toEqual(["s1"]);
  });

  it("filters skills by description", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [
            { id: "s1", name: "deploy", description: "Ship changes" },
            { id: "s2", name: "review", description: "Read a pull request" },
          ],
        }),
      ],
    });

    expect(items(qc, "pull").map((i) => i.id)).toEqual(["s2"]);
  });

  it("ranks name prefix matches above description-only matches", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [
            { id: "s1", name: "grilling", description: "Grill the user about a plan" },
            { id: "s2", name: "prototype", description: "Build a throwaway prototype" },
            { id: "s3", name: "wayfinder", description: "Plan a huge chunk of work" },
          ],
        }),
      ],
    });

    expect(items(qc, "wa").map((i) => i.id)).toEqual(["s3", "s2"]);
  });

  it("ranks an exact name match ahead of a longer prefix match", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [
            { id: "s1", name: "reviewer", description: "" },
            { id: "s2", name: "review", description: "" },
          ],
        }),
      ],
    });

    expect(items(qc, "review").map((i) => i.id)).toEqual(["s2", "s1"]);
  });

  it("ranks a name prefix above a mid-name match", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [
            { id: "s1", name: "pr-review", description: "" },
            { id: "s2", name: "review", description: "" },
          ],
        }),
      ],
    });

    expect(items(qc, "rev").map((i) => i.id)).toEqual(["s2", "s1"]);
  });

  it("keeps the configured skill order within a match tier", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [
            { id: "s1", name: "deploy-web", description: "" },
            { id: "s2", name: "deploy-api", description: "" },
          ],
        }),
      ],
    });

    expect(items(qc, "deploy").map((i) => i.id)).toEqual(["s1", "s2"]);
  });

  it("keeps a name match inside the 20-item cap when description hits fill it", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [
            ...Array.from({ length: 25 }, (_, i) => ({
              id: `d${i}`,
              name: `skill-${i}`,
              description: "Build a throwaway prototype",
            })),
            { id: "s-named", name: "wayfinder", description: "" },
          ],
        }),
      ],
    });

    const result = items(qc, "wa");
    expect(result).toHaveLength(20);
    expect(result[0]?.id).toBe("s-named");
  });

  it("tolerates skills with missing descriptions from cached API data", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [
            { id: "s1", name: "deploy" } as Agent["skills"][number],
          ],
        }),
      ],
    });

    expect(() => items(qc, "dep")).not.toThrow();
    expect(items(qc, "dep")).toEqual([
      { id: "s1", label: "deploy", description: "" },
    ]);
  });

  it("returns empty when the active agent has no skills", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [agent({ id: "agent-1", skills: [] })],
    });

    expect(items(qc)).toEqual([]);
  });

  it("caps results at 20", () => {
    chatState.selectedAgentId = "agent-1";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: Array.from({ length: 25 }, (_, i) => ({
            id: `s${i}`,
            name: `skill-${i}`,
            description: "",
          })),
        }),
      ],
    });

    expect(items(qc)).toHaveLength(20);
  });

  it("falls back to the first available agent when selectedAgentId is stale", () => {
    chatState.selectedAgentId = "missing";
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [
        agent({
          id: "agent-1",
          skills: [{ id: "s1", name: "deploy", description: "" }],
        }),
      ],
    });

    expect(items(qc).map((i) => i.id)).toEqual(["s1"]);
  });

  it("returns empty when no agents exist", () => {
    const qc = fakeQc({
      members: [{ user_id: "u1", name: "Alice", role: "member" }],
      agents: [],
    });

    expect(items(qc)).toEqual([]);
  });

  it("excludes skills from private agents the user cannot access", () => {
    chatState.selectedAgentId = "private-agent";
    const qc = fakeQc({
      members: [
        { user_id: "u1", name: "Alice", role: "member" },
        { user_id: "u2", name: "Bob", role: "member" },
      ],
      agents: [
        agent({
          id: "private-agent",
          visibility: "private",
          permission_mode: "private",
          invocation_targets: [],
          owner_id: "u2",
          skills: [{ id: "private-skill", name: "secret", description: "" }],
        }),
      ],
    });

    expect(items(qc)).toEqual([]);
  });
});

describe("SlashCommandList keyboard handling", () => {
  it("lets Enter and arrow keys fall through when there are no selectable items", () => {
    const ref = createRef<SlashCommandListRef>();

    render(
      <I18nWrapper>
        <SlashCommandList ref={ref} items={[]} query="" command={vi.fn()} />
      </I18nWrapper>,
    );

    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "Enter" }),
      }),
    ).toBe(false);
    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "Enter", metaKey: true }),
      }),
    ).toBe(false);
    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "ArrowUp" }),
      }),
    ).toBe(false);
    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "ArrowDown" }),
      }),
    ).toBe(false);
  });

  it("handles Enter and arrow keys when selectable items exist", () => {
    const ref = createRef<SlashCommandListRef>();
    const command = vi.fn();
    const selectableItems: SlashCommandItem[] = [
      { id: "s1", label: "deploy", description: "Ship changes" },
      { id: "s2", label: "review", description: "Review code" },
    ];

    render(
      <I18nWrapper>
        <SlashCommandList
          ref={ref}
          items={selectableItems}
          query=""
          command={command}
        />
      </I18nWrapper>,
    );

    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "ArrowUp" }),
      }),
    ).toBe(true);
    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "ArrowDown" }),
      }),
    ).toBe(true);
    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "Enter" }),
      }),
    ).toBe(true);
    expect(command).toHaveBeenCalledWith(selectableItems[0]);
  });

  // MUL-5495: same Ctrl aliases the command bar (cmdk) accepts, so the slash
  // picker navigates like every other list in the product.
  it("navigates with Ctrl+N/J and Ctrl+P/K, and leaves the bare letters alone", () => {
    const ref = createRef<SlashCommandListRef>();
    const command = vi.fn();
    const selectableItems: SlashCommandItem[] = [
      { id: "s1", label: "deploy", description: "Ship changes" },
      { id: "s2", label: "review", description: "Review code" },
      { id: "s3", label: "note", description: "Leave a note" },
    ];

    render(
      <I18nWrapper>
        <SlashCommandList
          ref={ref}
          items={selectableItems}
          query=""
          command={command}
        />
      </I18nWrapper>,
    );

    const highlightedLabel = () => {
      const buttons = Array.from(document.querySelectorAll<HTMLButtonElement>("button"));
      return buttons.find((b) => b.classList.contains("bg-accent"))?.textContent ?? "";
    };
    let handled: boolean | undefined;
    const press = (init: KeyboardEventInit) =>
      act(() => {
        handled = ref.current?.onKeyDown({ event: new KeyboardEvent("keydown", init) });
      });

    press({ key: "n", ctrlKey: true });
    expect(handled).toBe(true);
    expect(highlightedLabel()).toContain("/review");

    press({ key: "j", ctrlKey: true });
    expect(highlightedLabel()).toContain("/note");

    press({ key: "p", ctrlKey: true });
    expect(highlightedLabel()).toContain("/review");

    press({ key: "k", ctrlKey: true });
    expect(highlightedLabel()).toContain("/deploy");

    // Bare letters stay query characters — "/note" must remain typeable.
    press({ key: "n" });
    expect(handled).toBe(false);
    expect(highlightedLabel()).toContain("/deploy");

    press({ key: "Enter" });
    expect(command).toHaveBeenCalledWith(selectableItems[0]);
  });

  // MUL-3685: plain Tab accepts the highlighted item like Enter; Shift+Tab and
  // modifier+Tab fall through so reverse focus / OS switching are preserved.
  it("accepts the highlighted item on plain Tab, ignoring Shift/modifier+Tab", () => {
    const ref = createRef<SlashCommandListRef>();
    const command = vi.fn();
    const selectableItems: SlashCommandItem[] = [
      { id: "s1", label: "deploy", description: "Ship changes" },
      { id: "s2", label: "review", description: "Review code" },
    ];

    render(
      <I18nWrapper>
        <SlashCommandList
          ref={ref}
          items={selectableItems}
          query=""
          command={command}
        />
      </I18nWrapper>,
    );

    const press = (init: KeyboardEventInit) =>
      ref.current?.onKeyDown({ event: new KeyboardEvent("keydown", init) });

    expect(press({ key: "Tab", shiftKey: true })).toBe(false);
    expect(press({ key: "Tab", metaKey: true })).toBe(false);
    expect(command).not.toHaveBeenCalled();

    expect(press({ key: "Tab" })).toBe(true);
    expect(command).toHaveBeenCalledWith(selectableItems[0]);
  });

  it("lets Tab fall through when there are no selectable items, like Enter", () => {
    const ref = createRef<SlashCommandListRef>();

    render(
      <I18nWrapper>
        <SlashCommandList ref={ref} items={[]} query="" command={vi.fn()} />
      </I18nWrapper>,
    );

    expect(
      ref.current?.onKeyDown({
        event: new KeyboardEvent("keydown", { key: "Tab" }),
      }),
    ).toBe(false);
  });
});

describe("SlashCommandList empty states", () => {
  it("shows a configured-skills empty state before search text is entered", () => {
    const { getByText } = render(
      <I18nWrapper>
        <SlashCommandList items={[]} query="" command={vi.fn()} />
      </I18nWrapper>,
    );

    expect(getByText("No skills configured")).toBeInTheDocument();
  });

  it("shows a no-results empty state when search text has no matches", () => {
    const { getByText } = render(
      <I18nWrapper>
        <SlashCommandList items={[]} query="deploy" command={vi.fn()} />
      </I18nWrapper>,
    );

    expect(getByText("No matching skills")).toBeInTheDocument();
  });

  it("renders nothing on empty items when hideOnEmpty is set (command menu)", () => {
    const { container } = render(
      <I18nWrapper>
        <SlashCommandList items={[]} query="6" command={vi.fn()} hideOnEmpty />
      </I18nWrapper>,
    );

    // No popup box on a non-matching `/` (e.g. typing a date like 6/8).
    expect(container).toBeEmptyDOMElement();
  });
});

describe("buildBuiltinCommandItems", () => {
  it("returns the full built-in command set for an empty query", () => {
    expect(buildBuiltinCommandItems("")).toEqual(BUILTIN_COMMANDS);
  });

  it("includes /note while the query is a prefix of the label", () => {
    expect(buildBuiltinCommandItems("no").map((c) => c.id)).toEqual(["note"]);
    expect(buildBuiltinCommandItems("NOTE").map((c) => c.id)).toEqual(["note"]);
  });

  it("matches the label as a prefix only — not the description", () => {
    // "agent" appears in the description but is not a label prefix.
    expect(buildBuiltinCommandItems("agent")).toEqual([]);
    // A non-prefix substring of the label does not match either.
    expect(buildBuiltinCommandItems("ote")).toEqual([]);
  });

  it("returns nothing for a query that matches no command", () => {
    expect(buildBuiltinCommandItems("deploy")).toEqual([]);
  });
});

describe("SlashCommandList built-in command rendering", () => {
  it("renders the localized description for a built-in command", () => {
    const { getByText } = render(
      <I18nWrapper>
        <SlashCommandList
          items={buildBuiltinCommandItems("")}
          query=""
          command={vi.fn()}
          hideOnEmpty
        />
      </I18nWrapper>,
    );

    expect(getByText("/note")).toBeInTheDocument();
    expect(
      getByText("Add a note — won't trigger any agents"),
    ).toBeInTheDocument();
  });
});


// Async quick-action rendering in the `/` menu (MUL-5465, review finding #4).
//
// The render request resolves after an arbitrary delay, during which the user
// keeps typing. Three behaviours have to hold, and each one was a real bug at
// some point in this PR:
//   - a rejection must not destroy what the user typed
//   - a success must replace the ORIGINAL command, not wherever the caret is
//   - a command edited mid-flight must be left alone, not overwritten
describe("builtin `/` menu — async quick action rendering", () => {
  // Minimal editor stand-in: enough ProseMirror surface for the command to
  // read the range text and issue its chain.
  function fakeEditor(text: string) {
    const calls: { from: number; to: number; content: string; contentType?: string }[] = [];
    let docText = text;
    const chain = {
      focus: () => chain,
      insertContentAt: (
        range: { from: number; to: number },
        content: string,
        opts?: { contentType?: string },
      ) => {
        calls.push({ from: range.from, to: range.to, content, contentType: opts?.contentType });
        return chain;
      },
      insertContent: (content: string) => {
        calls.push({ from: -1, to: -1, content });
        return chain;
      },
      deleteRange: () => chain,
      run: () => true,
    };
    return {
      calls,
      setText: (next: string) => {
        docText = next;
      },
      editor: {
        chain: () => chain,
        state: {
          doc: {
            get content() {
              return { size: docText.length + 1 };
            },
            textBetween: (from: number, to: number) => docText.slice(from, to),
          },
        },
        view: { state: { selection: { $to: { nodeAfter: null } } } },
      },
    };
  }

  const range = { from: 0, to: 7 };
  const item = { id: `${QUICK_ACTION_ITEM_PREFIX}qa-1`, label: "review" };

  it("leaves the typed command intact and reports the failure when render rejects", async () => {
    const { editor, calls } = fakeEditor("/review");
    const onRenderError = vi.fn();
    const suggestion = createBuiltinCommandSuggestion({
      renderQuickAction: () => Promise.reject(new Error("boom")),
      onRenderError,
    });

    suggestion.command!({ editor, range, props: item } as never);
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(calls).toHaveLength(0);
    expect(onRenderError).toHaveBeenCalledTimes(1);
  });

  it("replaces the original range once a delayed render resolves", async () => {
    const { editor, calls } = fakeEditor("/review");
    let resolve!: (v: string) => void;
    const suggestion = createBuiltinCommandSuggestion({
      renderQuickAction: () => new Promise<string>((r) => { resolve = r; }),
    });

    suggestion.command!({ editor, range, props: item } as never);
    expect(calls).toHaveLength(0); // nothing destroyed while in flight

    await act(async () => {
      resolve("rendered body");
      await Promise.resolve();
      await Promise.resolve();
    });

    // contentType must be "markdown": inserted as a plain string, the
    // server-rendered `[@Name](mention://…)` lands as literal text and
    // serialises back out with escaped brackets, so the mention never becomes
    // a node and renders as raw markup in the thread.
    expect(calls).toEqual([
      { from: 0, to: 7, content: "rendered body", contentType: "markdown" },
    ]);
  });

  it("abandons the insert when the command was edited while the request was open", async () => {
    const { editor, calls, setText } = fakeEditor("/review");
    let resolve!: (v: string) => void;
    const suggestion = createBuiltinCommandSuggestion({
      renderQuickAction: () => new Promise<string>((r) => { resolve = r; }),
    });

    suggestion.command!({ editor, range, props: item } as never);
    // The user rewrites the command; a prefix-only check would still see a
    // leading "/" here and clobber it.
    setText("/fixnow");

    await act(async () => {
      resolve("rendered body");
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(calls).toHaveLength(0);
  });
});
