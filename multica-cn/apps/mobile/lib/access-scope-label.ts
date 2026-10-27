/**
 * Access-scope badge label key (RUYI-346) — pure helper split out of
 * `components/agents/access-scope-badge.tsx` so the vitest node lane can
 * lock the wire-value → resource-key mapping (see vitest.config.ts).
 *
 * The wire values are hyphenated (`specific-people`/`owner-only`,
 * @multica/core/agents/effective-access) while the resource keys under
 * `agents:access.scope_labels` are underscored — the key must be converted
 * before lookup, or i18next misses and defaultValue leaks the raw wire
 * value to the user.
 */

import type { AccessScope } from "@multica/core/agents";

/** AccessScope wire value → `access.scope_labels.*` resource leaf key. */
export function accessScopeLabelKey(scope: AccessScope): string {
  return scope.replace(/-/g, "_");
}
