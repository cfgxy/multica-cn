import { describe, expect, it } from "vitest";

import {
  dashboardAgentRunTimeOptions,
  dashboardFailuresByAgentOptions,
  dashboardFailuresDailyOptions,
  dashboardRunTimeDailyOptions,
  dashboardUsageByAgentOptions,
  dashboardUsageDailyOptions,
} from "./queries";

type QueryOptionsWithPlaceholder = {
  queryKey: readonly unknown[];
  placeholderData?: unknown;
};

function resolvePlaceholder(
  options: QueryOptionsWithPlaceholder,
  previousData: unknown,
  previousKey: readonly unknown[] | undefined,
) {
  expect(options.placeholderData).toBeTypeOf("function");
  return (
    options.placeholderData as (
      data: unknown,
      query: { queryKey: readonly unknown[] } | undefined,
    ) => unknown
  )(
    previousData,
    previousKey ? { queryKey: previousKey } : undefined,
  );
}

const optionBuilders = [
  dashboardUsageDailyOptions,
  dashboardUsageByAgentOptions,
  dashboardAgentRunTimeOptions,
  dashboardRunTimeDailyOptions,
  dashboardFailuresDailyOptions,
  dashboardFailuresByAgentOptions,
] as const;

const CURRENT = { start: "2026-02-08", end: "2026-03-08" };

describe("dashboard window placeholders", () => {
  it.each(optionBuilders)(
    "keeps the previous %s result when only the window changes",
    (buildOptions) => {
      const previous = [{ sentinel: "current-window" }];
      const previousOptions = buildOptions("ws-1", CURRENT, null, "UTC");
      const nextOptions = buildOptions(
        "ws-1",
        { start: "2026-02-01", end: "2026-02-28" },
        null,
        "UTC",
      );

      expect(
        resolvePlaceholder(nextOptions, previous, previousOptions.queryKey),
      ).toBe(previous);
    },
  );

  it("does not carry placeholder data across workspace, project, or timezone scopes", () => {
    const previous = [{ sentinel: "previous-scope" }];
    const nextOptions = dashboardUsageDailyOptions(
      "ws-1",
      CURRENT,
      "project-1",
      "Asia/Shanghai",
    );
    const previousScopes = [
      dashboardUsageDailyOptions("ws-2", CURRENT, "project-1", "Asia/Shanghai"),
      dashboardUsageDailyOptions("ws-1", CURRENT, "project-2", "Asia/Shanghai"),
      dashboardUsageDailyOptions("ws-1", CURRENT, "project-1", "UTC"),
    ];

    for (const previousOptions of previousScopes) {
      expect(
        resolvePlaceholder(nextOptions, previous, previousOptions.queryKey),
      ).toBeUndefined();
    }
  });

  it("keeps the initial loading state honest when there is no previous query", () => {
    const options = dashboardUsageDailyOptions("ws-1", CURRENT, null, "UTC");

    expect(resolvePlaceholder(options, undefined, undefined)).toBeUndefined();
  });

  it("repoints the cache per window so distinct historical ranges never share entries", () => {
    // The window object sits at the position the old `days` number occupied
    // (index 3) — TanStack Query hashes it structurally, so equal windows
    // collide (cache hits) and different windows get their own entry.
    const a = dashboardUsageDailyOptions(
      "ws-1",
      { start: "2026-01-01", end: "2026-01-31" },
      null,
      "UTC",
    );
    const aAgain = dashboardUsageDailyOptions(
      "ws-1",
      { start: "2026-01-01", end: "2026-01-31" },
      null,
      "UTC",
    );
    const b = dashboardUsageDailyOptions(
      "ws-1",
      { start: "2026-01-01", end: "2026-01-07" },
      null,
      "UTC",
    );

    expect(JSON.stringify(a.queryKey)).toEqual(JSON.stringify(aAgain.queryKey));
    expect(JSON.stringify(a.queryKey)).not.toEqual(JSON.stringify(b.queryKey));
  });
});
