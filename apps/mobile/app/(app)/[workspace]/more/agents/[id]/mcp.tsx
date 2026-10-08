/**
 * Agent MCP sub-screen (`more/agents/[id]/mcp`, RUYI-418 B3) — mobile subset
 * of web's agent MCP config tab. Three sources, one section each:
 *
 *  1. The agent's own `mcp_config` JSON (managed servers): add / edit /
 *     delete via UpdateAgentRequest's replace-semantics `mcp_config` field.
 *     Redacted payloads (non-owner viewers) render a read-only notice —
 *     an empty editor that a Save could clobber is worse than none.
 *  2. Workspace library servers assigned to this agent: per-assignment
 *     toggle + remove, add from the unassigned remainder (GH #6062).
 *  3. The local runtime's read-only MCP inventory, with the same notice
 *     ladder as web (missing / forbidden / offline / unsupported / empty).
 *
 * Permission: inventory is readable by anyone who can open the agent (it
 * carries no credential material); every write affordance is hidden for
 * non-managers — the same "hide, not disable" rule as env/webhooks. The
 * pure document model is the mobile mirror of the views module (see
 * lib/mcp-config-model.ts for the boundary note).
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { router, useLocalSearchParams } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import type { AgentRuntime, WorkspaceMcpServer } from "@multica/core/types";
import { ApiError } from "@/data/api";
import {
  isRuntimeUsableForUser,
  runtimeDisplayLabel,
} from "@multica/core/runtimes";
import { Text } from "@/components/ui/text";
import { Switch } from "@/components/ui/switch";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { agentDetailOptions } from "@/data/queries/agents";
import {
  agentMcpOptions,
  workspaceMcpServersOptions,
} from "@/data/queries/agent-mcp";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { runtimeCapabilitiesOptions } from "@/data/queries/runtime-skills";
import {
  useAddAgentMcpServer,
  useSetAgentMcpServerEnabled,
  useRemoveAgentMcpServer,
} from "@/data/mutations/agent-mcp";
import { useUpdateAgent } from "@/data/mutations/agents";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useCanManageAgent } from "@/components/agents/use-can-manage-agent";
import {
  listManagedMcpServers,
  removeManagedMcpServer,
  upsertManagedMcpServer,
  type ManagedMcpServer,
} from "@/lib/mcp-config-model";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

export default function AgentMcpScreen() {
  const insets = useSafeAreaInsets();
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const me = useAuthStore((s) => s.user);
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");

  const { data: agent } = useQuery(agentDetailOptions(wsId, agentId));
  const canManage = useCanManageAgent(agent);

  const { data: runtimes } = useQuery(runtimeListOptions(wsId));
  const runtime: AgentRuntime | null = useMemo(
    () =>
      agent?.runtime_id
        ? runtimes?.find((r) => r.id === agent.runtime_id) ?? null
        : null,
    [agent?.runtime_id, runtimes],
  );
  const canReadRuntime =
    runtime != null && isRuntimeUsableForUser(runtime, me?.id ?? null);
  const runtimeId =
    runtime?.runtime_mode === "local" &&
    runtime.status === "online" &&
    canReadRuntime
      ? runtime.id
      : null;
  const runtimeQuery = useQuery(runtimeCapabilitiesOptions(runtimeId));

  const assignedQuery = useQuery(agentMcpOptions(wsId, agentId));
  const libraryQuery = useQuery(workspaceMcpServersOptions(wsId));
  const addServer = useAddAgentMcpServer(agentId);
  const setServerEnabled = useSetAgentMcpServerEnabled(agentId);
  const removeServer = useRemoveAgentMcpServer(agentId);
  const update = useUpdateAgent(agentId);

  const redacted = agent?.mcp_config_redacted === true;
  const managedServers = useMemo(
    () => listManagedMcpServers(agent?.mcp_config),
    [agent?.mcp_config],
  );
  const managedNames = useMemo(
    () => new Set(managedServers.map((server) => server.name)),
    [managedServers],
  );
  const assignedServers = useMemo(
    () => assignedQuery.data ?? [],
    [assignedQuery.data],
  );
  const assignedIds = useMemo(
    () => new Set(assignedServers.map((server) => server.id)),
    [assignedServers],
  );
  // An entry the agent already has is not offered again (web parity).
  const availableServers = useMemo(
    () => (libraryQuery.data ?? []).filter((s) => !assignedIds.has(s.id)),
    [libraryQuery.data, assignedIds],
  );
  // The daemon merges runtime < (assigned workspace servers + the agent's
  // own); a disabled assignment shadows nothing (web parity).
  const effectiveNames = useMemo(() => {
    const names = new Set(managedNames);
    for (const server of assignedServers) {
      if (server.enabled !== false) names.add(server.name);
    }
    return names;
  }, [managedNames, assignedServers]);

  // Managed-server editor: null = closed; {previous: null} = adding.
  const [editing, setEditing] = useState<{
    previous: ManagedMcpServer | null;
  } | null>(null);
  const [deleting, setDeleting] = useState<ManagedMcpServer | null>(null);
  const [addPickerOpen, setAddPickerOpen] = useState(false);

  const alert = (fallback: string, error?: unknown) =>
    Alert.alert(
      error instanceof Error && error.message ? error.message : fallback,
    );

  const saveManaged = async (
    previous: ManagedMcpServer | null,
    name: string,
    config: Record<string, unknown>,
  ) => {
    if (!agent) return;
    const next = upsertManagedMcpServer(
      agent.mcp_config,
      previous,
      name,
      config,
    );
    try {
      await update.mutateAsync({ mcp_config: next });
      setEditing(null);
    } catch (error) {
      alert(t("tab_body.mcp_config.save_failed_toast", "Failed to save MCP config"), error);
    }
  };

  const deleteManaged = async (server: ManagedMcpServer) => {
    if (!agent) return;
    setDeleting(server);
    try {
      await update.mutateAsync({
        mcp_config: removeManagedMcpServer(agent.mcp_config, server),
      });
    } catch (error) {
      alert(t("tab_body.mcp_config.delete_failed_toast", "Failed to delete MCP server"), error);
    } finally {
      setDeleting(null);
    }
  };

  const closeLabel = t("create_dialog.cancel", "Cancel");

  return (
    <View className="flex-1 bg-background">
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false）；顶部让出系统状态栏（RUYI-563）。 */}
      <View
        className="flex-row items-center px-4 pb-2 border-b border-border"
        style={{ paddingTop: insets.top + 12 }}
      >
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
          {t("tabs.mcp_config", "MCP")}
        </Text>
        <View className="w-6" />
      </View>

      <ScrollView
        className="flex-1"
        contentContainerClassName="px-4 pt-3 pb-8 gap-5"
        keyboardShouldPersistTaps="handled"
      >
        {/* --- 1. Agent's own mcp_config --- */}
        <View className="gap-2">
          <View className="flex-row items-center justify-between">
            <Text className="text-sm font-semibold text-foreground">
              {t("tab_body.mcp_config.managed_title", "Agent's own servers")}
            </Text>
            {canManage && !redacted ? (
              <Pressable
                onPress={() => setEditing({ previous: null })}
                className="flex-row items-center gap-1 rounded-md border border-border px-2.5 py-1.5 active:bg-secondary"
                accessibilityRole="button"
                accessibilityLabel={t(
                  "tab_body.mcp_config.add_action",
                  "Add MCP",
                )}
              >
                <Ionicons
                  name="add"
                  size={14}
                  color={THEME[colorScheme].mutedForeground}
                />
                <Text className="text-xs text-muted-foreground">
                  {t("tab_body.mcp_config.add_action", "Add MCP")}
                </Text>
              </Pressable>
            ) : null}
          </View>

          {redacted ? (
            <View className="flex-row items-start gap-2 rounded-lg border border-border px-3 py-3">
              <Ionicons
                name="lock-closed-outline"
                size={16}
                color={THEME[colorScheme].mutedForeground}
              />
              <View className="flex-1 gap-1">
                <Text className="text-sm font-medium text-foreground">
                  {t(
                    "tab_body.mcp_config.redacted_title",
                    "Configured — hidden from your view",
                  )}
                </Text>
                <Text className="text-xs text-muted-foreground">
                  {t(
                    "tab_body.mcp_config.redacted_hint",
                    "Only the agent owner or a workspace admin can read this config.",
                  )}
                </Text>
              </View>
            </View>
          ) : managedServers.length > 0 ? (
            <View className="rounded-lg border border-border overflow-hidden">
              {managedServers.map((server, i) => (
                <ManagedServerRow
                  key={`${server.container}:${server.name}`}
                  server={server}
                  first={i === 0}
                  canManage={canManage}
                  deleteBusy={deleting?.name === server.name}
                  onEdit={() => setEditing({ previous: server })}
                  onDelete={() => void deleteManaged(server)}
                />
              ))}
            </View>
          ) : (
            <Notice
              text={t(
                "tab_body.mcp_config.managed_empty",
                "No MCP servers are managed by Multica yet.",
              )}
            />
          )}

          {editing ? (
            <ManagedServerEditor
              agentNames={managedNames}
              previous={editing.previous}
              saving={update.isPending}
              onCancel={() => setEditing(null)}
              onSave={(name, config) =>
                void saveManaged(editing.previous, name, config)
              }
            />
          ) : null}
        </View>

        {/* --- 2. Workspace library servers assigned to this agent --- */}
        <View className="gap-2">
          <View className="flex-row items-center justify-between">
            <Text className="text-sm font-semibold text-foreground">
              {t("tab_body.mcp_config.workspace_title", "From the workspace library")}
            </Text>
            {canManage && availableServers.length > 0 ? (
              <Pressable
                onPress={() => setAddPickerOpen((v) => !v)}
                className="flex-row items-center gap-1 rounded-md border border-border px-2.5 py-1.5 active:bg-secondary"
                accessibilityRole="button"
                accessibilityLabel={t(
                  "tab_body.mcp_config.workspace_add_action",
                  "Add from workspace",
                )}
              >
                <Ionicons
                  name="add"
                  size={14}
                  color={THEME[colorScheme].mutedForeground}
                />
                <Text className="text-xs text-muted-foreground">
                  {t(
                    "tab_body.mcp_config.workspace_add_action",
                    "Add from workspace",
                  )}
                </Text>
              </Pressable>
            ) : null}
          </View>

          {addPickerOpen ? (
            <View className="rounded-md border border-border overflow-hidden">
              {availableServers.map((server, i) => (
                <Pressable
                  key={server.id}
                  onPress={() => {
                    setAddPickerOpen(false);
                    addServer.mutate(server.id, {
                      onError: (error) =>
                        alert(
                          t(
                            "tab_body.mcp_config.workspace_action_failed",
                            "Could not update this agent's MCP servers",
                          ),
                          error,
                        ),
                    });
                  }}
                  className={`flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary ${
                    i > 0 ? "border-t border-border" : ""
                  }`}
                  accessibilityRole="button"
                  accessibilityLabel={server.name}
                >
                  <Text numberOfLines={1} className="flex-1 text-sm text-foreground">
                    {server.name}
                  </Text>
                  <Text className="uppercase text-xs text-muted-foreground">
                    {server.transport || "unknown"}
                  </Text>
                </Pressable>
              ))}
            </View>
          ) : null}

          {assignedQuery.isLoading ? (
            <Notice
              loading
              text={t(
                "tab_body.mcp_config.workspace_loading",
                "Loading assigned servers…",
              )}
            />
          ) : assignedServers.length > 0 ? (
            <View className="rounded-lg border border-border overflow-hidden">
              {assignedServers.map((server, i) => (
                <AssignedServerRow
                  key={server.id}
                  server={server}
                  first={i === 0}
                  overridden={managedNames.has(server.name)}
                  canManage={canManage}
                  busy={setServerEnabled.isPending || removeServer.isPending}
                  onToggle={(enabled) =>
                    setServerEnabled.mutate(
                      { serverId: server.id, enabled },
                      {
                        onError: (error) =>
                          alert(
                            t(
                              "tab_body.mcp_config.workspace_action_failed",
                              "Could not update this agent's MCP servers",
                            ),
                            error,
                          ),
                      },
                    )
                  }
                  onRemove={() =>
                    removeServer.mutate(server.id, {
                      onError: (error) =>
                        alert(
                          t(
                            "tab_body.mcp_config.workspace_action_failed",
                            "Could not update this agent's MCP servers",
                          ),
                          error,
                        ),
                    })
                  }
                />
              ))}
            </View>
          ) : (
            <Notice
              text={
                (libraryQuery.data ?? []).length === 0
                  ? t(
                      "tab_body.mcp_config.workspace_library_empty",
                      "This workspace has no MCP servers to assign yet. Add them in workspace Settings → MCP.",
                    )
                  : t(
                      "tab_body.mcp_config.workspace_none_assigned",
                      "No workspace MCP servers assigned to this agent yet.",
                    )
              }
            />
          )}
        </View>

        {/* --- 3. Runtime read-only inventory --- */}
        <View className="gap-2">
          <View className="flex-row items-center justify-between">
            <Text
              numberOfLines={1}
              className="flex-1 mr-2 text-sm font-semibold text-foreground"
            >
              {runtime
                ? t("tab_body.mcp_config.runtime_title_named", "Inherited from {{runtime}}", {
                    runtime: runtimeDisplayLabel(runtime),
                  })
                : t("tab_body.mcp_config.runtime_title", "Inherited from runtime")}
            </Text>
            {runtimeId ? (
              <Pressable
                onPress={() => void runtimeQuery.refetch()}
                disabled={runtimeQuery.isFetching}
                className="flex-row items-center gap-1"
                accessibilityRole="button"
                accessibilityLabel={t(
                  "tab_body.mcp_config.refresh_action",
                  "Refresh",
                )}
              >
                <Ionicons
                  name="refresh"
                  size={14}
                  color={THEME[colorScheme].mutedForeground}
                />
                <Text className="text-xs text-muted-foreground">
                  {t("tab_body.mcp_config.refresh_action", "Refresh")}
                </Text>
              </Pressable>
            ) : null}
          </View>

          {!runtime ? (
            <Notice
              text={t(
                "tab_body.mcp_config.runtime_missing",
                "Assign a local runtime to discover inherited MCP servers.",
              )}
            />
          ) : !canReadRuntime ? (
            <Notice
              text={t(
                "tab_body.mcp_config.runtime_forbidden",
                "You don't have access to this runtime.",
              )}
            />
          ) : runtime.status !== "online" ? (
            <Notice
              text={t(
                "tab_body.mcp_config.runtime_offline",
                "The local runtime is offline. Reconnect it to refresh inherited MCP servers.",
              )}
            />
          ) : runtimeQuery.isLoading ? (
            <Notice
              loading
              text={t(
                "tab_body.mcp_config.runtime_discovering",
                "Discovering MCP servers from the local runtime…",
              )}
            />
          ) : runtimeQuery.isError ? (
            <Notice
              text={
                runtimeQuery.error instanceof ApiError &&
                runtimeQuery.error.status === 403
                  ? t(
                      "tab_body.mcp_config.runtime_forbidden",
                      "You don't have access to this runtime.",
                    )
                  : t(
                      "tab_body.mcp_config.runtime_failed",
                      "Couldn't discover runtime MCP servers. Try again.",
                    )
              }
            />
          ) : runtimeQuery.data?.mcpSupported !== true ? (
            <Notice
              text={t(
                "tab_body.mcp_config.runtime_unsupported",
                "This runtime doesn't report MCP servers.",
              )}
            />
          ) : (runtimeQuery.data.mcpServers ?? []).length === 0 ? (
            <Notice
              text={t(
                "tab_body.mcp_config.runtime_empty",
                "No MCP servers were found in the runtime's user configuration.",
              )}
            />
          ) : (
            <View className="rounded-lg border border-border overflow-hidden">
              {runtimeQuery.data.mcpServers.map((server, i) => (
                <InventoryRow
                  key={server.name}
                  name={server.name}
                  transport={server.transport || "unknown"}
                  source={server.source}
                  enabled={server.enabled}
                  overridden={effectiveNames.has(server.name)}
                  first={i === 0}
                  disabledLabel={t(
                    "tab_body.mcp_config.runtime_disabled_badge",
                    "Off in runtime",
                  )}
                  overriddenLabel={t(
                    "tab_body.mcp_config.runtime_overridden_badge",
                    "Overridden by Multica",
                  )}
                />
              ))}
            </View>
          )}
        </View>
      </ScrollView>
    </View>
  );
}

function Notice({ text, loading = false }: { text: string; loading?: boolean }) {
  const { colorScheme } = useColorScheme();
  return (
    <View className="flex-row items-center gap-2 rounded-lg border border-dashed border-border px-3 py-4">
      {loading ? (
        <Ionicons
          name="sync"
          size={14}
          color={THEME[colorScheme].mutedForeground}
        />
      ) : (
        <Ionicons
          name="server-outline"
          size={14}
          color={THEME[colorScheme].mutedForeground}
        />
      )}
      <Text className="flex-1 text-xs text-muted-foreground">{text}</Text>
    </View>
  );
}

function ManagedServerRow({
  server,
  first,
  canManage,
  deleteBusy,
  onEdit,
  onDelete,
}: {
  server: ManagedMcpServer;
  first: boolean;
  canManage: boolean;
  deleteBusy: boolean;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");
  return (
    <View
      className={`flex-row items-center gap-3 px-3 py-2.5 ${
        first ? "" : "border-t border-border"
      }`}
    >
      <View className="flex-1 gap-0.5">
        <Text numberOfLines={1} className="text-sm font-medium text-foreground">
          {server.name}
        </Text>
        <Text className="uppercase text-xs text-muted-foreground">
          {server.transport}
        </Text>
      </View>
      {!server.enabled ? (
        <Text className="rounded border border-border px-1.5 py-0.5 text-xs text-muted-foreground">
          {t("tab_body.mcp_config.agent_disabled_badge", "Off for agent")}
        </Text>
      ) : null}
      {canManage ? (
        <View className="flex-row items-center gap-3">
          <Pressable
            onPress={onEdit}
            hitSlop={8}
            accessibilityRole="button"
            accessibilityLabel={`${t("tab_body.mcp_config.edit_aria", "Edit MCP server")} ${server.name}`}
          >
            <Ionicons
              name="pencil-outline"
              size={16}
              color={THEME[colorScheme].mutedForeground}
            />
          </Pressable>
          <Pressable
            onPress={onDelete}
            disabled={deleteBusy}
            hitSlop={8}
            accessibilityRole="button"
            accessibilityLabel={`${t("tab_body.mcp_config.delete_aria", "Delete MCP server")} ${server.name}`}
          >
            <Ionicons
              name="trash-outline"
              size={16}
              color={THEME[colorScheme].destructive}
            />
          </Pressable>
        </View>
      ) : null}
    </View>
  );
}

/**
 * Name + config JSON editor for the agent's own servers. Web uses a
 * structured per-field dialog; on the phone the same upsert semantics ride
 * a JSON field — the document model (mcp-config-model) is shared logic, so
 * the wire format stays identical.
 */
function ManagedServerEditor({
  agentNames,
  previous,
  saving,
  onCancel,
  onSave,
}: {
  agentNames: Set<string>;
  previous: ManagedMcpServer | null;
  saving: boolean;
  onCancel: () => void;
  onSave: (name: string, config: Record<string, unknown>) => void;
}) {
  const { t } = useT("agents");
  const [name, setName] = useState(previous?.name ?? "");
  const [configText, setConfigText] = useState(
    previous ? JSON.stringify(previous.config, null, 2) : "{\n}",
  );
  const trimmed = name.trim();
  const nameError =
    trimmed.length === 0
      ? "required"
      : agentNames.has(trimmed) && trimmed !== previous?.name
        ? "duplicate"
        : null;
  let configError: "invalid" | null = null;
  let parsed: Record<string, unknown> | null = null;
  try {
    const value = JSON.parse(configText) as unknown;
    if (value && typeof value === "object" && !Array.isArray(value)) {
      parsed = value as Record<string, unknown>;
    } else {
      configError = "invalid";
    }
  } catch {
    configError = "invalid";
  }
  const valid = !nameError && !configError && parsed !== null;

  return (
    <View className="rounded-lg border border-border px-3 py-3 gap-2.5">
      <Text className="text-sm font-semibold text-foreground">
        {previous
          ? t("tab_body.mcp_config.edit_aria", "Edit MCP server")
          : t("tab_body.mcp_config.add_action", "Add MCP")}
      </Text>
      <TextInput
        value={name}
        onChangeText={setName}
        placeholder={t("tab_body.mcp_config.dialog_name_label", "Name")}
        placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
        autoCapitalize="none"
        autoCorrect={false}
        className="rounded-md border border-border px-3 py-2 text-sm text-foreground"
      />
      {nameError === "duplicate" ? (
        <Text className="text-xs text-destructive">
          {t(
            "tab_body.mcp_config.dialog_name_duplicate",
            "A server with this name already exists.",
          )}
        </Text>
      ) : null}
      <TextInput
        value={configText}
        onChangeText={setConfigText}
        placeholder={'{ "command": "…" }'}
        placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
        autoCapitalize="none"
        autoCorrect={false}
        multiline
        textAlignVertical="top"
        className="rounded-md border border-border px-3 py-2 text-xs font-mono text-foreground"
      />
      {configError === "invalid" ? (
        <Text className="text-xs text-destructive">
          {t(
            "tab_body.mcp_config.invalid_json",
            "Invalid JSON: {{error}}",
          )}
        </Text>
      ) : null}
      <View className="flex-row items-center justify-end gap-2">
        <Pressable
          onPress={onCancel}
          disabled={saving}
          className="rounded-lg border border-border px-4 py-2 active:opacity-80"
          accessibilityRole="button"
          accessibilityLabel={t("create_dialog.cancel", "Cancel")}
        >
          <Text className="text-sm text-foreground">
            {t("create_dialog.cancel", "Cancel")}
          </Text>
        </Pressable>
        <Pressable
          onPress={() => valid && trimmed && parsed && onSave(trimmed, parsed)}
          disabled={!valid || saving}
          className={`rounded-lg bg-primary px-4 py-2 ${
            valid && !saving ? "active:opacity-80" : "opacity-50"
          }`}
          accessibilityRole="button"
          accessibilityLabel={t("tab_body.common.save", "Save")}
        >
          <Text className="text-sm font-medium text-primary-foreground">
            {t("tab_body.common.save", "Save")}
          </Text>
        </Pressable>
      </View>
    </View>
  );
}

function AssignedServerRow({
  server,
  first,
  overridden,
  canManage,
  busy,
  onToggle,
  onRemove,
}: {
  server: WorkspaceMcpServer;
  first: boolean;
  overridden: boolean;
  canManage: boolean;
  busy: boolean;
  onToggle: (enabled: boolean) => void;
  onRemove: () => void;
}) {
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");
  const enabled = server.enabled !== false;
  return (
    <View
      className={`flex-row items-center gap-3 px-3 py-2.5 ${
        first ? "" : "border-t border-border"
      }`}
    >
      <View className="flex-1 gap-0.5">
        <View className="flex-row items-center gap-1.5">
          <Text numberOfLines={1} className="text-sm font-medium text-foreground">
            {server.name}
          </Text>
          {overridden ? (
            <Text className="rounded border border-border px-1.5 py-0.5 text-xs text-muted-foreground">
              {t(
                "tab_body.mcp_config.workspace_overridden_badge",
                "Overridden",
              )}
            </Text>
          ) : null}
        </View>
        <Text className="uppercase text-xs text-muted-foreground">
          {server.transport || "unknown"}
        </Text>
      </View>
      {canManage ? (
        <>
          <Pressable
            onPress={onRemove}
            disabled={busy}
            hitSlop={8}
            accessibilityRole="button"
            accessibilityLabel={t(
              "tab_body.mcp_config.workspace_remove_aria",
              "Remove {{name}} from this agent",
              { name: server.name },
            )}
          >
            <Ionicons
              name="trash-outline"
              size={16}
              color={THEME[colorScheme].destructive}
            />
          </Pressable>
          <Switch
            checked={enabled}
            disabled={busy}
            onCheckedChange={onToggle}
            aria-label={t(
              "tab_body.mcp_config.workspace_toggle_aria",
              "Enable {{name}} for this agent",
              { name: server.name },
            )}
          />
        </>
      ) : !enabled ? (
        <Text className="rounded border border-border px-1.5 py-0.5 text-xs text-muted-foreground">
          {t("tab_body.mcp_config.workspace_disabled_badge", "Disabled")}
        </Text>
      ) : null}
    </View>
  );
}

function InventoryRow({
  name,
  transport,
  source,
  enabled,
  overridden,
  first,
  disabledLabel,
  overriddenLabel,
}: {
  name: string;
  transport: string;
  source?: string;
  enabled: boolean;
  overridden: boolean;
  first: boolean;
  disabledLabel: string;
  overriddenLabel: string;
}) {
  return (
    <View
      className={`flex-row items-center gap-3 px-3 py-2.5 ${
        first ? "" : "border-t border-border"
      }`}
    >
      <View className="flex-1 gap-0.5">
        <Text numberOfLines={1} className="text-sm font-medium text-foreground">
          {name}
        </Text>
        <Text className="text-xs text-muted-foreground">
          <Text className="uppercase">{transport}</Text>
          {source ? ` · ${source}` : ""}
        </Text>
      </View>
      {overridden ? (
        <Text className="rounded border border-border px-1.5 py-0.5 text-xs text-muted-foreground">
          {overriddenLabel}
        </Text>
      ) : !enabled ? (
        <Text className="rounded border border-border px-1.5 py-0.5 text-xs text-muted-foreground">
          {disabledLabel}
        </Text>
      ) : null}
    </View>
  );
}
