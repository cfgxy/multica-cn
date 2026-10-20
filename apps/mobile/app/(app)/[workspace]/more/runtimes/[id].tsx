import { useMemo, useState } from "react";
import { ActivityIndicator, Alert, ScrollView, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { runtimeDisplayLabel } from "@multica/core/runtimes";
import type { RuntimeDevice } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { TextField } from "@/components/ui/text-field";
import { Separator } from "@/components/ui/separator";
import { agentListOptions } from "@/data/queries/agents";
import { memberListOptions } from "@/data/queries/members";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { useDeleteRuntime, useUnbindAgentsAndDeleteRuntime, useUpdateRuntime } from "@/data/mutations/runtime-management";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";
import { runtimeCredentialDisplayState, runtimeNodeKind } from "@/lib/runtime-node";

export default function RuntimeNodeDetailPage() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const runtimeId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((state) => state.currentWorkspaceId);
  const user = useAuthStore((state) => state.user);
  const { t } = useT("runtimes");
  const runtimes = useQuery(runtimeListOptions(wsId));
  const members = useQuery(memberListOptions(wsId));
  const agents = useQuery(agentListOptions(wsId));
  const update = useUpdateRuntime();
  const strictDelete = useDeleteRuntime();
  const confirmedDelete = useUnbindAgentsAndDeleteRuntime();
  const [editing, setEditing] = useState(false);
  const [customName, setCustomName] = useState("");

  const runtime = runtimes.data?.find((item) => item.id === runtimeId);
  const currentMember = members.data?.find((member) => member.user_id === user?.id);
  const isAdmin = currentMember?.role === "owner" || currentMember?.role === "admin";
  const canEdit = isAdmin || (!!user && runtime?.owner_id === user.id);
  const boundAgents = useMemo(
    () => (agents.data ?? []).filter((agent) => agent.runtime_id === runtimeId && !agent.archived_at),
    [agents.data, runtimeId],
  );
  const isDeleting = strictDelete.isPending || confirmedDelete.isPending;

  if (runtimes.isLoading || members.isLoading || agents.isLoading) {
    return <View className="flex-1 items-center justify-center bg-background"><ActivityIndicator /></View>;
  }
  if (!runtime) {
    return (
      <View className="flex-1 items-center justify-center bg-background px-8">
        <Text className="text-sm text-muted-foreground">{t("machine.not_found_hint", "It may be offline, removed, or no longer available to you.")}</Text>
      </View>
    );
  }

  const credentialLabel = credentialStatusLabel(runtime.credential_status, t);

  const saveName = () => {
    if (!canEdit || update.isPending) return;
    update.mutate(
      { runtimeId, customName: customName.trim() },
      {
        onSuccess: () => setEditing(false),
        onError: () => Alert.alert(t("mobile_nodes.save_failed", "Couldn't save the runtime name")),
      },
    );
  };

  const removeRuntime = () => {
    if (!canEdit || isDeleting) return;
    const names = boundAgents.map((agent) => agent.name).slice(0, 6).join("\n");
    const detail = boundAgents.length > 0
      ? t("mobile_nodes.delete_bound_description", "Bound agents will be kept but unbound; queued and running tasks will be cancelled.") + `\n\n${names}`
      : t("mobile_nodes.delete_description", "This action cannot be undone.");
    Alert.alert(
      boundAgents.length > 0
        ? t("mobile_nodes.delete_bound_title", "Unbind agents and delete runtime?")
        : t("mobile_nodes.delete_title", "Delete runtime?"),
      detail,
      [
        { text: t("common:cancel", "Cancel"), style: "cancel" },
        {
          text: t("common:delete", "Delete"),
          style: "destructive",
          onPress: () => {
            if (boundAgents.length > 0) {
              confirmedDelete.mutate(
                { runtimeId, expectedActiveAgentIds: boundAgents.map((agent) => agent.id) },
                {
                  onSuccess: () => router.back(),
                  onError: () => Alert.alert(t("mobile_nodes.delete_failed", "The runtime changed or could not be deleted. Refresh and review the current bindings.")),
                },
              );
            } else {
              strictDelete.mutate(runtimeId, {
                onSuccess: () => router.back(),
                onError: () => Alert.alert(t("mobile_nodes.delete_failed", "The runtime changed or could not be deleted. Refresh and review the current bindings.")),
              });
            }
          },
        },
      ],
    );
  };

  return (
    <ScrollView className="flex-1 bg-background" contentContainerClassName="gap-5 px-4 py-4">
      <View className="gap-1">
        <Text className="text-lg font-semibold text-foreground">{runtimeDisplayLabel(runtime)}</Text>
        <Text className="text-sm text-muted-foreground">{`${runtime.provider} · ${runtime.status}`}</Text>
      </View>

      <View className="overflow-hidden rounded-lg border border-border bg-card">
        <DetailRow label={t("mobile_nodes.type", "Type")} value={runtimeKindLabel(runtimeNodeKind(runtime), t)} />
        <Separator />
        <DetailRow label={t("mobile_nodes.credential", "Credential status")} value={credentialLabel} />
        <Separator />
        <DetailRow label={t("mobile_nodes.agents", "Bound agents")} value={String(boundAgents.length)} />
        <Separator />
        <DetailRow label={t("mobile_nodes.visibility", "Visibility")} value={runtime.visibility === "public" ? t("detail.visibility_label.public", "Public") : t("detail.visibility_label.private", "Private")} />
      </View>

      {canEdit ? (
        <View className="gap-3">
          {editing ? (
            <View className="gap-3 rounded-lg border border-border bg-card p-4">
              <Text className="text-sm font-medium text-foreground">{t("mobile_nodes.rename", "Rename runtime")}</Text>
              <TextField
                value={customName}
                onChangeText={setCustomName}
                placeholder={runtimeDisplayLabel(runtime)}
                accessibilityLabel={t("mobile_nodes.name_label", "Runtime name")}
                maxLength={120}
              />
              <View className="flex-row gap-2">
                <Button variant="outline" className="flex-1" onPress={() => setEditing(false)}>
                  <Text>{t("common:cancel", "Cancel")}</Text>
                </Button>
                <Button className="flex-1" disabled={!customName.trim() || update.isPending} onPress={saveName}>
                  <Text>{update.isPending ? t("mobile_nodes.saving", "Saving…") : t("common:save", "Save")}</Text>
                </Button>
              </View>
            </View>
          ) : (
            <Button
              variant="outline"
              onPress={() => {
                setCustomName(runtime.custom_name ?? "");
                setEditing(true);
              }}
            >
              <Ionicons name="pencil-outline" size={17} />
              <Text>{t("mobile_nodes.rename", "Rename runtime")}</Text>
            </Button>
          )}
          {!runtime.profile_id ? (
            <Button variant="destructive" disabled={isDeleting} onPress={removeRuntime}>
              <Ionicons name="trash-outline" size={17} />
              <Text>{isDeleting ? t("mobile_nodes.deleting", "Deleting…") : t("common:delete", "Delete")}</Text>
            </Button>
          ) : null}
        </View>
      ) : null}
    </ScrollView>
  );
}

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <View className="flex-row items-center justify-between gap-4 px-4 py-3">
      <Text className="text-sm text-muted-foreground">{label}</Text>
      <Text numberOfLines={2} className="flex-1 text-right text-sm text-foreground">{value}</Text>
    </View>
  );
}

function credentialStatusLabel(
  status: RuntimeDevice["credential_status"],
  t: (key: string, fallback: string) => string,
): string {
  const state = runtimeCredentialDisplayState(status);
  if (state === "configured") return t("mobile_nodes.credential_configured", "Configured");
  if (state === "error") return t("mobile_nodes.credential_error", "Issue");
  return t("mobile_nodes.credential_missing", "Not configured");
}

function runtimeKindLabel(
  kind: "voice" | "cli" | "daemon",
  t: (key: string, fallback: string) => string,
): string {
  if (kind === "voice") return t("mobile_nodes.voice", "Voice");
  return kind === "daemon"
    ? t("machine.metrics.local_daemon", "Local daemon")
    : t("machine.metrics.cli", "CLI");
}
