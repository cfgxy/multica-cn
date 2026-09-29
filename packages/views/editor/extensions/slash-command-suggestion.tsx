"use client";

import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";
import type { QueryClient } from "@tanstack/react-query";
import type { SuggestionOptions } from "@tiptap/suggestion";
import { PluginKey } from "@tiptap/pm/state";
import { useAuthStore } from "@multica/core/auth";
import { useChatStore } from "@multica/core/chat";
import { getCurrentWsId } from "@multica/core/platform";
import { canAssignAgentToIssue } from "@multica/core/permissions";
import { isImeComposing } from "@multica/core/utils";
import { workspaceKeys, skillListOptions } from "@multica/core/workspace/queries";
import type { Agent, MemberWithUser, SkillSummary } from "@multica/core/types";
import { useT } from "../../i18n";
import { ActorAvatar } from "../../common/actor-avatar";
import {
  createSuggestionPopupRender,
} from "./suggestion-popup";
import {
  isPickerAcceptKey,
  pickerNavigationDirection,
} from "../../common/picker-keys";
import { isTriggerArmedAt } from "./suggestion-trigger-arming";

const MAX_ITEMS = 20;

/** Known built-in command ids — the keys under editor `slash_command.commands`. */
export type BuiltinCommandKey = "note";

export interface SlashCommandItem {
  id: string;
  label: string;
  /** Raw description (skill picker). Built-in commands use descriptionKey. */
  description?: string;
  /**
   * For built-in commands: the i18n key under editor `slash_command.commands`.
   * When set, the menu renders the translated copy instead of `description`,
   * so the visible string stays localized (the typed `/label` does not).
   */
  descriptionKey?: BuiltinCommandKey;
  kind?: "skill";
  supportingAgents?: Agent[];
  assignedAgentId?: string | null;
  allAgentsSupport?: boolean;
  /**
   * Chat skill picker only: whether the active chat agent has this skill
   * attached and enabled. `false` renders the row disabled with a reason —
   * the daemon only resolves skills through the agent-skill junction, so a
   * reference to an unattached skill would silently do nothing. Undefined
   * (issue composer) keeps the row selectable as before.
   */
  attached?: boolean;
}

interface SlashCommandListProps {
  items: SlashCommandItem[];
  query: string;
  command: (item: SlashCommandItem) => void;
  /**
   * When true, render nothing instead of an empty-state box when there are no
   * matching items. Used by the built-in command menu in issue comments, where
   * `/` is common in prose (paths, dates) and a popup on every slash would be
   * noise. The chat skill picker leaves this false so it can still explain
   * "no skills configured".
   */
  hideOnEmpty?: boolean;
}

export interface SlashCommandListRef {
  onKeyDown: (props: { event: KeyboardEvent }) => boolean;
}

export const SlashCommandList = forwardRef<
  SlashCommandListRef,
  SlashCommandListProps
>(function SlashCommandList({ items, query, command, hideOnEmpty = false }, ref) {
  const { t } = useT("editor");
  const [selectedIndex, setSelectedIndex] = useState(0);
  const itemRefs = useRef<(HTMLButtonElement | null)[]>([]);

  // Rows the chat picker marked `attached: false` are visible but dead —
  // the daemon resolves skill references through the agent-skill junction,
  // so picking one would insert text that silently does nothing.
  const isRowDisabled = useCallback(
    (item: SlashCommandItem) => item.kind === "skill" && item.attached === false,
    [],
  );
  const selectableIndices = useMemo(
    () =>
      items
        .map((item, index) => (isRowDisabled(item) ? null : index))
        .filter((index): index is number => index !== null),
    [items, isRowDisabled],
  );

  useEffect(() => {
    setSelectedIndex(selectableIndices[0] ?? 0);
  }, [selectableIndices]);

  useEffect(() => {
    itemRefs.current[selectedIndex]?.scrollIntoView({ block: "nearest" });
  }, [selectedIndex]);

  const selectItem = useCallback(
    (index: number) => {
      const item = items[index];
      if (!item || isRowDisabled(item)) return;
      command(item);
    },
    [items, command, isRowDisabled],
  );

  useImperativeHandle(ref, () => ({
    onKeyDown: ({ event }) => {
      if (isImeComposing(event)) return false;
      // Arrow keys plus the Ctrl+N/J/P/K aliases the command bar accepts —
      // see pickerNavigationDirection.
      const direction = pickerNavigationDirection(event);
      if (direction !== null) {
        if (selectableIndices.length === 0) return false;
        const pos = selectableIndices.indexOf(selectedIndex);
        const delta = direction === "next" ? 1 : selectableIndices.length - 1;
        const next = selectableIndices[
          (Math.max(pos, 0) + delta) % selectableIndices.length
        ]!;
        setSelectedIndex(next);
        return true;
      }
      // Enter is the canonical accept; plain Tab is an additive alias (see
      // isPickerAcceptKey). Shift/modifier+Tab fall through to focus nav.
      if (isPickerAcceptKey(event)) {
        if (selectableIndices.length === 0) {
          // Rows are on screen but none selectable (all-unattached library).
          // Swallow the key so a dead Enter never leaks into the composer as
          // a send/newline — arrows stay fall-through for cursor movement.
          return items.length > 0;
        }
        selectItem(selectedIndex);
        return true;
      }
      return false;
    },
  }));

  if (items.length === 0) {
    if (hideOnEmpty) return null;
    return (
      <div className="rounded-md border bg-popover p-2 text-caption text-muted-foreground shadow-md">
        {t(($) =>
          query.trim()
            ? $.slash_command.no_results
            : $.slash_command.no_skills_configured,
        )}
      </div>
    );
  }

  // Built-in commands carry an i18n key so the visible description stays
  // localized; skills carry a raw description string from their config.
  const describe = (item: SlashCommandItem): string | undefined =>
    item.descriptionKey === "note"
      ? t(($) => $.slash_command.commands.note)
      : item.description;

  // Group boundaries: the chat picker partitions skills into attached
  // (selectable) and unattached (disabled) blocks; the issue composer's
  // items carry no attachment data and keep the legacy single "Skills"
  // header; commands trail last.
  const groupKeyOf = (
    item: SlashCommandItem,
  ): "attached" | "unattached" | "skill" | "command" => {
    if (item.kind !== "skill") return "command";
    if (item.attached === undefined) return "skill";
    return item.attached ? "attached" : "unattached";
  };
  const hasSkills = items.some((item) => item.kind === "skill");

  return (
    // Height budget clamps to min(design max, viewport-aware
    // `--suggestion-available-height` from suggestion-popup.tsx's size
    // middleware), falling back to the design max when rendered standalone.
    // Single height authority — mirrors MentionList.
    <div className="rounded-md border bg-popover py-1 shadow-md w-80 max-h-[min(300px,var(--suggestion-available-height,300px))] overflow-y-auto">
      {items.map((item, index) => {
        const description = describe(item);
        const key = groupKeyOf(item);
        const groupStart = index === 0 || groupKeyOf(items[index - 1]!) !== key;
        const disabled = isRowDisabled(item);
        return (
          <div key={item.id}>
          {groupStart && key === "attached" && (
            <div className="px-3 py-1 text-caption text-muted-foreground">{t(($) => $.slash_command.skills_attached_group)}</div>
          )}
          {groupStart && key === "unattached" && (
            <div className="px-3 py-1 text-caption text-muted-foreground">{t(($) => $.slash_command.skills_unattached_group)}</div>
          )}
          {groupStart && key === "skill" && (
            <div className="px-3 py-1 text-caption text-muted-foreground">{t(($) => $.slash_command.skills_group)}</div>
          )}
          {groupStart && key === "command" && hasSkills && (
            <div className="px-3 pt-2 pb-1 text-caption text-muted-foreground">{t(($) => $.slash_command.commands_group)}</div>
          )}
          <button
            ref={(el) => {
              itemRefs.current[index] = el;
            }}
            disabled={disabled}
            aria-disabled={disabled || undefined}
            className={`flex w-full flex-col gap-0.5 px-3 py-1.5 text-left text-caption transition-colors ${
              disabled
                ? "cursor-not-allowed opacity-60"
                : selectedIndex === index
                  ? "bg-accent"
                  : "hover:bg-accent/50"
            }`}
            onClick={() => selectItem(index)}
          >
            <span className="font-medium">/{item.label}</span>
            {description && (
              <span className="truncate text-muted-foreground">
                {description}
              </span>
            )}
            {item.kind === "skill" && disabled && (
              <span className="text-muted-foreground">
                {t(($) => $.slash_command.unattached_reason)}
              </span>
            )}
            {item.kind === "skill" && !disabled && (
              <span
                className="flex items-center gap-1 text-muted-foreground"
                title={item.allAgentsSupport ? t(($) => $.slash_command.all_agents) : undefined}
                aria-label={item.allAgentsSupport ? t(($) => $.slash_command.all_agents) : undefined}
              >
                {(item.supportingAgents?.length ?? 0) === 0 && (
                  <span>{t(($) => $.slash_command.no_agents)}</span>
                )}
                {item.supportingAgents?.slice(0, 3).map((agent) => (
                  <span key={agent.id} title={agent.name} className={agent.id === item.assignedAgentId ? "rounded-full ring-2 ring-primary" : ""}>
                    <ActorAvatar actorType="agent" actorId={agent.id} name={agent.name} avatarUrl={agent.avatar_url} size="xs" profileLink={false} />
                  </span>
                ))}
                {(item.supportingAgents?.length ?? 0) > 3 && <span>+{(item.supportingAgents?.length ?? 0) - 3}</span>}
              </span>
            )}
          </button>
          </div>
        );
      })}
    </div>
  );
});

const NO_MATCH = 4;

/** Returns the match tier: exact name, prefix, substring, then description. */
function skillMatchRank(
  skill: { name: string; description?: string },
  q: string,
): number {
  const name = skill.name.toLowerCase();
  if (name === q) return 0;
  if (name.startsWith(q)) return 1;
  if (name.includes(q)) return 2;
  if ((skill.description ?? "").toLowerCase().includes(q)) return 3;
  return NO_MATCH;
}

/** Ranks matches by relevance while preserving configured order within each tier. */
function rankSkillMatches<T extends { name: string; description?: string }>(
  skills: T[],
  q: string,
): T[] {
  if (!q) return skills;
  return skills
    .map((skill) => ({ skill, rank: skillMatchRank(skill, q) }))
    .filter((entry) => entry.rank !== NO_MATCH)
    .sort((a, b) => a.rank - b.rank)
    .map((entry) => entry.skill);
}

export function buildIssueCommandItems(
  qc: QueryClient,
  query: string,
  skills: Pick<SkillSummary, "id" | "name" | "description">[],
  assignedAgentId: string | null,
  quickActions: { id: string; name: string; description?: string }[] = [],
): SlashCommandItem[] {
  const wsId = getCurrentWsId();
  const agents: Agent[] = wsId ? (qc.getQueryData(workspaceKeys.agents(wsId)) ?? []) : [];
  const activeAgents = agents.filter((agent) => !agent.archived_at);
  const rankedSkills = rankSkillMatches(skills, query.toLowerCase());
  const commandMatches = buildBuiltinCommandItems(query, quickActions);
  const builtin = commandMatches.filter((item) => item.descriptionKey);
  const commands = rankedSkills.length > 0 && commandMatches.length > MAX_ITEMS / 2
    ? [...commandMatches.filter((item) => !item.descriptionKey).slice(0, MAX_ITEMS / 2 - builtin.length), ...builtin]
    : commandMatches;
  const skillItems = rankedSkills
    .slice(0, MAX_ITEMS - commands.length)
    .map((skill): SlashCommandItem => {
      const supportingAgents = activeAgents.filter((agent) =>
        agent.skills?.some((assigned) => assigned.id === skill.id && assigned.enabled !== false),
      );
      return {
        id: skill.id,
        label: skill.name,
        description: skill.description,
        kind: "skill",
        supportingAgents,
        assignedAgentId,
        allAgentsSupport: activeAgents.length > 0 && supportingAgents.length === activeAgents.length,
      };
    });
  return [...skillItems, ...commands];
}

function buildItems(qc: QueryClient, query: string): SlashCommandItem[] {
  const wsId = getCurrentWsId();
  if (!wsId) return [];

  const agents: Agent[] = qc.getQueryData(workspaceKeys.agents(wsId)) ?? [];
  const members: MemberWithUser[] =
    qc.getQueryData(workspaceKeys.members(wsId)) ?? [];
  // Tiptap calls suggestion items outside React render, so direct store reads
  // are intentional here.
  const { selectedAgentId } = useChatStore.getState();
  const userId = useAuthStore.getState().user?.id ?? null;
  const memberRole = members.find((m) => m.user_id === userId)?.role ?? null;

  const availableAgents = agents.filter(
    (a) =>
      !a.archived_at &&
      canAssignAgentToIssue(a, { userId, role: memberRole }).allowed,
  );
  const activeAgent =
    availableAgents.find((a) => a.id === selectedAgentId) ??
    availableAgents[0] ??
    null;
  if (!activeAgent) return [];

  // RUYI-288: the picker shows the whole workspace library, not just the
  // agent's attachments — hiding unattached skills made it read as a curated
  // subset. A cold cache yields no rows for this keystroke; warm it in the
  // background so the next keystroke sees the real list (chat-input also
  // prefetches on mount; a `[]` cache means fetched-and-empty, don't refetch).
  const cachedSkills =
    qc.getQueryData<SkillSummary[]>(workspaceKeys.skills(wsId));
  if (cachedSkills === undefined) {
    void qc.fetchQuery(skillListOptions(wsId)).catch(() => {});
  }
  const skills = cachedSkills ?? [];

  const q = query.toLowerCase();
  const attachedIds = new Set(
    activeAgent.skills
      .filter((s) => s.enabled !== false)
      .map((s) => s.id),
  );
  // Selectable (attached) rows lead; a stable sort preserves rank/library
  // order inside each block, so the cap below can never drop an attached
  // skill in favor of an unattached one at the same rank.
  const ranked = rankSkillMatches(skills, q)
    .map((skill) => ({ skill, attached: attachedIds.has(skill.id) }));
  ranked.sort((a, b) => Number(b.attached) - Number(a.attached));

  return ranked.slice(0, MAX_ITEMS).map(({ skill, attached }) => {
    const supportingAgents = availableAgents.filter((a) =>
      a.skills?.some((s) => s.id === skill.id && s.enabled !== false),
    );
    return {
      id: skill.id,
      label: skill.name,
      description: skill.description ?? "",
      kind: "skill",
      supportingAgents,
      assignedAgentId: activeAgent.id,
      allAgentsSupport:
        availableAgents.length > 0 &&
        supportingAgents.length === availableAgents.length,
      attached,
    };
  });
}

export function createSlashCommandSuggestion(qc: QueryClient): Omit<
  SuggestionOptions<SlashCommandItem>,
  "editor"
> {
  const pluginKey = new PluginKey("slashCommandSuggestion");

  return {
    char: "/",
    pluginKey,
    // Only open over a `/` the user actually typed, so a pasted path
    // (`/usr/local/bin`) never opens the skill picker (MUL-5429).
    shouldShow: ({ editor, range }) => isTriggerArmedAt(editor, range.from),
    items: ({ query }) => buildItems(qc, query),
    command: ({ editor, range, props }) => {
      const nodeAfter = editor.view.state.selection.$to.nodeAfter;
      const overrideSpace = nodeAfter?.text?.startsWith(" ");
      if (overrideSpace) {
        range.to += 1;
      }

      editor
        .chain()
        .focus()
        .insertContentAt(range, [
          {
            type: "slashCommand",
            attrs: {
              id: props.id,
              label: props.label,
              mentionSuggestionChar: "/",
            },
          },
          { type: "text", text: " " },
        ])
        .run();

      window.getSelection()?.collapseToEnd();
    },
    render: createSuggestionPopupRender<SlashCommandItem, SlashCommandItem, SlashCommandListRef, SlashCommandListProps>({
      pluginKey,
      component: SlashCommandList,
      getProps: (props) => ({
        items: props.items,
        query: props.query,
        command: props.command,
      }),
      onKeyDown: (ref, props) => ref?.onKeyDown(props) ?? false,
    }),
  };
}

// ---------------------------------------------------------------------------
// Built-in command menu (issue comments)
// ---------------------------------------------------------------------------

/**
 * Built-in slash commands offered in the issue comment composer. Unlike the
 * chat `/` picker (which lists the active agent's skills), these are a fixed,
 * hand-curated set. Currently only `/note`, which marks a comment as a
 * human-only note that won't trigger the assigned agent — mirrors the backend
 * `noteCommentPrefix` in server/internal/handler/comment.go.
 */
export const BUILTIN_COMMANDS: SlashCommandItem[] = [
  { id: "note", label: "note", descriptionKey: "note" },
];

/** Marks a menu entry as a configured quick action rather than a built-in. */
export const QUICK_ACTION_ITEM_PREFIX = "quick-action:";

export function isQuickActionItem(item: SlashCommandItem): boolean {
  return item.id.startsWith(QUICK_ACTION_ITEM_PREFIX);
}

export function quickActionIdFromItem(item: SlashCommandItem): string {
  return item.id.slice(QUICK_ACTION_ITEM_PREFIX.length);
}

// Match on the command label as a prefix only — the description is for display,
// not search. With a single command this keeps the menu predictable (typing
// `/no` surfaces `note`; an unrelated `/deploy` shows nothing).
export function buildBuiltinCommandItems(
  query: string,
  quickActions: { id: string; name: string; description?: string }[] = [],
): SlashCommandItem[] {
  const q = query.toLowerCase();
  // Quick actions lead: on an issue they are the reason a user reaches for
  // `/`, and `/note` is a rarely-used escape hatch.
  const actionItems: SlashCommandItem[] = quickActions.map((a) => ({
    id: `${QUICK_ACTION_ITEM_PREFIX}${a.id}`,
    label: a.name,
    description: a.description || undefined,
  }));
  const builtin = BUILTIN_COMMANDS.filter((c) => c.label.toLowerCase().startsWith(q));
  return [
    ...actionItems.filter((c) => c.label.toLowerCase().startsWith(q)).slice(0, MAX_ITEMS - builtin.length),
    ...builtin,
  ];
}

export interface BuiltinCommandSuggestionOptions {
  getAssignedAgentId?: () => string | null;
  /**
   * Configured quick actions offered alongside the built-ins. Read lazily on
   * every keystroke so a newly created action shows up without remounting the
   * editor.
   */
  getQuickActions?: () => { id: string; name: string; description?: string }[];
  /**
   * Resolves a quick action to the text it would post. Server-rendered, so the
   * inserted body is byte-identical to what clicking the sidebar button sends.
   * Returning "" (or throwing) must leave the composer untouched rather than
   * inserting a half-rendered prompt.
   */
  renderQuickAction?: (quickActionId: string) => Promise<string>;
  /**
   * Called when renderQuickAction rejects. The extension cannot show UI of its
   * own — a ProseMirror command runs outside React's tree — so the host turns
   * this into a toast. Without it a failed pick is completely silent.
   */
  onRenderError?: (error: unknown) => void;
}

export function createBuiltinCommandSuggestion(
  options: BuiltinCommandSuggestionOptions = {},
  getItems = (query: string) => buildBuiltinCommandItems(query, options.getQuickActions?.() ?? []),
): Omit<SuggestionOptions<SlashCommandItem>, "editor"> {
  const pluginKey = new PluginKey("builtinCommandSuggestion");

  return {
    char: "/",
    pluginKey,
    // Only open over a `/` the user actually typed, so a pasted path
    // (`/usr/local/bin`) never opens the command menu (MUL-5429).
    shouldShow: ({ editor, range }) => isTriggerArmedAt(editor, range.from),
    items: ({ query }) => getItems(query),
    command: ({ editor, range, props }) => {
      if (props.kind === "skill") {
        const nodeAfter = editor.view.state.selection.$to.nodeAfter;
        if (nodeAfter?.text?.startsWith(" ")) range.to += 1;
        editor.chain().focus().insertContentAt(range, [
          { type: "slashCommand", attrs: { id: props.id, label: props.label, mentionSuggestionChar: "/" } },
          { type: "text", text: " " },
        ]).run();
        window.getSelection()?.collapseToEnd();
        return;
      }
      if (isQuickActionItem(props)) {
        const render = options.renderQuickAction;
        if (!render) return;
        const id = quickActionIdFromItem(props);

        // The "/query" text is deliberately left in place while the request
        // is in flight. Deleting first meant a failed or slow render destroyed
        // what the user typed with nothing to show for it, and the insert then
        // landed wherever the caret happened to be by the time it resolved.
        //
        // Snapshot the EXACT text under the range, not just its shape. A
        // prefix check ("does it still start with /") passes when the user
        // rewrote `/review` into `/fix` mid-request, and the stale response
        // would then overwrite the new command.
        const originalText = editor.state.doc.textBetween(range.from, range.to);

        void render(id)
          .then((content) => {
            if (!content) return;
            const withinDoc = range.to <= editor.state.doc.content.size;
            const unchanged =
              withinDoc && editor.state.doc.textBetween(range.from, range.to) === originalText;
            if (!unchanged) {
              // The command was edited, moved, or removed while the request
              // was outstanding. Inserting anywhere now would either clobber
              // the user's newer text or drop the body in an unrelated spot,
              // so this pick is simply abandoned.
              return;
            }
            editor
              .chain()
              .focus()
              // contentType: "markdown" is load-bearing. Without it Tiptap
              // inserts the string as literal TEXT, so the server-rendered
              // `[@Name](mention://agent/…)` never becomes a mention node —
              // it serialises back out with the brackets escaped
              // (`\[@Name\](…)`) and renders as raw markup in the thread.
              .insertContentAt({ from: range.from, to: range.to }, content, {
                contentType: "markdown",
              })
              .run();
            window.getSelection()?.collapseToEnd();
          })
          .catch((error: unknown) => {
            // The command text is still there, so the user can retry or edit
            // it by hand; the host surfaces why nothing was inserted.
            options.onRenderError?.(error);
          });
        return;
      }

      // Insert the plain-text prefix (e.g. "/note ") rather than a rich node,
      // so a menu selection and a hand-typed command are byte-identical and the
      // backend can detect the marker with a simple prefix match. The trailing
      // space terminates the suggestion match so the menu does not re-open.
      editor
        .chain()
        .focus()
        .insertContentAt(range, [{ type: "text", text: `/${props.label} ` }])
        .run();

      window.getSelection()?.collapseToEnd();
    },
    render: createSuggestionPopupRender<SlashCommandItem, SlashCommandItem, SlashCommandListRef, SlashCommandListProps>({
      pluginKey,
      component: SlashCommandList,
      getProps: (props) => ({
        items: props.items,
        query: props.query,
        command: props.command,
        hideOnEmpty: true,
      }),
      onKeyDown: (ref, props) => ref?.onKeyDown(props) ?? false,
    }),
  };
}

export function createIssueCommandSuggestion(
  qc: QueryClient,
  options: BuiltinCommandSuggestionOptions = {},
): Omit<SuggestionOptions<SlashCommandItem>, "editor"> {
  return createBuiltinCommandSuggestion(options, (query) => {
    const wsId = getCurrentWsId();
    const skills: SkillSummary[] = wsId ? (qc.getQueryData(workspaceKeys.skills(wsId)) ?? []) : [];
    return buildIssueCommandItems(qc, query, skills, options.getAssignedAgentId?.() ?? null, options.getQuickActions?.() ?? []);
  });
}
