# Mobile App Rules (apps/mobile/)

For cross-app sharing rules, see the root `CLAUDE.md` *Sharing Principles* section. This file documents the locked tech-stack baseline and the few mobile-specific rules — so AI doesn't suggest outdated alternatives.

## What mobile may import from `packages/`

- `import type` from `@multica/core/types/*` (zero runtime coupling)
- Pure functions from `@multica/core/`

Everything else, mobile writes its own.

## Behavioral parity with web/desktop

Mobile is allowed to differ in **UI and interaction** — it's a phone, not a port. It is NOT allowed to differ in **product semantics**. Users should not get a different mental model of "what's there" depending on which client they open.

Concrete rules:

- **Counts and visibility must agree.** If web shows the user N comments on an issue under a given filter, mobile must show the same N (subject to identical pagination/coalescing rules). If mobile silently re-implements timeline grouping with different coalescing windows, mobile is wrong.
- **Permissions and access checks must agree.** "Can comment", "can change status", "can archive inbox item" — mobile decides via the same logic web does (mirrored from packages/core, not re-derived from feel).
- **State enums and transitions must agree.** Issue status set, priority set, inbox item types, comment types — mobile renders all of them (with a sensible fallback for unknown values, per "API Response Compatibility" in the root CLAUDE.md). Mobile does NOT silently drop categories.
- **Data identity must agree.** Same `id`, same `slug`, same canonical fields. Mobile does not invent its own ids or normalize differently.

**Concrete UX divergence is fine** when it preserves semantics:

- ✅ Web shows comment thread as a recursive tree; mobile shows a flat list (because phone screens). Same comments, different layout.
- ✅ Web has a sidebar workspace switcher; mobile puts it in Settings. Same switching semantics.
- ✅ Web shows inbox item read-state with a filled background; mobile uses a leading dot. Same boolean.
- ❌ Web counts both replies and parent comments in the comment count; mobile counts only top-level. **Not allowed** — same N rule.
- ❌ Web treats `status="cancelled"` as visible; mobile silently hides it. **Not allowed** — same enums rule.

When UI requires a divergence, write down at the divergence point what the rule is mirroring (point at the source function in packages/core or packages/views) and why mobile renders it differently. Future readers should be able to tell, in 30 seconds, that the mobile divergence is intentional and which web-side function is the source of truth.

### ⚠️ Incident (2026-05-09): inbox dedup missing — counts disagreed

**Symptom**: Web sidebar showed "Inbox 1" while mobile rendered 3+ unread dots on the same workspace, same user, same moment.

**Root cause**: Backend `GET /api/inbox` returns raw rows that include:
1. archived items, and
2. multiple inbox notifications per issue (a comment, a status change, and an assignment on the same issue each create one row).

Web/desktop run those raw rows through `deduplicateInboxItems` (`packages/core/inbox/queries.ts`) before rendering and before counting unread:
1. filter `archived = true` out
2. group by `issue_id`, keep the newest in each group
3. sort by `created_at` desc

Mobile's first cut rendered the raw list directly. So a single issue with 3 notifications showed as 3 rows with 3 unread dots, while web showed 1.

**Fix**: mirror `deduplicateInboxItems` into `apps/mobile/lib/inbox-display.ts`, run mobile's inbox tab through it before rendering and before any counting.

**Lesson — encode this into your reflexes when adding any new mobile screen that consumes a list endpoint**:

> Before rendering an API list response, grep `packages/core/<domain>/queries.ts` and `packages/views/<domain>/components/*.tsx` for any preprocessing — `dedupe*`, `coalesce*`, `filter*`, `*-display.ts`, `useMemo(() => transform(raw))`. Mirror everything that runs between `useQuery` and the JSX in web/desktop. **Do not assume the backend returns "what should be displayed"** — it usually returns the raw cache shape, and the client is responsible for shaping it.

This pattern repeats: timeline coalescing (`buildTimelineGroups`), inbox dedup, comment thread flattening, etc. Each one is a behavioral parity hazard if mobile skips it.

## Tech-stack baseline

Start minimal. Add to this list when actually adopted — do NOT pre-list libraries.

- **Expo SDK 55**
- **React Native 0.82**
- **React 19.1** — whatever Expo SDK 55 ships. Pinned in `apps/mobile/package.json` directly, NOT via root `catalog:`.
- **TypeScript** strict
- **Expo Router 55** (file-based routing — version aligns with Expo SDK)
- **NativeWind 4** + **Tailwind 3.4** — NativeWind 5 is unstable; stay on v4. (Note: web/desktop use Tailwind v4 — versions intentionally differ.)
- **react-native-reusables (RNR)** — the shadcn equivalent for React Native. Uses NativeWind + RN-Primitives + CVA. Component API mirrors shadcn. **Phased adoption in progress — see `apps/mobile/docs/rnr-migration.md` for the canonical plan, three-tier classification, and Phase 0/1/2/3 status.**
- **TanStack Query 5** — mobile owns its `QueryClient` with `AppState` focus listener + `NetInfo` online listener.
- **Zustand** — mobile-local state only.
- **expo-secure-store** — auth token persistence + theme preference (`light` / `dark` / `system`).

When upgrading any of these, update this list.

## UI components & theming

The full plan, file inventory, and migration phases live in `apps/mobile/docs/rnr-migration.md`. The rules below are the durable ones that must survive after the migration completes — read this section first when working on any UI.

### Hard rule — defaults first, three-tier waterfall

Two principles govern every UI decision on mobile. They exist to fight the temptation to recreate things that already exist — which is exactly the trap that produced the current 21 hand-written components and 18 hand-rolled sheets.

**Principle 1 — defaults first.** When you use any RNR component, accept its default variant, default size, default spacing, default palette. Do NOT add wrapper layers, "improved" defaults, or `variant="multicaCustom"` styles unless a concrete product need demands it. Reaching for shadcn defaults is correct; reaching for a hand-tuned version of them is the failure mode.

**Principle 2 — iOS native > RNR > discuss.** When you need a new interaction, walk this waterfall in order, stop at the first hit:

1. **iOS / RN ships a native API?** Use it directly. Don't wrap a `Modal` to mimic it.
   - Text input prompt → `Alert.prompt`
   - Confirm / destructive prompt → `Alert.alert`
   - Action sheet (one-of-N) → `ActionSheetIOS.showActionSheetWithOptions`
   - Date / time → `@react-native-community/datetimepicker` (already installed)
   - Image / camera → `expo-image-picker` (already installed)
   - Documents → `expo-document-picker` (already installed)
   - Share → `Share.share` from `react-native`
   - Haptics → `expo-haptics` (already installed)
2. **RNR ships a matching component?** `npx @react-native-reusables/cli@latest add <name>`. Use the default variant/size/palette.
3. **Neither.** **Stop and ask the user.** Don't silently hand-roll a replacement — that's exactly how the pre-migration legacy accumulated.

### Component placement

After deciding via the waterfall:

- **Generic UI primitives** → `components/ui/`. Either RNR `add` output or hand-written with `cva` + `cn()` + semantic tokens + `@rn-primitives/*` building blocks.
- **Domain UI** (anything mentioning issues, priorities, statuses, actors, agents, presence, projects, runs) → `components/<domain>/`. Composes primitives but isn't generic.

Never copy the visual shape of an existing hand-written `components/ui/` component as a template if its RNR equivalent exists — most of them are pre-migration legacy. The migration doc tracks which files are legacy and which have been replaced.

### Theming model — CSS variables + class-based dark mode

- Source of truth for colors is `global.css` — CSS variables defined under `:root` (light) and `.dark:root` (dark). `tailwind.config.js` maps utilities like `bg-background` to `hsl(var(--background))`, so the same class name resolves to the right color in either mode automatically.
- `darkMode: 'class'` (NOT media-query). We control the mode explicitly so the in-app Settings → Appearance picker (`light` / `dark` / `system`) can override the OS preference.
- The mode is switched by NativeWind's `useColorScheme().setColorScheme(mode)`. Calling it sets the root class; every `bg-foo` / `text-foo` reactively rebinds to the new variable values. No manual className toggling, no re-render dance.
- React Navigation (`expo-router`'s `Stack` headers, modal chrome, drawer) is themed separately by passing `NAV_THEME[isDarkColorScheme ? 'dark' : 'light']` into `ThemeProvider`. Source of `NAV_THEME` is `lib/theme.ts`, which mirrors `global.css` in TypeScript.
- Persistence: the user's choice goes into `expo-secure-store` under the key `theme-preference` (values: `light` / `dark` / `system`). Loaded synchronously at app startup in `app/_layout.tsx` before the first paint; missing key defaults to `system`.
- **When you change a CSS variable in `global.css`, also update `lib/theme.ts`.** They mirror each other. The RNR docs include a prompt template for this sync.

### What this replaces (and what stays)

- The old "Visual tokens" approach — hand-transcribed hex values in `tailwind.config.js` — is being **replaced** by the CSS-variable system above. Web tokens are still inspiration only; we do NOT import `packages/ui/styles/tokens.css` (Tailwind v3.4 vs v4 mismatch makes file sharing impractical; isolation is intentional).
- The `cn()` helper at `lib/utils.ts` stays — RNR uses the same one.
- The sheet rule from Lesson 6 below still applies. RNR ships `Dialog` and other modal primitives; use them for **new** sheets. Existing sheets migrate one PR at a time per `~/.claude/plans/mobile-sheet-rollout.md` — do not bulk-replace `sheet-shell.tsx`.

## Build & release

- **Main CI** (`.github/workflows/ci.yml`) excludes mobile via `--filter='!@multica/mobile'`. Mobile failures do NOT block web/desktop PRs.
- **Mobile verify** (`.github/workflows/mobile-verify.yml`): triggered on `apps/mobile/**` or `packages/core/types/**` changes — runs typecheck/lint/test only, no IPA build.
- **Mobile release** (`.github/workflows/mobile-release.yml`): triggered by `mobile-v*.*.*` tag → `eas build` + `eas submit`.
- **OTA** — EAS Update for JS-only fixes that don't change the runtime version. Manual / on-demand push to preview/production channels.

Mobile release cadence is decoupled from main `v*.*.*` tags (server / CLI / desktop).

## Realtime / WebSocket strategy

Mobile uses the same WS server protocol as web/desktop, but mounts subscriptions differently. The rules below exist because mobile-specific constraints (cellular data cost, AppState lifecycle, per-screen unmount cleanup, smaller cache surface) make a direct port of web's pattern wrong.

### Three-layer stack

```
Layer 1  ws-client.ts                — single socket, no React. Exponential
                                       backoff with full jitter. Three-state
                                       lifecycle (idle / active / paused) so
                                       the provider can pause on background
                                       and resume on foreground without
                                       racing the auto-reconnect timer.
Layer 2  realtime-provider.tsx       — owns the WSClient. Mounts/unmounts on
                                       auth + workspace + AppState + NetInfo
                                       changes. Exposes useWSClient().
Layer 3  use-<feature>-realtime.ts   — per-feature subscriptions. Translate
                                       events → cache mutations.
```

Layer 3 is what changes per feature; layers 1 and 2 are infrastructure and shouldn't be edited when adding event coverage.

### Mount strategy: list-level global, per-record per-screen

Mobile **does NOT use a single centralized `useRealtimeSync` hook** like `packages/core/realtime/use-realtime-sync.ts`. That pattern is fine on web (one tab = one mount, lives forever) but on mobile it gets in the way: most events care about a single record (one issue's comments, one chat session's messages), and the hook needs to know which record without prop-drilling.

Two mount tiers:

- **Listing-level (always-on for the workspace session)** — mount inside the `<RealtimeSubscriptions />` component in `app/(app)/[workspace]/_layout.tsx`. These don't take parameters; they patch caches keyed only on `wsId`. Examples: `useInboxRealtime`, `useMyIssuesRealtime`. Both run from the moment the user enters a workspace until they leave it, regardless of which tab is foregrounded.

- **Per-record (mounted with id, cleans up on unmount)** — mount inside the screen that owns the record, parameterized by the id from the route. Example: `useIssueRealtime(id, () => router.back())` in `issue/[id].tsx`. The hook filters every event by `payload.issue_id === id` and only patches the current issue's caches. When the user navigates away the `useEffect` cleanup unsubscribes all listeners, so a backgrounded screen doesn't keep mutating caches it no longer owns.

Don't mount a per-record hook globally to "just be safe" — every filter call on every event then runs N times where N is the number of issues a user has ever opened in this session.

### Patch over invalidate (cellular-data rule)

When a WS payload contains the full updated object, **patch** the cache (`setQueryData` / `setQueriesData`). Only fall back to **invalidate** when:

1. The payload is just an id (we don't know the full new shape — e.g., `issue:created` with no scope context).
2. The cache shape doesn't match what we can patch (e.g., multi-key scope-filtered lists where we'd have to predict membership).
3. The event is rare enough that the extra refetch isn't a real cost (e.g., `issue:deleted` on a list that was about to invalidate anyway).
4. After a reconnect, where we may have missed events while disconnected.

Web is fine to invalidate generously because most users are on broadband; mobile users on cellular pay for each refetch. A `setQueryData` is free; an `invalidateQueries` is a network roundtrip per affected query key.

### Mobile-owned updaters (don't import `packages/core/issues/ws-updaters.ts`)

Mobile has its own `apps/mobile/data/realtime/issue-ws-updaters.ts` even though web has a near-identical file. **Do not import web's updaters into mobile.** Two reasons:

1. **Key-factory binding.** Web's updaters reference `issueKeys` from `packages/core/issues/queries.ts` — a different runtime instance from mobile's `apps/mobile/data/queries/issue-keys.ts`. TanStack Query compares keys structurally so it *appears* to work, but binding cache mutation to a foreign key factory invites silent drift the moment either side adjusts its key shape (renames a segment, adds a discriminator).
2. **Cache-shape divergence.** Mobile has simpler caches: flat `Issue[]` for my-issues (web has status-bucketed); no children subtree (web does); no label-byIssue cache (web does). Web's updaters carry conditional dead-code for paths mobile doesn't have, and mobile would silently no-op on web shapes that don't exist locally.

When the same logic needs to exist on both sides, copy the design — not the import. Document the mirror at the top of the mobile file (see `issue-ws-updaters.ts` for the pattern).

### Event-always-wins (optimistic conflict policy)

Mutations like `useUpdateIssue` apply an optimistic patch to the detail cache, then the server processes the request and broadcasts `issue:updated`. If a separate WS event (from another client / another user / an agent) arrives between the optimistic patch and the mutation response, the WS handler overwrites the optimistic state with the server's authoritative state. Brief UI flicker is acceptable; correctness wins.

**Do not** add timestamp-comparison logic to "protect" the optimistic state — the server is the truth and the user benefits from seeing real changes immediately. If a specific event proves problematic in practice, add the gate at that point, not by default.

### Reconnect handling

Each hook registers a single `ws.onReconnect(cb)` that invalidates **only the queries it owns**:

| Hook | Invalidates on reconnect |
|---|---|
| `useInboxRealtime` | `inboxKeys.list(wsId)` |
| `useMyIssuesRealtime` | `issueKeys.myAll(wsId)` |
| `useIssueRealtime(id)` | `issueKeys.detail(wsId, id)` + `issueKeys.timeline(wsId, id)` |

No global "invalidate everything on reconnect" sweep. The fanout would be every screen the user has ever visited in this session refetching simultaneously — wasteful on cellular and prone to rate-limiting the server in low-signal areas where reconnects happen frequently.

### Cross-cutting cache patches across features

Some events legitimately need to mutate a foreign feature's cache. The
canonical example: `issue:updated` changing an issue's status must also
update the StatusIcon shown on the matching inbox row, and `issue:deleted`
must strip every inbox row pointing at the dead issue.

The pattern:

1. **The feature whose cache is being patched owns the updater.** Example:
   `apps/mobile/data/realtime/inbox-ws-updaters.ts` exports
   `patchInboxIssueStatus` and `dropInboxItemsByIssue` — they live with
   inbox, not with issues, because they read `inboxKeys.list(wsId)`.
2. **That feature's realtime hook subscribes to the foreign event.**
   `use-inbox-realtime.ts` subscribes to `issue:updated` and `issue:deleted`
   alongside the `inbox:*` events. The issue-realtime hook does NOT know
   that inbox cares.
3. **Mirror web's wiring.** Web's `packages/core/inbox/ws-updaters.ts` has
   the same handlers; mobile copies the design. Behavioral parity hazard:
   without these the mobile inbox row keeps showing the prior status (or
   404s on tap if the issue is gone) while web users see the change live.

If you find yourself reaching across features in `use-issues-realtime` to
patch something else, you have the inversion: move the updater to the
patched feature and subscribe there.

### Adding new event coverage — recipe

1. **Read the payload.** Find the event in `@multica/core/types/events.ts`. Note the fields; decide if patch is possible (full object) or invalidate is required (just an id).
2. **Mirror, don't import.** If web has an updater for this event in `packages/core/<feature>/ws-updaters.ts`, copy the design into `apps/mobile/data/realtime/<feature>-ws-updaters.ts`. Adapt to mobile's actual cache shapes — don't carry web's bucket/children/childProgress dead-code if mobile doesn't have those caches.
3. **Subscribe in a hook.** Either extend an existing `use-<feature>-realtime.ts` or create a new one. Filter by id at the top of each handler so per-record hooks ignore unrelated events.
4. **Mount it.** Listing-level → add to `<RealtimeSubscriptions />` in workspace `_layout.tsx`. Per-record → add to the owning screen's body, parameterized by the route id.
5. **Add reconnect invalidate.** Single `ws.onReconnect()` call scoped to the hook's own keys.
6. **Verify cross-client.** Open the affected screen on mobile, change the same record from a second client (web or another device), confirm mobile updates within ~500ms without pull-to-refresh.

If a new event has no consumer on mobile (e.g., `subscriber:added` when mobile doesn't render subscriber lists yet), **don't subscribe**. Mounting a listener with no UI consumer adds CPU on every fire for zero user benefit.

## Data layer helpers (use these — don't recreate them)

Common boilerplate is wrapped. New code that reinvents these helpers is a
review-block, both because it makes the codebase inconsistent AND because
the helpers encode subtle correctness rules (signal forwarding, schema
fallback, sync-before-await ordering, type-safe payloads).

### Three rails that every feature must follow

1. **Logic mirrors web/desktop, only interaction is mobile-original.**
   Mobile is the *consumer* of the same server contract — endpoints,
   request bodies, response shapes, optimistic patch semantics, cache key
   prefix shapes all match web verbatim. Before adding any new feature,
   grep `packages/core/<feature>/{queries,mutations,ws-updaters}.ts` and
   `packages/core/api/client.ts` for the existing pattern and mirror it.
   UI / interaction can diverge freely (per "Behavioral parity" section
   above), but the data contract may not.

2. **Use the existing components — no new primitives.** Walk the
   `iOS native > RNR > discuss` waterfall in §UI components. If RNR ships
   it, `npx @react-native-reusables/cli@latest add <name>`. If iOS ships
   it (Alert / ActionSheetIOS / Haptics / share / picker), use it directly.
   If neither has it AND it's a single-screen need, inline compose with
   `<Pressable>` + `<Text>` + tokens. **Do NOT create a new generic
   primitive in `components/ui/` for one or two callers** — the migration
   doc lists "21 hand-written components" as exactly the trap we're
   escaping. Threshold for a new primitive is three callers AND no
   RNR/iOS-native alternative.

3. **Use the wrapped request / WS layer.** See the helper map below.

### API client: `fetchValidated` + `fetchValidatedWith`

`apps/mobile/data/api.ts` exposes two private helpers on `ApiClient` that
collapse the fetch + parseWithFallback envelope. **Every new read-side
method that returns a typed body must use them.**

| Helper | When to use | Shape |
|---|---|---|
| `this.fetchValidated(path, schema, fallback, opts?)` | GET endpoints | One-liner method body — see `getMe`, `listInbox`, `getNotificationPreferences` |
| `this.fetchValidatedWith(path, schema, fallback, init, opts?)` | Any HTTP method (PATCH / PUT / POST) whose response is consumed | Carries the body via `init.body` + method; signal forwarding handled |
| `this.fetch<T>(path, init?)` directly | Writes whose response is `{ count }` / `void` / not consumed by UI logic | Only here is a raw `as T` acceptable, because the value never reaches a render path |

Rules:
- The fallback object MUST match the success type exactly so downstream
  code never has a partial value (see `EMPTY_USER` / `EMPTY_INBOX_LIST`
  pattern in `apps/mobile/data/schemas.ts`).
- The `endpoint` label is for telemetry — defaults to the path; override
  only when the path has dynamic segments and you want stable groupings
  (`GET /api/issues/:id` not `GET /api/issues/abc-123`).
- Migration is progressive: not every legacy method is converted yet.
  Adding a new method? Use the helpers. Touching an old method that
  isn't using them? Convert it as part of the same PR.

### Query / mutation factory pattern

Every workspace-scoped feature exposes a key factory in
`apps/mobile/data/queries/<feature>.ts`:

```ts
export const inboxKeys = {
  all: (wsId: string | null) => ["inbox", wsId] as const,
  list: (wsId: string | null) => [...inboxKeys.all(wsId), "list"] as const,
};
```

Three-segment shape matches web (`packages/core/inbox/queries.ts`).
Reasons:

- TQ does prefix matching by default — `invalidateQueries({ queryKey:
  inboxKeys.all(wsId) })` invalidates the list AND any future sub-keys
  (e.g. a `detail(id)`) under the same prefix. Use `.all` to clear a
  workspace cleanly, `.list` to target the list specifically.
- Cross-platform mental-model parity: a reader switching between mobile
  and web finds the same key shape.
- Stops bare `["inbox", wsId]` strings from spreading. Grep
  `\["inbox"` in this codebase should only hit the factory file.

Mutations import the factory and use `inboxKeys.list(wsId)` everywhere —
never inline strings.

### WS layer: `ws.on<E>()` + `useWSSubscriptions`

Two helpers replace ~20 lines of boilerplate per realtime hook:

1. **`ws.on<E extends WSEventType>(event, handler)`** — the handler's
   `payload` parameter is auto-typed to `WSEventPayload<E>`. **Do not
   add `as XxxPayload` casts at handler bodies** — they're redundant
   and (worse) silently hide drift if `WSEventPayloadMap` shifts.
   The cast is only acceptable when one handler covers multiple events
   that don't share a typed common ancestor (see `onTaskEvent` in
   `use-issue-realtime.ts` — `task:progress` has no formal payload).
2. **`useWSSubscriptions(setup, deps)`** in
   `apps/mobile/lib/use-ws-subscriptions.ts` — wraps the
   `if (!ws || !wsId) return; useEffect + cleanup` template. Setup
   callback receives `(ws, wsId)`, returns the unsub array (or
   `undefined` to short-circuit, e.g. when a per-record id is missing).

Adding a new event type? Extend `packages/core/types/events.ts`:

1. Add the event to the `WSEventType` union.
2. Add the payload interface.
3. Add the `WSEventType → payload` entry in `WSEventPayloadMap`.

Forgetting step 3 means callers get `unknown` (loud — they have to
narrow), not `any` (silent unsafe access). That's the safety net.

### Synchronous setQueryData before `await cancelQueries`

Optimistic mutations that flip state read by a UI element that's about
to be in a navigation snapshot (the classic case: marking an inbox row
read, then `router.push` to the issue) MUST call `setQueryData` in
`onMutate` **before** `await qc.cancelQueries(...)`. The await yields
one microtask; iOS captures the source-view snapshot during that gap and
freezes the row in its unread style inside the slide-in transition.

Lives inside the mutation, not the caller. See `useMarkInboxRead.onMutate`
in `apps/mobile/data/mutations/inbox.ts` for the canonical example.

### Checklist for a new feature

Before opening a PR for a new screen / mutation / realtime hook:

1. Grep `packages/core/<feature>/` for the web equivalent — endpoints,
   key shape, optimistic patch shape. Mirror, don't invent.
2. API methods → `fetchValidated` / `fetchValidatedWith` (or raw
   `this.fetch` only for writes with no consumed response).
3. Query key → factory in `data/queries/<feature>.ts`, 3-segment shape.
4. Mutations → optimistic three-step (snapshot → patch → rollback) +
   settle invalidate, all keys via factory.
5. Realtime → `useWSSubscriptions(setup, deps)`, typed `ws.on<E>()`,
   per-event patching (no global invalidate) when payload carries the
   full object.
6. UI → waterfall (iOS native > RNR > inline compose). No new
   `components/ui/` primitive unless three callers + RNR doesn't ship.
7. Verify cross-client: change the same record from web and confirm
   mobile updates within ~500ms without pull-to-refresh.

## Lessons learned (encode into reflexes)

These are real mistakes that have been made building the mobile shell. Each one cost time to find. Treat as enforceable rules, not suggestions.

### 1. Install/upgrade any dependency: check `dist-tags` first

Do NOT hardcode version numbers from memory. Run `pnpm view <pkg> dist-tags` to see `latest / sdk-XX / canary` and decide which tag to lock. For Expo packages (`expo-*` / `react-native-*` that Expo aligns), use `pnpm exec expo install <pkg>` — it queries Expo's dependency manifest and picks the SDK-compatible version. `pnpm add <pkg>` will silently install the npm `latest`, which often outpaces the SDK and breaks at runtime. Past mistakes: hardcoded `expo@~54.0.0` (latest was already `55.x`); installed `lucide-react-native@0.468` without checking React 19 peer compatibility.

### 2. New source subdirectory: verify git tracking

Every time you create a new source subdirectory under `apps/mobile/` (e.g. `data/`, `lib/foo/`, `components/inbox/`):

1. Run `git check-ignore -v <dir>/<file>` immediately. The repo-root `.gitignore` has generic rules (`data/`, `build/`, `bin/`, `*.app`, `*.dmg`) that are intended for backend runtime/output dirs but will silently swallow mobile source.
2. If a rule matches, add `!<dir>/` and `!<dir>/**` to `apps/mobile/.gitignore` (subtree override beats parent rule).
3. After the commit lands, run `git ls-files <dir>` to confirm every file is tracked.

This rule exists because `apps/mobile/data/` was once committed-but-not-tracked — 14 source files (ApiClient, all queries, all stores) were missing from the git tree even though `git status` was clean. Local builds worked because Metro reads the filesystem; CI / clones would have died.

### 3. ApiClient capability list (4 must-haves)

Mobile's fetch wrapper (`apps/mobile/data/api.ts`) MUST implement all four. Missing any of them is a bug, not a deferred polish item.

1. **Zod `parseWithFallback` for response validation.** Strictly enforced by the root CLAUDE.md "API Response Compatibility" section and the "Type drift defense" section above. **Any new endpoint method that does `as T` on the response body is a bug.** Reuse schemas from `packages/core/api/schemas.ts` (pure Zod exports, on the mobile sharing whitelist); define mobile-side fallbacks for new endpoints in `apps/mobile/data/`.

2. **`onUnauthorized` 401 callback.** The `ApiClientOptions.onUnauthorized` hook fires on every 401 and must be wired in `app/_layout.tsx` to: clear auth token, clear workspace store, clear TanStack Query cache, navigate to `/login`. Without it a session that expired server-side puts every subsequent request into a 401 loop and the user sees opaque "API error: 401" toasts on every screen. Use a `signingOutRef` to make the callback idempotent — multiple in-flight requests will all 401 simultaneously when a session expires.

3. **`X-Request-ID` per request.** Generate a short random ID (`createRequestId()` in `apps/mobile/lib/request-id.ts`), send as `X-Request-ID` header. The same ID goes into client-side log lines so backend telemetry can be cross-referenced (server picks it up via the same header).

4. **Structured request logger.** Two log lines per request: `[api] → METHOD path` (start, with `rid`) and `[api] ← STATUS path` (end, with `rid` + `duration`). Use `console.error` for 5xx, `console.warn` for 404s, `console.log` for success. Without this, debugging mobile API issues means staring at the React Native Network panel; with it, the dev console is self-explanatory and prod telemetry already comes structured.

**What mobile correctly does NOT need (don't add these):** CSRF token (`X-CSRF-Token`), `credentials: "include"`, cookie reading. Mobile is Bearer-token auth, not cookie auth — the cookie attack surface that requires CSRF protection on web doesn't exist on mobile.

### 4. Visual alignment is baseline, not polish

When implementing a mobile screen / row / list:

1. Open the web/desktop equivalent source file (e.g. `packages/views/inbox/components/inbox-list-item.tsx`) and compare its JSX structure side-by-side with the mobile JSX you're about to write.
2. Run a screenshot of the web/desktop view next to a screenshot of the simulator.
3. The four items below are **baseline**, not polish for a later iteration:
   - **Tab bar must have icons** (Ionicons / SF Symbols / lucide-react-native) with focused/unfocused state switch.
   - **Each screen has a title at the top** (Stack large title, or a custom `ScreenHeader`).
   - **Row's right-side elements stack vertically into a column** when there are multiple (status above, time below). Pattern: nested flex-rows, each with its own right-aligned element. NOT a single horizontal flex-row with status and time competing for the same trailing slot.
   - **Secondary lines must use a type-aware label component** (mirror, e.g., `InboxDetailLabel`'s type switch). Rendering raw `item.body` directly leaks server-side markdown markers (`##`, `*`) and stale debug strings into the UI.

Skipping any of these in a "first cut" turns the v1 into something that prompts a "you didn't care about interaction at all" review — every time. Easier to do them up-front (15 min total) than to retrofit.

### 5. Every read query must pass `signal` to fetch; api.ts always has a hard timeout

**Symptom that triggered the rule (2026-05-11)**: Inbox screen sometimes returned to the foreground showing the FlatList pull-to-refresh spinner stuck indefinitely. List items were rendered underneath, but `isRefetching` never flipped back to `false`. Pull-to-refresh, navigating away, and re-opening the tab did not clear it.

**Root cause**: `apps/mobile/data/api.ts`'s `fetch()` had no timeout, no `AbortController`, and no caller-`signal` plumbing. iOS suspends backgrounded apps within ~30 seconds and can silently kill in-flight network tasks (facebook/react-native#35384 — "iOS fetch() POST fails if called too soon, with app running in background"; facebook/react-native#38711 — "JS Timers don't fire when app is launched in background"). When the app foregrounded, the suspended fetch's Promise neither resolved nor rejected. TanStack Query saw an existing query still in `fetching` state and did NOT start a new fetch on invalidate — it just waited on the dead Promise forever. `isRefetching` stayed `true`, the FlatList spinner stayed spinning.

**Rule, three parts (every one is required — partial fixes leave a footgun)**:

**1. `api.ts` `fetch()` MUST have a hard timeout** (currently 30s; the `FETCH_TIMEOUT_MS` constant). Without this, a single suspended request can wedge a query indefinitely. Use a manual `AbortController` + `setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS)` — **DO NOT** use `AbortSignal.timeout()`: Hermes throws `TypeError: AbortSignal.timeout is not a function` (facebook/react-native#42042). Same for `AbortSignal.any()` — Hermes does not implement it (livekit/livekit#4014). To combine the timeout signal with a caller-supplied signal, attach an `"abort"` event listener manually and forward to the inner controller.

**2. Every read-side `api.ts` method MUST accept `opts?: { signal?: AbortSignal }` and pass it to `fetch()`**. Mutations don't need this (TanStack Query doesn't pass a signal to `mutationFn`). The pattern:
```ts
async listInbox(opts?: { signal?: AbortSignal }): Promise<InboxItem[]> {
  return this.fetch<InboxItem[]>("/api/inbox", { signal: opts?.signal });
}
```
Adding a new query-bound method without `opts` is a bug — the next person who writes a `queryFn` will silently drop the signal.

**3. Every `queryFn` MUST forward the signal it receives from TanStack Query**. The official TanStack guide (tanstack.com/query/v5/docs/framework/react/guides/query-cancellation) states: "When a query becomes out-of-date or inactive, this `signal` will become aborted." The pattern:
```ts
queryOptions({
  queryKey: [...],
  queryFn: ({ signal }) => api.listInbox({ signal }),
});
```
Forgetting the destructure (writing `() => api.listInbox()`) defeats every benefit of (1) and (2): TQ can't cancel hung requests when the user navigates away, and on workspace switch every stale request lives until its 30s timeout.

**Verification**: After any change to `api.ts` or a new query addition, `grep -n "queryFn: () =>" apps/mobile/data/queries/` should return zero matches. Every `queryFn` should destructure `{ signal }`.

**Why the wiring already in `data/query-client.ts` (focusManager + AppState, onlineManager + NetInfo) is not enough on its own**: focusManager triggers a *refetch attempt* when the app comes back to the foreground, but if the prior fetch promise is hanging, TQ won't start a new request — it'll keep waiting on the dead one. Only timeout + signal cancellation actually unwedges the query. The three pieces work together: signal lets TQ proactively cancel on staleness, timeout is the safety net when nothing else fires, focusManager is the "user came back, let's recheck" trigger.

### 6. Modal container selection: match container to content, don't copy the first sheet

The mobile codebase has ~15 Modal sheets. They almost all copy the same shape (`Modal transparent fade` + hand-drawn `bg-black/40` backdrop + centered/bottom card with `maxHeight`). That shape is correct for **short action menus** (the earliest sheets), wrong for **everything else**. Once the pattern was established as "the mobile sheet style," subsequent sheets inherited it regardless of content — and inherited a different bug each time: keyboard squashing the card, `maxHeight: 380` clipping FlatLists on tall phones, `useSafeAreaInsets` returning 0 inside Modal so bottom content collides with the Home Indicator, etc.

**Choose the container by content type, not by "what the last sheet did":**

| Content shape | Container | Why |
|---|---|---|
| < 5 fixed actions, 1-2s stay, no keyboard | `Modal transparent` + bottom action card (current pattern) | Short, light, dim-backdrop tap-to-dismiss is correct here |
| Yes/No or one-tap confirm | `Alert.alert` | Native, accessible, no custom UI |
| < 7 fixed picker options, no search | `Modal transparent` + small centered card | Same as action card, just centered |
| Long list / search box / content view / form / anything with a keyboard | **`Modal presentationStyle="pageSheet"`** | iOS native: auto ~93% height, drag-dismiss, rounded corners, safe area mostly auto. Add an X button for the discoverability path; pageSheet's top exposed strip is NOT tappable. |
| Multi-screen flow / route-level modal | Expo Router `presentation: "modal"` | Has back-stack, swipe-dismiss, deep-linkable |

**The pitfalls a `presentationStyle="pageSheet"` still has:**

- **Bottom padding past the Home Indicator is not auto-applied** to scroll content. Read `useSafeAreaInsets()` from the **parent component (outside the Modal)** and pass `bottomInset` as a prop into the sheet — `useSafeAreaInsets()` called inside a `presentationStyle="pageSheet"` Modal does not reliably return non-zero on all iOS versions / RN versions. The parent-read + prop-drill is the robust path. See `apps/mobile/components/issue/runs-sheet.tsx` for the reference implementation.
- **Android falls back to full-screen** — no rounded corners, no drag. mobile/CLAUDE.md treats iOS as the primary target so this is acceptable, but document it inline at the call site if a particular feature must work identically on both.
- **Tapping the exposed top strip does NOT dismiss.** That strip is the underlying screen, not a backdrop. Users dismiss via drag-down or via your X button. Always include an X button.
- **`transparent` and `presentationStyle="pageSheet"` are mutually exclusive.** If you find yourself wanting both, you picked the wrong container.

**Past sheets that need to migrate to pageSheet** (logged in `/Users/qingnaiyuan/.claude/plans/mobile-sheet-rollout.md`): session-sheet, issue-filter-sheet, assignee/label/project/project-lead picker sheets, add-resource-sheet. Do these one PR at a time, one verification at a time — don't try to batch.

**Carve-out — picker-row consistency wins over per-container optimisation:**

The table above says "< 7 fixed picker options → centered card". That rule
applies in isolation, but **breaks down when multiple pickers coexist in
the same chip row** (issue-detail AttributeRow is the canonical case:
status / priority / assignee / label / project / due-date all sit next
to each other). Mixing centered cards (for status/priority, short
fixed lists) with pageSheets (for assignee/label/project, long lists)
means the user gets two different gestures depending on which chip
they tap — there's no muscle-memory carry-over.

When you find yourself building a row like this, **use pageSheet for
every picker in the row**, even the ones a standalone centered card
would handle fine. The cost is some empty space below 5–7 short rows;
the gain is uniform tap → slide-up-sheet + drag-down-to-dismiss
behaviour across the whole row. Linear iOS / Things 3 / Apple
Reminders all do this for the same reason.

The centered-card pattern stays correct for **isolated short menus**
(e.g. the chat-composer's "More" popover, the timeline's coalesce-
expand) where there's no neighbour to be consistent with.

### 7. Destructive swipe: reveal only, no auto-fire — always pair with haptic

iOS Mail / Linear iOS / Things: leftward swipe reveals a red Archive
button; the user **must tap it** to commit. The earlier mobile inbox
swipe auto-fired on full drag past the threshold and "felt wrong" — no
peek, easy to trigger by accident on a fast vertical scroll that
catches some horizontal motion. There is no native UX that auto-commits
a destructive action on swipe — match the platform standard.

The rule:

- `ReanimatedSwipeable` with `renderRightActions={<Pressable onPress={fireArchive} />}`.
- **No `onSwipeableOpen` auto-fire.** Drag → reveals the action; release
  past threshold → action stays revealed; tap action → commit; tap
  outside or drag back → cancel.
- One-shot `Haptics.impactAsync('medium')` when the drag crosses the
  action width. Wire via `useAnimatedReaction(() => drag.value <= -ACTION_WIDTH, ...)`
  + `runOnJS(Haptics.impactAsync)`. The shared-value reaction runs on
  the UI thread; `runOnJS` bridges to the JS-only Haptics call.

See `apps/mobile/components/inbox/swipeable-inbox-row.tsx` for the
reference implementation. When adding a new swipe-to-action row
elsewhere, copy that pattern; do not reinvent.

### 8. Tier C domain components: opportunistic upgrade only — no silent rewrites

Tier C in `apps/mobile/docs/rnr-migration.md` §4 names the domain UI
files that stay where they are but need foundation upgrades
(`ActorAvatar`, `StatusIcon`, `PriorityIcon`, `PresenceDot`, etc.).
**You don't rewrite a Tier C file just because you're rendering it in
your new feature.** That spreads scope and stalls feature PRs.

Two rules:

1. **Touch only what your PR needs to touch.** If `ActorAvatar` has
   hardcoded `#71717a` and you're building an inbox feature that
   *uses* `<ActorAvatar>`, leave the hex alone. Note it for a future
   doc / cleanup PR.
2. **Upgrade Tier C only when you're modifying that file for a
   different real reason.** E.g. adding presence to chat header → you
   were going to touch `<ActorAvatar>` anyway → fold the RNR-Avatar
   migration + hex → token cleanup into the same PR.

The pre-migration legacy persists because someone "while I'm in
here…"-style touched 21 files in one PR; we don't do that anymore.
Document any Tier C smells you spotted in the PR description as
follow-ups; surface for a future grouped Tier C cleanup PR.
