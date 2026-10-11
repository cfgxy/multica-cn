// @vitest-environment node
import { describe, expect, it } from "vitest";
import en from "../locales/en/self-evolution.json";
import zhHans from "../locales/zh-Hans/self-evolution.json";
import ja from "../locales/ja/self-evolution.json";
import ko from "../locales/ko/self-evolution.json";
import { RUN_ERROR_I18N_KEYS, RUN_ERROR_FALLBACK_KEY } from "./components/retrospective-run-error";

// The run row's structured failure verdict (RUYI-561): the server persists a
// stable reason code (server/internal/retrospective/reason.go), the UI
// localizes it from retrospective.run_errors. These tests lock the mapping —
// every code present in all four locales, same key set, whitelisted param
// interpolated exactly where the server sends it, everything non-empty. A
// code renamed on the server without this table fails here first.

const LOCALES = { en, "zh-Hans": zhHans, ja, ko } as const;

const expectedKeys = Object.values(RUN_ERROR_I18N_KEYS).toSorted();
const ISSUE_ID_CODES = new Set(["report_out_of_scope", "draft_out_of_scope"]);

function phraseOf(locale: unknown, key: string): unknown {
  const runErrors = (
    locale as { retrospective?: { run_errors?: Record<string, unknown> } }
  ).retrospective?.run_errors;
  return runErrors?.[key];
}

describe("retrospective run error i18n parity across all 4 locales", () => {
  it("covers every mapped reason code with the exact same key set", () => {
    for (const [name, locale] of Object.entries(LOCALES)) {
      const actual = Object.keys(
        (locale.retrospective as Record<string, unknown>).run_errors as object,
      ).toSorted();
      expect(actual, `${name}: retrospective.run_errors keys drifted`).toEqual(expectedKeys);
    }
  });

  it("keeps every reason phrase and the unknown-code fallback non-empty", () => {
    for (const [name, locale] of Object.entries(LOCALES)) {
      const fallbackKey = RUN_ERROR_FALLBACK_KEY.split(".")[1] ?? "";
      const fallback = (locale.retrospective as Record<string, unknown>)[fallbackKey];
      expect(fallback, `${name}: ${RUN_ERROR_FALLBACK_KEY} missing`).toBeDefined();
      expect(typeof fallback, `${name}: ${RUN_ERROR_FALLBACK_KEY} not a string`).toBe("string");
      expect(String(fallback).length, `${name}: fallback is empty`).toBeGreaterThan(0);
      for (const key of expectedKeys) {
        const phrase = phraseOf(locale, key);
        expect(typeof phrase, `${name}: run_errors.${key} is not a string`).toBe("string");
        expect(String(phrase).length, `${name}: run_errors.${key} is empty`).toBeGreaterThan(0);
      }
    }
  });

  it("interpolates {{issue_id}} exactly on the out-of-scope codes and nowhere else", () => {
    for (const [name, locale] of Object.entries(LOCALES)) {
      for (const key of expectedKeys) {
        const phrase = String(phraseOf(locale, key));
        if (ISSUE_ID_CODES.has(key)) {
          expect(phrase.includes("{{issue_id}}"), `${name}: ${key} lost {{issue_id}}`).toBe(true);
        } else {
          expect(
            phrase.includes("{{"), `${name}: ${key} carries an interpolation token outside the whitelist`,
          ).toBe(false);
        }
      }
    }
  });

  it("keeps actionable reasons pointing at the in-tab config and retry", () => {
    // The PM-confirmed actionable copy shape (2026-10-09): missing what,
    // where to fix it, retry. The agent-gate codes are the actionable set —
    // their en phrases must carry the fix instruction, and no locale may
    // leak the config panel's nav path as an English fragment.
    const actionable = [
      "agent_not_configured",
      "agent_missing",
      "agent_archived",
      "agent_runtime_missing",
    ] as const;
    for (const key of actionable) {
      const enPhrase = String(phraseOf(en, key)).toLowerCase();
      expect(enPhrase.includes("save") || enPhrase.includes("run again"),
        `en: ${key} lost the retry instruction`,
      ).toBe(true);
    }
  });
});

// The run-detail sheet's copy (RUYI-663) rides the same four-locale bundle,
// so the whole retrospective section is locked to one deep key set: a key
// added to one locale only drifts the other three into missing-copy errors.
const DETAIL_KEYS = [
  "viewDetail", "detailTitle", "triggerLabel", "triggerSource", "windowLabel",
  "startedAt", "finishedAt", "duration", "durationRunning", "statsLabel",
  "failureLabel", "scannedIssues", "analyzedIssues", "noIssues", "viewLog",
] as const;

function deepKeys(node: unknown, path: string[] = []): string[] {
  if (node === null || typeof node !== "object") return [path.join(".")];
  return Object.entries(node as Record<string, unknown>).flatMap(([k, v]) =>
    deepKeys(v, [...path, k]),
  );
}

describe("retrospective section key parity across all 4 locales (RUYI-663 detail surface)", () => {
  it("presents the same deep key set everywhere", () => {
    const enKeys = deepKeys(en.retrospective).toSorted();
    for (const [name, locale] of Object.entries(LOCALES)) {
      expect(deepKeys(locale.retrospective).toSorted(), `${name}: retrospective keys drifted`).toEqual(enKeys);
    }
  });

  it("covers the run-detail keys in every locale as non-empty strings", () => {
    for (const [name, locale] of Object.entries(LOCALES)) {
      const retro = locale.retrospective as unknown as Record<string, unknown>;
      for (const key of DETAIL_KEYS) {
        const value = retro[key];
        expect(value, `${name}: retrospective.${key} missing`).toBeDefined();
        expect(typeof value, `${name}: retrospective.${key} is not a string or object`).toMatch(/string|object/);
        if (typeof value === "string") {
          expect(value.length, `${name}: retrospective.${key} is empty`).toBeGreaterThan(0);
        }
      }
    }
    // triggerSource is the one nested object: both enum labels must exist.
    for (const [name, locale] of Object.entries(LOCALES)) {
      const source = (locale.retrospective as unknown as Record<string, { schedule?: unknown; manual?: unknown }>).triggerSource;
      for (const k of ["schedule", "manual"] as const) {
        expect(typeof source?.[k], `${name}: triggerSource.${k} missing`).toBe("string");
        expect(String(source?.[k]).length, `${name}: triggerSource.${k} is empty`).toBeGreaterThan(0);
      }
    }
  });
});
