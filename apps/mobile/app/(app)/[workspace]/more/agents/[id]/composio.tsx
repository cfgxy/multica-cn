/**
 * Agent Composio MCP-apps sub-screen (`more/agents/[id]/composio`,
 * RUYI-418 B3) — mobile mirror of web's agent-mcp-tab (creator-only).
 * Lets the agent owner pick which of their own active Composio connections
 * this agent may mount as MCP servers; the selection is written to
 * `agent.composio_toolkit_allowlist` via the regular UpdateAgent path
 * (no dedicated endpoint, MUL-3870).
 *
 * Gating mirrors web exactly: the entry row on the detail screen renders
 * only for the owner with the composio_mcp_apps feature flag on, and this
 * screen defensively re-checks both. A redacted allowlist (stale cache,
 * future fan-out) renders the same read-only notice as web instead of an
 * editor a Save could clobber. The shared-agent warning (MUL-3963) carries
 * over verbatim: anyone who can invoke the agent can drive these apps on
 * the owner's behalf.
 */
import { useMemo } from "react";
import { Alert, Pressable, ScrollView, View } from "react-native";
import { router, useLocalSearchParams } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import { COMPOSIO_MCP_APPS_FLAG } from "@multica/core/feature-flags";
import { Text } from "@/components/ui/text";
import { agentDetailOptions } from "@/data/queries/agents";
import { appConfigOptions } from "@/data/queries/billing";
import { composioConnectionsOptions } from "@/data/queries/composio";
import { useUpdateAgent } from "@/data/mutations/agents";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

export default function AgentComposioScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");

  const { data: agent } = useQuery(agentDetailOptions(wsId, agentId));
  const { data: appConfig } = useQuery(appConfigOptions());
  const composioEnabled =
    appConfig?.feature_flags?.[COMPOSIO_MCP_APPS_FLAG] === true;
  const update = useUpdateAgent(agentId);
  const connectionsQuery = useQuery({
    ...composioConnectionsOptions(),
    enabled: composioEnabled,
  });

  // Only ACTIVE connections are selectable — an expired / revoked
  // connection can't back an MCP mount (web parity). Dedupe by slug.
  const activeSlugs = useMemo(() => {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const c of connectionsQuery.data ?? []) {
      if (c.status !== "active") continue;
      if (seen.has(c.toolkit_slug)) continue;
      seen.add(c.toolkit_slug);
      out.push(c.toolkit_slug);
    }
    return out;
  }, [connectionsQuery.data]);

  const allowlist = useMemo(
    () => agent?.composio_toolkit_allowlist ?? [],
    [agent?.composio_toolkit_allowlist],
  );

  // Shared-agent warning (web parity): non-private agent + something to
  // enable means other invokers can reach these apps.
  const isPrivate = agent?.permission_mode === "private";
  const isWorkspacePublic =
    agent?.permission_mode === "public_to" &&
    (agent?.invocation_targets ?? []).some(
      (target) => target.target_type === "workspace",
    );
  const showSharedWarning =
    !isPrivate && (allowlist.length > 0 || activeSlugs.length > 0);

  const handleToggle = (slug: string, checked: boolean) => {
    const set = new Set(allowlist);
    if (checked) set.add(slug);
    else set.delete(slug);
    update.mutate(
      { composio_toolkit_allowlist: Array.from(set) },
      {
        onError: () =>
          Alert.alert(
            t("tab_body.composio_mcp.save_failed_toast", "Couldn't save — please try again"),
          ),
      },
    );
  };

  const closeLabel = t("create_dialog.cancel", "Cancel");

  return (
    <View className="flex-1 bg-background">
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false） */}
      <View className="flex-row items-center px-4 pt-3 pb-2 border-b border-border">
        <Pressable
          onPress={() => router.back()}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel={closeLabel}
        >
          <Ionicons
            name="close"
            size={22}
            color={THEME[colorScheme].foreground}
          />
        </Pressable>
        <Text className="flex-1 text-center text-lg font-semibold text-foreground">
          {t("tabs.composio_mcp", "MCP Apps")}
        </Text>
        <View className="w-6" />
      </View>

      {!composioEnabled ? (
        // Flag off = the entry should never have been reachable; render the
        // same empty shell web's null return leaves behind.
        <View className="flex-1" />
      ) : (
        <ScrollView
          className="flex-1"
          contentContainerClassName="px-4 pt-3 pb-8 gap-3"
        >
          <Text className="text-xs text-muted-foreground leading-4">
            {t(
              "tab_body.composio_mcp.subtitle",
              "Check a toolkit to let this agent mount it as an MCP server — but only when you (its creator) are the one who triggered the run, directly or down a sub-agent chain.",
            )}
          </Text>

          {agent?.composio_toolkit_allowlist_redacted === true ? (
            <View className="gap-1 rounded-lg border border-border px-3 py-3">
              <View className="flex-row items-center gap-2">
                <Ionicons
                  name="lock-closed-outline"
                  size={14}
                  color={THEME[colorScheme].mutedForeground}
                />
                <Text className="text-sm font-medium text-foreground">
                  {t(
                    "tab_body.composio_mcp.redacted_title",
                    "Configured — hidden from your view",
                  )}
                </Text>
              </View>
              <Text className="text-xs text-muted-foreground">
                {t(
                  "tab_body.composio_mcp.redacted_hint",
                  "Only the agent's creator can view or change which apps it may use.",
                )}
              </Text>
            </View>
          ) : (
            <>
              {showSharedWarning ? (
                <View className="flex-row items-start gap-2 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2">
                  <Ionicons
                    name="warning-outline"
                    size={14}
                    color={THEME[colorScheme].warning}
                  />
                  <Text className="flex-1 text-xs text-foreground">
                    {isWorkspacePublic
                      ? t(
                          "tab_body.composio_mcp.workspace_warning",
                          "Public to workspace: any workspace member may use these Composio apps through this agent.",
                        )
                      : t(
                          "tab_body.composio_mcp.shared_warning",
                          "This agent is shared. People who can run this agent may use the Composio apps you enable here. Use Private or narrow the access list if these apps expose sensitive data.",
                        )}
                  </Text>
                </View>
              ) : null}

              {connectionsQuery.isLoading ? (
                <Text className="text-sm text-muted-foreground py-2">
                  {t("tab_body.composio_mcp.loading", "Loading your connections…")}
                </Text>
              ) : connectionsQuery.isError ? (
                <Text className="text-sm text-destructive py-2">
                  {t(
                    "tab_body.composio_mcp.load_failed",
                    "Couldn't load your connected apps. Try again shortly.",
                  )}
                </Text>
              ) : activeSlugs.length === 0 ? (
                <View className="items-center gap-1 rounded-lg border border-dashed border-border px-3 py-6">
                  <Ionicons
                    name="apps-outline"
                    size={28}
                    color={THEME[colorScheme].mutedForeground}
                  />
                  <Text className="text-sm font-medium text-foreground">
                    {t(
                      "tab_body.composio_mcp.empty_title",
                      "No connected apps yet",
                    )}
                  </Text>
                  <Text className="text-xs text-muted-foreground text-center">
                    {t(
                      "tab_body.composio_mcp.empty_hint",
                      "Connect toolkits from the workspace settings' Integrations tab.",
                    )}
                  </Text>
                </View>
              ) : (
                <View className="rounded-lg border border-border overflow-hidden">
                  {activeSlugs.map((slug, i) => {
                    const checked = allowlist.includes(slug);
                    return (
                      <Pressable
                        key={slug}
                        onPress={() => handleToggle(slug, !checked)}
                        disabled={update.isPending}
                        className={`flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary ${
                          i > 0 ? "border-t border-border" : ""
                        }`}
                        accessibilityRole="checkbox"
                        accessibilityState={{ checked }}
                        accessibilityLabel={t(
                          "tab_body.composio_mcp.toggle_aria",
                          "Allow {{toolkit}} for this agent",
                          { toolkit: slug },
                        )}
                      >
                        <View
                          className={`size-5 rounded border items-center justify-center ${
                            checked
                              ? "bg-brand border-brand"
                              : "border-muted-foreground"
                          }`}
                        >
                          {checked ? (
                            <Ionicons
                              name="checkmark"
                              size={14}
                              color={THEME[colorScheme].primaryForeground}
                            />
                          ) : null}
                        </View>
                        <View className="flex-1 gap-0.5">
                          <Text
                            numberOfLines={1}
                            className="text-sm font-medium text-foreground"
                          >
                            {slug}
                          </Text>
                          <Text className="uppercase text-xs text-muted-foreground">
                            {t("tab_body.composio_mcp.connected", "Connected")}
                          </Text>
                        </View>
                        {update.isPending ? (
                          <Ionicons
                            name="sync"
                            size={14}
                            color={THEME[colorScheme].mutedForeground}
                          />
                        ) : null}
                      </Pressable>
                    );
                  })}
                </View>
              )}

              {update.isPending ? (
                <Text className="text-xs text-muted-foreground">
                  {t("tab_body.composio_mcp.saving", "Saving…")}
                </Text>
              ) : null}
            </>
          )}
        </ScrollView>
      )}
    </View>
  );
}
