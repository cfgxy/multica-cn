import { useState } from "react";
import { ActivityIndicator, Alert, Pressable, RefreshControl, ScrollView, View } from "react-native";
import { Stack } from "expo-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Text } from "@/components/ui/text";
import { api } from "@/data/api";
import { memberListOptions } from "@/data/queries/members";
import { skillListOptions } from "@/data/queries/skills";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";

export default function SkillsPage() {
  const { t } = useT("settings");
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const userId = useAuthStore((s) => s.user?.id);
  const qc = useQueryClient();
  const skills = useQuery(skillListOptions(wsId));
  const members = useQuery(memberListOptions(wsId));
  const canRestore = members.data?.some((member) => member.user_id === userId && member.role === "owner") === true;
  return <View className="flex-1 bg-background">
    <Stack.Screen options={{ title: t("mobile_management.skills", "Skills") }} />
    <ScrollView refreshControl={<RefreshControl refreshing={skills.isRefetching} onRefresh={() => void skills.refetch()} />} contentContainerClassName="gap-3 px-4 py-4">
      {skills.isLoading ? <ActivityIndicator /> : null}
      {skills.isError ? <Pressable accessibilityRole="button" onPress={() => void skills.refetch()} className="rounded border border-border p-3"><Text className="text-sm text-destructive">{t("mobile_management.load_failed", "Could not load. Tap to retry.")}</Text></Pressable> : null}
      {(skills.data ?? []).map((skill) => <SkillRow key={skill.id} skill={skill} wsId={wsId} canRestore={canRestore} onRestored={() => { void qc.invalidateQueries({ queryKey: ["skills", wsId] }); }} />)}
      {skills.data?.length === 0 ? <Text className="text-sm text-muted-foreground">{t("mobile_management.no_skills", "No skills available")}</Text> : null}
    </ScrollView>
  </View>;
}

function SkillRow({ skill, wsId, canRestore, onRestored }: { skill: { id: string; name: string; description?: string | null }; wsId: string | null; canRestore: boolean; onRestored: () => void }) {
  const { t } = useT("settings");
  const [expanded, setExpanded] = useState(false);
  const [selectedVersion, setSelectedVersion] = useState<number | null>(null);
  const versions = useQuery({ queryKey: ["skill-versions", wsId, skill.id], queryFn: ({ signal }) => api.getSkillVersions(skill.id, { signal }), enabled: expanded && !!wsId });
  const usage = useQuery({ queryKey: ["skill-usage", wsId, skill.id], queryFn: ({ signal }) => api.getSkillUsage(skill.id, { signal }), enabled: expanded && !!wsId });
  const detail = useQuery({ queryKey: ["skill-version", wsId, skill.id, selectedVersion], queryFn: ({ signal }) => api.getSkillVersion(skill.id, selectedVersion!, { signal }), enabled: expanded && !!wsId && selectedVersion !== null });
  const restore = useMutation({ mutationFn: (version: number) => api.restoreSkillVersion(skill.id, version), onSuccess: () => { setSelectedVersion(null); onRestored(); } });
  return <View className="gap-2 border-b border-border py-3">
    <Pressable accessibilityRole="button" accessibilityState={{ expanded }} onPress={() => setExpanded((value) => !value)} className="gap-1">
      <Text className="text-sm font-semibold text-foreground">{skill.name}</Text>
      <Text className="text-xs text-muted-foreground">{skill.description || t("mobile_management.no_description", "No description")}</Text>
      {expanded ? <Text className="text-xs text-primary">{t("mobile_management.hide_details", "Hide details")}</Text> : null}
    </Pressable>
    {expanded ? <View className="gap-2 pl-2">
      {usage.isLoading || versions.isLoading ? <ActivityIndicator /> : null}
      <Text className="text-xs text-muted-foreground">{t("mobile_management.skill_usage", "Usage")}: {usage.data?.last_30_days ?? 0} / 30d · {usage.data?.assigned_agents ?? 0} {t("mobile_management.agents", "agents")}</Text>
      {(versions.data ?? []).map((version) => <View key={version.version} className="flex-row items-center justify-between gap-2">
        <Pressable accessibilityRole="button" onPress={() => setSelectedVersion(version.version)} className="flex-1 py-2"><Text className="text-sm text-foreground">v{version.version} · {version.source} · {new Date(version.created_at).toLocaleDateString()}</Text></Pressable>
        {canRestore && version.can_restore ? <Pressable accessibilityRole="button" disabled={restore.isPending} onPress={() => Alert.alert(t("mobile_management.restore_version", "Restore version"), `${t("mobile_management.restore_confirm", "Restore this skill version?")} v${version.version}`, [{ text: t("mobile_management.cancel", "Cancel"), style: "cancel" }, { text: t("mobile_management.restore", "Restore"), onPress: () => restore.mutate(version.version) }])} className="rounded bg-secondary px-3 py-2"><Text className="text-xs text-foreground">{t("mobile_management.restore", "Restore")}</Text></Pressable> : null}
      </View>)}
      {selectedVersion !== null ? detail.isLoading ? <ActivityIndicator /> : detail.isError ? <Text accessibilityRole="alert" className="text-xs text-destructive">{t("mobile_management.load_failed", "Could not load. Tap to retry.")}</Text> : <View className="max-h-64 gap-2 rounded border border-border p-3"><Text className="text-xs text-muted-foreground">{t("mobile_management.version_content", "Version content")}</Text><ScrollView nestedScrollEnabled><Text selectable className="font-mono text-xs text-foreground">{detail.data?.content ?? ""}{detail.data?.files.map((file) => `\n\n--- ${file.path} ---\n${file.content}`).join("")}</Text></ScrollView></View> : null}
      {usage.isError || versions.isError || restore.isError ? <Text accessibilityRole="alert" className="text-xs text-destructive">{t("mobile_management.action_failed", "Action failed. Check your permissions and try again.")}</Text> : null}
    </View> : null}
  </View>;
}
