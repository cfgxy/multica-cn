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
- **NativeWind 4** + **Tailwind 3.4** — NativeWind 5 is unstable and doesn't support Expo Go; stay on v4. (Note: web/desktop use Tailwind v4 — versions intentionally differ.)
- **react-native-reusables (RNR)** — the shadcn equivalent for React Native. Uses NativeWind + RN-Primitives + CVA. Component API mirrors shadcn.
- **TanStack Query 5** — mobile owns its `QueryClient` with `AppState` focus listener + `NetInfo` online listener.
- **Zustand** — mobile-local state only.
- **expo-secure-store** — auth token persistence.

When upgrading any of these, update this list.

## Visual tokens (separate from web)

Mobile maintains its own design tokens in `apps/mobile/tailwind.config.js`. You MAY reference `packages/ui/styles/tokens.css` (web/desktop tokens) as inspiration, but **do not import or symlink the file**. Tokens are transcribed by hand and may diverge for mobile (touch-friendly spacing, no hover states, native typography).

Tailwind version mismatch (mobile v3.4 vs web v4) makes file sharing impractical anyway — this isolation is intentional.

## Build & release

- **Main CI** (`.github/workflows/ci.yml`) excludes mobile via `--filter='!@multica/mobile'`. Mobile failures do NOT block web/desktop PRs.
- **Mobile verify** (`.github/workflows/mobile-verify.yml`): triggered on `apps/mobile/**` or `packages/core/types/**` changes — runs typecheck/lint/test only, no IPA build.
- **Mobile release** (`.github/workflows/mobile-release.yml`): triggered by `mobile-v*.*.*` tag → `eas build` + `eas submit`.
- **OTA** — EAS Update for JS-only fixes that don't change the runtime version. Manual / on-demand push to preview/production channels.

Mobile release cadence is decoupled from main `v*.*.*` tags (server / CLI / desktop).
