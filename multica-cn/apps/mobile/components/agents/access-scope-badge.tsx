/**
 * Three-state access-scope badge (RUYI-346) — the mobile counterpart of the
 * access chip on web's agent rows. The value comes from the SHARED pure
 * derivation `effectiveAccessScope` (@multica/core/agents/effective-access,
 * MUL-3963) so the three states and their fallback match web exactly.
 *
 * Unknown values (a scope string this client doesn't know) render the raw
 * value in the muted style instead of crashing — root CLAUDE.md "API
 * Response Compatibility".
 */
import { View } from "react-native";
import {
  effectiveAccessScope,
  type AccessScope,
} from "@multica/core/agents";
import type { Agent } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { useT } from "@/lib/use-t";

export function AccessScopeBadge({
  agent,
}: {
  agent: Pick<Agent, "permission_mode" | "invocation_targets">;
}) {
  const { t } = useT("agents");
  const scope: AccessScope = effectiveAccessScope(
    agent.permission_mode,
    agent.invocation_targets,
  );
  // wire 值是连字符（specific-people/owner-only），资源 key 是下划线
  // （specific_people/owner_only）——直接拼会 miss 而裸奔 wire 值。
  const label = t(`access.scope_labels.${scope.replace(/-/g, "_")}`, {
    defaultValue: scope,
  });

  return (
    <View className="rounded-md bg-muted px-1.5 py-0.5 self-start">
      <Text className="text-[11px] text-muted-foreground" numberOfLines={1}>
        {label}
      </Text>
    </View>
  );
}
