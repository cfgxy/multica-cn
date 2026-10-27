/**
 * Agent env editor pure helpers (RUYI-346, design §6) — the wire semantics
 * of web's `packages/views/agents/components/tabs/env-tab.tsx`:
 *
 *  - Save replaces the map wholesale (PUT /env); we never emit the "****"
 *    mask — the editor only holds revealed plaintext.
 *  - Blank-key rows are dropped and keys trimmed, the same normalization
 *    `entriesToEnvMap` does on web.
 *  - Duplicate keys (after trimming) block Save — the server would
 *    silently last-write-win (`duplicate_keys_toast` parity).
 *
 * Pure functions so the vitest node lane can cover them (see
 * vitest.config.ts — lib/ is the no-RN-runtime lane).
 */

export interface EnvRow {
  /** Row identity for React keys — not sent to the server. */
  rowId: number;
  key: string;
  value: string;
}

let nextEnvRowId = 1;

/** Fresh blank row for the "Add variable" affordance — same id counter as
 *  envMapToRows so rowIds stay unique across seeds and manual adds. */
export function newEnvRow(): EnvRow {
  return { rowId: nextEnvRowId++, key: "", value: "" };
}

/** Agent payload map → editable rows, skipping blank keys. */
export function envMapToRows(map: Record<string, string>): EnvRow[] {
  return Object.entries(map)
    .filter(([k]) => k.trim() !== "")
    .map(([key, value]) => ({ rowId: nextEnvRowId++, key, value }));
}

/** Editable rows → wire map. Blank-key rows are dropped, keys trimmed. */
export function envRowsToMap(rows: EnvRow[]): Record<string, string> {
  const map: Record<string, string> = {};
  for (const row of rows) {
    const key = row.key.trim();
    if (key) map[key] = row.value;
  }
  return map;
}

/** Duplicate keys after trimming — Save is blocked while any exist. */
export function hasDuplicateKeys(rows: EnvRow[]): boolean {
  const seen = new Set<string>();
  for (const row of rows) {
    const key = row.key.trim();
    if (!key) continue;
    if (seen.has(key)) return true;
    seen.add(key);
  }
  return false;
}
