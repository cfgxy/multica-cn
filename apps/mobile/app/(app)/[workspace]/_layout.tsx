import { useEffect } from "react";
import type { NativeStackNavigationOptions } from "@react-navigation/native-stack";
import { Platform } from "react-native";
import { Redirect, Stack, useLocalSearchParams } from "expo-router";
import { useIsFocused } from "@react-navigation/native";
import { useQuery } from "@tanstack/react-query";
import i18n from "i18next";
import { useAuthStore } from "@/data/auth-store";
import { workspaceListOptions } from "@/data/queries/workspaces";
import { useWorkspaceStore } from "@/data/workspace-store";
import { RealtimeProvider } from "@/data/realtime/realtime-provider";
import { useInboxRealtime } from "@/data/realtime/use-inbox-realtime";
import { useNotificationRealtime } from "@/data/realtime/use-notification-realtime";
import { useIssuesRealtime } from "@/data/realtime/use-issues-realtime";
import { useMyIssuesRealtime } from "@/data/realtime/use-my-issues-realtime";
import { useChatSessionsRealtime } from "@/data/realtime/use-chat-sessions-realtime";
import { useProjectsRealtime } from "@/data/realtime/use-projects-realtime";
import { usePinsRealtime } from "@/data/realtime/use-pins-realtime";
import { usePresenceRealtime } from "@/data/realtime/use-presence-realtime";
import { useSquadsRealtime } from "@/data/realtime/use-squads-realtime";
import { useDecisionInboxRealtime } from "@/data/realtime/use-decision-inbox-realtime";
import { useWorkspacePresencePrefetch } from "@/lib/use-workspace-presence-prefetch";
import { modalStackedSheetOptions } from "@/lib/modal-stacked-sheet-options";
import { shouldResolveWorkspaceMembership } from "@/lib/workspace-route";
import { ModalCloseButton } from "@/components/ui/modal-close-button";
import { useNewIssueDraftResetOnWorkspaceChange } from "@/data/stores/new-issue-draft-store";
import { useNewProjectDraftResetOnWorkspaceChange } from "@/data/stores/new-project-draft-store";
import { useChatAgentRequestResetOnWorkspaceChange } from "@/data/stores/chat-agent-request-store";

/**
 * Shared Stack.Screen options for every iOS formSheet-presented sheet route.
 *
 * Why these specific values:
 *   - `presentation: "formSheet"` instantiates iOS
 *     UISheetPresentationController — native grabber, stacked-card backdrop,
 *     drag-to-dismiss spring physics, detents.
 *   - `sheetAllowedDetents: [0.6, 0.95]` — explicit numeric detents. The
 *     ergonomic `"fitToContents"` is broken on iOS 26 + Expo 55
 *     (expo/expo#42904 padding inconsistency, expo/expo#42965 zero-size).
 *     Predictable two-snap presentation across every picker-row sheet >
 *     shrink-wrap; this is the right default for sheets that sit next to
 *     other sheets in the same chip row (issue / project AttributeRow) so
 *     the user gets the same gesture regardless of which chip they tap.
 *     Isolated sheets that have no neighbour to be consistent with (e.g.
 *     the workspace `menu` sheet) override this with `"fitToContents"`
 *     to avoid the large blank area below their content.
 *   - `sheetGrabberVisible: true` — surfaces the iOS native drag handle
 *     so users discover the gesture.
 *   - `contentStyle.height: "100%"` — safety net against the same
 *     zero-size class of bugs above; ensures the sheet body fills the
 *     allotted detent.
 *   - `headerShown: false` — every sheet body draws its own header (title
 *     + optional right action). The native Stack header would double up.
 */
const SHEET_OPTIONS: NativeStackNavigationOptions = {
  presentation: "formSheet",
  sheetGrabberVisible: true,
  sheetAllowedDetents: [0.6, 0.95],
  sheetCornerRadius: 20,
  contentStyle: { flex: 1 },
  headerShown: false,
};

/**
 * Cold-start deep-link anchor. Expo Router otherwise treats whatever
 * route resolves the URL as the root of the stack — if the user opens a
 * notification that targets `issue/[id]/picker/status` directly, they
 * land on the formSheet with NO parent under it, no way to go back to
 * the tabs. `anchor: "(tabs)"` tells the router to mount the tab UI as
 * the implicit underlying screen so back/swipe-dismiss returns the user
 * to a sensible base state.
 */
export const unstable_settings = { anchor: "(tabs)" } as const;

/**
 * Mounts every per-feature realtime subscription. Lives inside
 * RealtimeProvider so the WSClient context is available, and stays alive
 * for the whole workspace session — the inbox unread count must keep
 * refreshing even while the user is on an issue page or settings, not
 * just when the inbox tab is foregrounded.
 *
 * Add new realtime feature hooks here as they land (issue, chat, etc).
 */
function RealtimeSubscriptions() {
  useInboxRealtime();
  // RUYI-37: inbox:new → Android status-bar notification (deduped, tap-to-issue).
  useNotificationRealtime();
  useIssuesRealtime();
  useMyIssuesRealtime();
  useChatSessionsRealtime();
  useProjectsRealtime();
  usePinsRealtime();
  // RUYI-346: squad:created/updated/deleted → squad 缓存前缀整体失效
  // （管理侧订阅，低频名册变更；详见 use-squads-realtime.ts）。
  useSquadsRealtime();
  // RUYI-494: decision:updated → 决策中心聚合（含 Tab 角标）失效；
  // 单卡缓存补丁仍在 use-issue-realtime.ts，见 use-decision-inbox-realtime.ts。
  useDecisionInboxRealtime();
  // Presence: warm the three queries up front so avatars don't flash a
  // dotless first render, and listen for daemon/agent/task events to keep
  // the runtime + snapshot caches fresh. See use-presence-realtime.ts for
  // the deliberately-skipped high-frequency events.
  useWorkspacePresencePrefetch();
  usePresenceRealtime();
  return null;
}

/**
 * Workspace context layout. Reads the slug from the URL (the route is the
 * source of truth — see apps/mobile/CLAUDE.md "Behavioral parity"), validates
 * membership against the workspaces list, then syncs id+slug into the
 * Zustand store so ApiClient.fetch can read the slug synchronously when
 * injecting the X-Workspace-Slug header.
 *
 * If the slug doesn't match any workspace the user belongs to, redirect to
 * /select-workspace (covers stale persisted slugs after the user lost
 * membership, deep links to wrong slugs, etc.).
 */
export default function WorkspaceLayout() {
  const { workspace: slug } = useLocalSearchParams<{ workspace: string }>();
  const isServerSwitching = useAuthStore((s) => s.isServerSwitching);
  const shouldResolveMembership = shouldResolveWorkspaceMembership(
    isServerSwitching,
  );
  const { data: workspaces, isLoading } = useQuery({
    ...workspaceListOptions(),
    enabled: shouldResolveMembership,
  });
  const setCurrentWorkspace = useWorkspaceStore((s) => s.setCurrentWorkspace);
  const isFocused = useIsFocused();

  const matched = workspaces?.find((w) => w.slug === slug);

  useEffect(() => {
    if (matched && isFocused) {
      setCurrentWorkspace(matched.id, matched.slug);
    }
  }, [isFocused, matched, setCurrentWorkspace]);

  // Wipe cross-route Zustand draft stores whenever the active workspace
  // changes — a draft picked under workspace A (assignee id, draft
  // session id, etc.) is invalid in workspace B and must not leak.
  useNewIssueDraftResetOnWorkspaceChange(matched?.id ?? null);
  useNewProjectDraftResetOnWorkspaceChange(matched?.id ?? null);
  useChatAgentRequestResetOnWorkspaceChange(matched?.id ?? null);

  // Wait for the workspaces list before deciding membership — otherwise a
  // valid deep link would briefly redirect away on cold start.
  if (!shouldResolveMembership || isLoading) return null;

  if (!matched) return <Redirect href="/select-workspace" />;

  // Tabs hide their own header; pushed screens (issue/[id]) get a native
  // iOS Stack header with the standard back button + swipe-to-dismiss.
  return (
    <RealtimeProvider>
      <RealtimeSubscriptions />
      <Stack>
        <Stack.Screen name="(tabs)" options={{ headerShown: false }} />
        <Stack.Screen
          name="issue/[id]"
          options={{
            // 单条任务详情，用单数实体名 `layout:tab.issue`（en "Issue"）;
            // `layout:nav.issues` 是列表页导航项，复数，语义不匹配。
            title: i18n.t("layout:tab.issue", "Issue"),
            headerBackTitle: "Back",
          }}
        />
        <Stack.Screen
          name="project/[id]"
          options={{
            title: i18n.t("layout:tab.project", "Project"),
            headerBackTitle: "Back",
          }}
        />
        <Stack.Screen
          name="project/[id]/edit"
          options={{
            // 编辑现有项目，不能复用 `create_project.title`（渲染成「新建项目」）。
            title: i18n.t("modals:edit_project.title", "Edit Project"),
            presentation: "modal",
            headerLeft: () => <ModalCloseButton />,
          }}
        />
        <Stack.Screen
          name="issue/[id]/edit"
          options={{
            // 同上：编辑现有任务不能复用 `create_issue.sr_manual`（「新建任务」）。
            title: i18n.t("modals:edit_issue.title", "Edit Issue"),
            presentation: "modal",
            headerLeft: () => <ModalCloseButton />,
          }}
        />
        <Stack.Screen
          name="project/new"
          options={{
            title: i18n.t("modals:create_project.title", "New Project"),
            presentation: "modal",
            headerLeft: () => <ModalCloseButton />,
          }}
        />
        <Stack.Screen name="inbox/[id]" options={SHEET_OPTIONS} />
        {/* Archived inbox sub-view (RUYI-532) — pushed from the entry at the
            bottom of the main list, native header like issue/[id]. Title uses
            the shared `list.archived_title` key web's archived view renders. */}
        <Stack.Screen
          name="inbox/archived"
          options={{
            title: i18n.t("inbox:list.archived_title", "Archived"),
            headerBackTitle: "Back",
          }}
        />
        {/* Issue-detail formSheet pickers. All share the same sheet config:
            explicit numeric detents to dodge expo/expo#42904+#42965 (the
            `fitToContents` zero-size / padding bugs on iOS 26 + Expo 55),
            iOS native grabber, and contentStyle.height=100% as a safety
            net against the same zero-size class of bugs. */}
        <Stack.Screen
          name="issue/[id]/picker/status"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen
          name="issue/[id]/picker/priority"
          options={SHEET_OPTIONS}
        />
        {/* Experiment: assignee uses iOS-native nav header + UISearchController
            instead of the body-rendered header pattern in SHEET_OPTIONS.
            Eliminates the #3634 overlap class of bugs and the focus-loss
            footgun of a custom TextInput inside ListHeaderComponent. The
            route file wires `headerSearchBarOptions` via setOptions. If this
            proves out, propagate to label / project / other search pickers
            and update CLAUDE.md Lesson 6 with a carve-out. */}
        <Stack.Screen
          name="issue/[id]/picker/assignee"
          options={{
            ...SHEET_OPTIONS,
            headerShown: true,
            title: i18n.t("issues:actions.assignee", "Assignee"),
          }}
        />
        {/* 与上面 assignee 同构：`SHEET_OPTIONS.headerShown` 为 false 时
            native header 不存在，`headerSearchBarOptions` 无处挂载
            （`ScreenStackFragment` 要先有 header 才 attach searchView），
            搜索框在 iOS / Android 上都不渲染 —— `issues:mobile.picker.
            create_label`（批次 9 由 JSX 硬编码转成的 key）因此从未被渲染过。
            标题用既有单数实体名 `issues:filters.section_label`（en "Label"）。 */}
        <Stack.Screen
          name="issue/[id]/picker/label"
          options={{
            ...SHEET_OPTIONS,
            headerShown: true,
            title: i18n.t("issues:filters.section_label", "Label"),
          }}
        />
        <Stack.Screen
          name="mention-picker"
          options={{
            ...SHEET_OPTIONS,
            headerShown: true,
            title: i18n.t("issues:mobile.mention.screen_title", "Mention"),
          }}
        />
        <Stack.Screen
          name="skill-picker"
          options={{
            ...SHEET_OPTIONS,
            headerShown: true,
            title: i18n.t("issues:mobile.skill.title", "Skills"),
          }}
        />
        {/* 同 label：无 native header 则 `headerSearchBarOptions` 无处挂载，
            搜索框不渲染，`query` 恒为空 —— 本批新增的
            `common:mobile.common.no_matches`（搜索态才渲染）因此永不可达。 */}
        {/* RUYI-476: 长列表选择器以最大 detent 打开（sheetInitialDetentIndex
            只覆盖本路由，不进 SHEET_OPTIONS）。以 0.6 小档打开时，列表顶部
            的纵向手势被 detent 切换抢走：上滑先涨高度再滚动、下滑收层而不
            滚列表（iOS prefersScrollingExpandsWhenScrolledToEdge 与
            Android BottomSheetBehavior 在非最大档都是 sheet 拖拽优先）。
            打开即落 0.95 档后列表独占纵向手势，0.6 档仅作 grabber 下拉的
            停靠点。 */}
        <Stack.Screen
          name="issue/[id]/picker/project"
          options={{
            ...SHEET_OPTIONS,
            headerShown: true,
            title: i18n.t("layout:tab.project", "Project"),
            sheetInitialDetentIndex: "last",
          }}
        />
        <Stack.Screen
          name="issue/[id]/picker/due-date"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen name="issue/[id]/runs" options={SHEET_OPTIONS} />
        {/* Run detail (RUYI-33) — stacked on the runs sheet. Same sheet
            chrome on iOS; the body draws its own title + Copy-all + close.
            Android: full-screen modal instead of a second formSheet. The
            nested-formSheet dismiss path is broken upstream (react-native-
            screens #4331 — native backdrop/swipe dismiss desyncs the JS
            router so the sheet reopens from stale stack state, and #4090 —
            the screen underneath loses touches/scroll until the app is
            killed), which is exactly defect D / D3. Single-layer formSheets
            (runs, pickers) stay on the long-validated path. */}
        <Stack.Screen
          name="issue/[id]/runs/[taskId]"
          options={
            Platform.OS === "android"
              ? {
                  presentation: "modal" as const,
                  contentStyle: { flex: 1 },
                  headerShown: false,
                }
              : SHEET_OPTIONS
          }
        />
        {/* Full emoji picker for a comment reaction. Pushed from the "+"
            button inside the comment long-press tapback row — see
            components/issue/comment-context-menu.tsx. */}
        <Stack.Screen
          name="issue/[id]/comment/[commentId]/emoji-picker"
          options={SHEET_OPTIONS}
        />
        {/* Comments directory (RUYI-28) — Android-first navigation aid.
            Full-page modal (not a formSheet): it's a browse/search surface
            with a back-stack entry, not a picker row. Body draws its own
            header per the modal-container rules; Android system back
            closes it. Deliberately does NOT reuse the deep-link
            `highlight` param — see comments.tsx header comment. */}
        <Stack.Screen
          name="issue/[id]/comments"
          options={{
            title: i18n.t("issues:mobile.detail.comments_title", "Comments"),
            presentation: "modal",
          }}
        />
        {/* Related pull requests (RUYI-43) — full-page modal, same
            container rationale as issue/[id]/comments above: a browse
            surface with a back-stack entry, not a picker row. Title reuses
            the existing web sidebar section key
            `issues:detail.section_pull_requests` (en "Pull requests"),
            already localized in every bundle. */}
        <Stack.Screen
          name="issue/[id]/pull-requests"
          options={{
            title: i18n.t("issues:detail.section_pull_requests", "Pull requests"),
            presentation: "modal",
          }}
        />
        {/* Project-detail formSheet pickers. */}
        <Stack.Screen
          name="project/[id]/picker/status"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen
          name="project/[id]/picker/priority"
          options={SHEET_OPTIONS}
        />
        {/* 同上。标题用既有 `projects:toolbar.section_lead`（en "Lead"），
            与 label 用 `issues:filters.section_label` 同构：筛选区 section
            的单数实体名。`projects:detail.prop_lead` 在 origin/main 不存在。 */}
        <Stack.Screen
          name="project/[id]/picker/lead"
          options={{
            ...SHEET_OPTIONS,
            headerShown: true,
            title: i18n.t("projects:toolbar.section_lead", "Lead"),
          }}
        />
        <Stack.Screen
          name="project/[id]/add-resource"
          options={SHEET_OPTIONS}
        />
        {/* New-issue draft pickers — stacked on top of the new-issue.tsx
            Stack.Screen (which is itself a `modal`). iOS presents them as
            formSheets; Android swaps each one to a full-screen modal —
            modalStackedSheetOptions explains why the nested
            formSheet-on-modal path is avoided on Android (RUYI-623). */}
        <Stack.Screen
          name="new-issue-picker/status"
          options={modalStackedSheetOptions(SHEET_OPTIONS, Platform.OS)}
        />
        <Stack.Screen
          name="new-issue-picker/priority"
          options={modalStackedSheetOptions(SHEET_OPTIONS, Platform.OS)}
        />
        <Stack.Screen
          name="new-issue-picker/assignee"
          options={modalStackedSheetOptions(
            {
              ...SHEET_OPTIONS,
              headerShown: true,
              title: i18n.t("issues:actions.assignee", "Assignee"),
            },
            Platform.OS,
          )}
        />
        {/* 同 issue/[id]/picker/project。sheetInitialDetentIndex 见彼处
            RUYI-476 注释：长列表选择器以最大 detent 打开，避免 detent
            切换抢走列表的纵向手势。 */}
        <Stack.Screen
          name="new-issue-picker/project"
          options={modalStackedSheetOptions(
            {
              ...SHEET_OPTIONS,
              headerShown: true,
              title: i18n.t("layout:tab.project", "Project"),
              sheetInitialDetentIndex: "last" as const,
            },
            Platform.OS,
          )}
        />
        <Stack.Screen
          name="new-issue-picker/due-date"
          options={modalStackedSheetOptions(SHEET_OPTIONS, Platform.OS)}
        />
        {/* New-project draft pickers — same pattern as
            new-issue-picker/*. Stacked on top of `project/new` (a modal). */}
        <Stack.Screen
          name="new-project-picker/status"
          options={modalStackedSheetOptions(SHEET_OPTIONS, Platform.OS)}
        />
        <Stack.Screen
          name="new-project-picker/priority"
          options={modalStackedSheetOptions(SHEET_OPTIONS, Platform.OS)}
        />
        {/* Shared filter sheet for My Issues and the workspace Issues page —
            chooses the right view-store via `?scope=my|all` URL param. */}
        <Stack.Screen name="issues-filter" options={SHEET_OPTIONS} />
        {/* RUYI-496: chat detail screen — the whole chat surface, pushed on
            top of the tabs. Draws its own Header (back + title + session
            actions), so the native stack header stays off. */}
        <Stack.Screen
          name="chat/[sessionId]"
          options={{ headerShown: false }}
        />
        {/* Archived chats sub-view (RUYI-533) — pushed from the entry at the
            bottom of the chat tab list; native header like issue/[id]. Same
            pattern as the RUYI-532 archived-inbox sub-view; title uses the
            shared `list.archived_title` key web's archived view renders. */}
        <Stack.Screen
          name="chat/archived"
          options={{
            title: i18n.t("chat:list.archived_title", "Archived"),
            headerBackTitle: "Back",
          }}
        />
        {/* Chat session rename sheet (RUYI-51) — reached from the chat
            header's ⋯ menu. Isolated sheet (no chip-row neighbours), so it
            may override the detents with fitToContents; see the SHEET_OPTIONS
            comment for why the shared default can't. */}
        <Stack.Screen
          name="chat-rename"
          options={{
            ...SHEET_OPTIONS,
            sheetAllowedDetents: "fitToContents",
          }}
        />
        {/* Workspace switcher — reached from the More popover's collapsed
            WorkspaceCard. Two-step (pick → iOS Alert confirm → switch). */}
        <Stack.Screen name="switch-workspace" options={SHEET_OPTIONS} />
        {/* RUYI-344: more/issues (the old workspace-wide list) is gone —
            its job moved into the bottom Tasks tab. The personal view
            lives on here as more/my-issues (More dropdown entry). */}
        <Stack.Screen
          name="more/my-issues"
          options={{ title: i18n.t("layout:nav.my_issues", "My Issues"), headerBackTitle: "Back" }}
        />
        <Stack.Screen
          name="more/projects"
          options={{ title: i18n.t("layout:nav.projects", "Projects"), headerBackTitle: "Back" }}
        />
        {/* Tasks-tab sort / actor-picker sheets (formSheet presentation). */}
        <Stack.Screen name="tasks-sort" options={SHEET_OPTIONS} />
        <Stack.Screen name="tasks-actor-picker" options={SHEET_OPTIONS} />
        {/* Decisions-tab filter sheet (RUYI-530, formSheet presentation) —
            reads/writes decisions-view-store directly. */}
        <Stack.Screen name="decisions-filter" options={SHEET_OPTIONS} />
        <Stack.Screen
          name="more/agents"
          options={{ title: i18n.t("layout:nav.agents", "Agents"), headerBackTitle: "Back" }}
        />
        <Stack.Screen
          name="more/agents/[id]"
          options={{ title: i18n.t("layout:nav.agents", "Agents"), headerBackTitle: "Back" }}
        />
        {/* RUYI-346: 智能体与小队管理（P0）。创建走 modal；agent 域
            skills/env/webhooks 与 squad 域 add-member 走 formSheet
            （body 自绘 header）。squad 详情用原生 header，body 内以
            Stack.Screen 动态写 title。 */}
        <Stack.Screen
          name="more/agents/new"
          options={{
            presentation: "modal",
            headerShown: false,
          }}
        />
        <Stack.Screen
          name="more/agents/[id]/edit-profile"
          options={SHEET_OPTIONS}
        />
        {/* RUYI-624: dedicated instructions window (RUYI-541 squad pattern)
            and run-config editor (web execution section) behind the detail
            page's split entries. */}
        <Stack.Screen
          name="more/agents/[id]/edit-instructions"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen
          name="more/agents/[id]/run-config"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen
          name="more/agents/[id]/skills"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen
          name="more/agents/[id]/env"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen
          name="more/agents/[id]/webhooks"
          options={SHEET_OPTIONS}
        />
        {/* RUYI-425 §4.3: voice runtime instances — list from the More
            dropdown, modal create form, settings page (its in-page
            Stack.Screen overrides the title with the instance name). */}
        <Stack.Screen
          name="more/runtimes"
          options={{ title: i18n.t("layout:nav.voice_runtimes", "Voice Runtimes"), headerBackTitle: "Back" }}
        />
        <Stack.Screen
          name="more/runtimes/new"
          options={{
            presentation: "modal",
            headerShown: false,
          }}
        />
        <Stack.Screen
          name="more/runtimes/[id]"
          options={{ title: i18n.t("layout:nav.voice_runtimes", "Voice Runtimes"), headerBackTitle: "Back" }}
        />
        {/* RUYI-418 B2/B3：agent 设置/能力子屏（formSheet，body 自绘 header）。 */}
        <Stack.Screen
          name="more/agents/[id]/access"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen
          name="more/agents/[id]/custom-args"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen
          name="more/agents/[id]/runtime-config"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen
          name="more/agents/[id]/mcp"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen
          name="more/agents/[id]/composio"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen
          name="more/agents/[id]/integrations"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen
          name="more/squads"
          options={{ title: i18n.t("layout:nav.squads", "Squads"), headerBackTitle: "Back" }}
        />
        <Stack.Screen
          name="more/squads/[id]"
          options={{ title: i18n.t("layout:nav.squads", "Squads"), headerBackTitle: "Back" }}
        />
        <Stack.Screen
          name="more/squads/new"
          options={{
            presentation: "modal",
            headerShown: false,
          }}
        />
        <Stack.Screen
          name="more/squads/[id]/add-member"
          options={SHEET_OPTIONS}
        />
        {/* RUYI-418 Q10: 执行配置管理（列表/编辑/激活），formSheet。 */}
        <Stack.Screen
          name="more/squads/[id]/execution-profiles"
          options={SHEET_OPTIONS}
        />
        {/* RUYI-541: 小队 instructions 编辑窗——详情页缩略预览背后的
            独立窗口（完整查看/编辑 + 未保存离开拦截），formSheet。 */}
        <Stack.Screen
          name="more/squads/[id]/edit-instructions"
          options={SHEET_OPTIONS}
        />
        <Stack.Screen
          name="more/pins"
          options={{ title: i18n.t("layout:sidebar.pinned_label", "Pinned"), headerBackTitle: "Back" }}
        />
        <Stack.Screen
          name="more/settings"
          options={{ title: i18n.t("layout:nav.settings", "Settings"), headerBackTitle: "Back" }}
        />
        <Stack.Screen
          name="more/settings/profile"
          options={{ title: i18n.t("settings:page.tabs.profile", "Profile"), headerBackTitle: i18n.t("layout:nav.settings", "Settings") }}
        />
        <Stack.Screen
          name="more/settings/notifications"
          options={{ title: i18n.t("settings:page.tabs.notifications", "Notifications"), headerBackTitle: i18n.t("layout:nav.settings", "Settings") }}
        />
        <Stack.Screen
          name="new-issue"
          options={{
            title: i18n.t("modals:create_issue.sr_manual", "New Issue"),
            presentation: "modal",
            headerLeft: () => <ModalCloseButton />,
          }}
        />
        {/* Smart-mode actor picker (RUYI-68) — stacked on the new-issue
            modal like the other new-issue-picker sheets; the search header
            mirrors new-issue-picker/assignee. */}
        <Stack.Screen
          name="new-issue-picker/actor"
          options={modalStackedSheetOptions(
            {
              ...SHEET_OPTIONS,
              headerShown: true,
              title: i18n.t(
                "modals:create_issue.agent.created_by",
                "Created by",
              ),
            },
            Platform.OS,
          )}
        />
        <Stack.Screen
          name="search"
          options={{
            title: i18n.t("search:title", "Search"),
            presentation: "modal",
            headerLeft: () => <ModalCloseButton />,
          }}
        />
      </Stack>
    </RealtimeProvider>
  );
}
