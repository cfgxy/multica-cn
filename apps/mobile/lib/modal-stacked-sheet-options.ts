/**
 * Presentation options for a formSheet stacked on top of a modal — the
 * new-issue / new-project draft picker routes sitting over the
 * `new-issue` / `project/new` modals (RUYI-623).
 *
 * iOS keeps the caller's sheet options untouched. On Android the sheet
 * chrome is swapped for a full-screen modal: the nested-formSheet dismiss
 * path is broken upstream (react-native-screens #4331 — native
 * backdrop/swipe dismiss desyncs the JS router so the sheet reopens from
 * stale stack state; #4090 — the screen underneath loses touches/scroll
 * until the app is killed), the same failure family the
 * `issue/[id]/runs/[taskId]` screen already avoids. Deriving from the iOS
 * options keeps header/title in sync across platforms; sheet-only tuning
 * (detents, grabber, corner radius) has no meaning in a full-screen
 * modal and is dropped.
 *
 * `platform` is injected instead of read from `Platform.OS` so the
 * derivation stays testable in the node vitest lane.
 */

/** Structural subset the helper reads — callers pass full Stack.Screen
    option objects (SHEET_OPTIONS spreads); extra props flow through on
    iOS and are deliberately dropped on Android. */
export interface StackedSheetOptions {
  headerShown?: boolean;
  title?: string;
}

/** The exact full-screen modal shape handed to Stack.Screen on Android. */
export interface AndroidModalSheetOptions {
  presentation: "modal";
  contentStyle: { flex: number };
  headerShown?: boolean;
  title?: string;
}

export function modalStackedSheetOptions<O extends StackedSheetOptions>(
  ios: O,
  platform: string,
): O | AndroidModalSheetOptions {
  if (platform !== "android") return ios;
  return {
    presentation: "modal",
    contentStyle: { flex: 1 },
    headerShown: ios.headerShown ?? false,
    ...(ios.title !== undefined ? { title: ios.title } : {}),
  };
}
