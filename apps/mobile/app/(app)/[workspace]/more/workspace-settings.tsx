import { useEffect, useState } from "react";
import { ActivityIndicator, Alert, Modal, Pressable, ScrollView, TextInput, View } from "react-native";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { router, Stack } from "expo-router";
import { Text } from "@/components/ui/text";
import { api } from "@/data/api";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { memberListOptions } from "@/data/queries/members";
import { workspaceListOptions } from "@/data/queries/workspaces";
import { labelListOptions } from "@/data/queries/labels";
import { issueStatusListOptions } from "@/data/queries/issue-statuses";
import { useT } from "@/lib/use-t";
import type { IssueStatusCategory } from "@multica/core/types";

export default function WorkspaceSettingsPage() {
  const { t } = useT("settings");
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const userId = useAuthStore((s) => s.user?.id);
  const qc = useQueryClient();
  const workspaceQuery = useQuery(workspaceListOptions());
  const membersQuery = useQuery(memberListOptions(wsId));
  const labelsQuery = useQuery(labelListOptions(wsId));
  const statusesQuery = useQuery(issueStatusListOptions(wsId));
  const workspace = workspaceQuery.data?.find((row) => row.id === wsId);
  const mine = membersQuery.data?.find((row) => row.user_id === userId);
  const canManage = mine?.role === "owner" || mine?.role === "admin";
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [labelName, setLabelName] = useState("");
  const [statusName, setStatusName] = useState("");
  const [statusCategory, setStatusCategory] = useState<IssueStatusCategory>("todo");
  const [editing, setEditing] = useState<{ kind: "label" | "status"; id: string } | null>(null);
  const [editName, setEditName] = useState("");
  const [editColor, setEditColor] = useState("#6366f1");
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteTyped, setDeleteTyped] = useState("");
  useEffect(() => setName(workspace?.name ?? ""), [workspace?.id, workspace?.name]);
  const saveName = useMutation({
    mutationFn: () => api.updateWorkspace(wsId!, { name: name.trim() }),
    onSuccess: async () => { await qc.invalidateQueries({ queryKey: ["workspaces"] }); },
  });
  const invite = useMutation({
    mutationFn: () => api.inviteWorkspaceMember(wsId!, { email: email.trim(), role: "member" }),
    onSuccess: async () => { setEmail(""); await qc.invalidateQueries({ queryKey: ["members", wsId] }); },
  });
  const role = useMutation({
    mutationFn: ({ id, value }: { id: string; value: "admin" | "member" }) => api.updateWorkspaceMember(wsId!, id, { role: value }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["members", wsId] }),
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.removeWorkspaceMember(wsId!, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["members", wsId] }),
  });
  const deleteWorkspace = useMutation({
    mutationFn: () => api.deleteWorkspace(wsId!),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["workspaces"] });
      setDeleteOpen(false);
      router.replace("/select-workspace");
    },
  });
  const createLabel = useMutation({ mutationFn: () => api.createLabel({ name: labelName.trim(), color: "#6366f1" }), onSuccess: async () => { setLabelName(""); await qc.invalidateQueries({ queryKey: ["labels", wsId] }); } });
  const updateLabel = useMutation({ mutationFn: () => api.updateLabel(editing!.id, { name: editName.trim(), color: editColor }), onSuccess: async () => { setEditing(null); await qc.invalidateQueries({ queryKey: ["labels", wsId] }); } });
  const deleteLabel = useMutation({ mutationFn: (id: string) => api.deleteLabel(id), onSuccess: () => qc.invalidateQueries({ queryKey: ["labels", wsId] }) });
  const createStatus = useMutation({ mutationFn: () => api.createIssueStatus({ name: statusName.trim(), category: statusCategory, color: "#6366f1" }), onSuccess: async () => { setStatusName(""); await qc.invalidateQueries({ queryKey: ["issue-statuses", wsId] }); } });
  const updateStatus = useMutation({ mutationFn: () => api.updateIssueStatus(editing!.id, { name: editName.trim(), color: editColor }), onSuccess: async () => { setEditing(null); await qc.invalidateQueries({ queryKey: ["issue-statuses", wsId] }); } });
  const archiveStatus = useMutation({ mutationFn: (id: string) => api.archiveIssueStatus(id), onSuccess: () => qc.invalidateQueries({ queryKey: ["issue-statuses", wsId] }) });
  const reorderStatuses = useMutation({ mutationFn: ({ category, ids }: { category: typeof statusCategory; ids: string[] }) => api.reorderIssueStatuses(category, ids), onSuccess: () => qc.invalidateQueries({ queryKey: ["issue-statuses", wsId] }) });
  const confirmRemove = (id: string, label: string) => Alert.alert(
    t("mobile_management.remove_member", "Remove member"),
    `${t("mobile_management.remove_member_confirm", "Remove access for")} ${label}?`,
    [{ text: t("mobile_management.cancel", "Cancel"), style: "cancel" }, { text: t("mobile_management.remove", "Remove"), style: "destructive", onPress: () => remove.mutate(id) }],
  );
  const openEdit = (kind: "label" | "status", item: { id: string; name: string; color: string }) => { setEditing({ kind, id: item.id }); setEditName(item.name); setEditColor(item.color || "#6366f1"); };
  const confirmLabelDelete = (id: string, label: string) => Alert.alert(t("mobile_management.delete_label", "Delete label"), `${t("mobile_management.delete_confirm", "Delete")} ${label}?`, [{ text: t("mobile_management.cancel", "Cancel"), style: "cancel" }, { text: t("mobile_management.delete_action", "Delete"), style: "destructive", onPress: () => deleteLabel.mutate(id) }]);
  const confirmStatusArchive = (id: string, label: string) => Alert.alert(t("mobile_management.archive_status", "Archive status"), `${t("mobile_management.archive_confirm", "Archive for future use")} ${label}?`, [{ text: t("mobile_management.cancel", "Cancel"), style: "cancel" }, { text: t("mobile_management.archive", "Archive"), style: "destructive", onPress: () => archiveStatus.mutate(id) }]);
  return <View className="flex-1 bg-background">
    <Stack.Screen options={{ title: t("mobile_management.workspace", "Workspace settings") }} />
    {!canManage ? <View className="p-4"><Text className="text-sm text-muted-foreground">{t("mobile_management.admin_only", "Workspace owners and admins only")}</Text></View> : <ScrollView contentContainerClassName="gap-5 px-4 py-4">
      <View className="gap-2">
        <Text className="text-sm font-medium text-foreground">{t("mobile_management.workspace_name", "Workspace name")}</Text>
        <TextInput accessibilityLabel={t("mobile_management.workspace_name", "Workspace name")} value={name} onChangeText={setName} className="min-h-11 rounded border border-border px-3 text-foreground" />
        <Pressable accessibilityRole="button" disabled={!name.trim() || name.trim() === workspace?.name || saveName.isPending} onPress={() => saveName.mutate()} className="self-start rounded bg-primary px-4 py-2"><Text className="text-sm text-primary-foreground">{saveName.isPending ? t("mobile_management.saving", "Saving…") : t("mobile_management.save", "Save")}</Text></Pressable>
      </View>
      <View className="gap-2">
        <Text className="text-base font-semibold text-foreground">{t("mobile_management.members", "Members")}</Text>
        <View className="flex-row gap-2"><TextInput accessibilityLabel={t("mobile_management.email", "Email address")} value={email} onChangeText={setEmail} keyboardType="email-address" autoCapitalize="none" className="min-h-11 flex-1 rounded border border-border px-3 text-foreground" placeholder="name@example.com" /><Pressable accessibilityRole="button" disabled={!email.includes("@") || invite.isPending} onPress={() => invite.mutate()} className="justify-center rounded bg-primary px-3"><Text className="text-sm text-primary-foreground">{t("mobile_management.invite", "Invite")}</Text></Pressable></View>
        {membersQuery.isLoading ? <ActivityIndicator /> : null}
        {(membersQuery.data ?? []).map((member) => <View key={member.id} className="flex-row items-center gap-2 border-b border-border py-3">
          <View className="min-w-0 flex-1"><Text className="text-sm font-medium text-foreground" numberOfLines={1}>{member.name || member.email}</Text><Text className="text-xs text-muted-foreground">{member.email} · {member.role}</Text></View>
          {member.role !== "owner" && member.user_id !== userId ? <>
            <Pressable accessibilityRole="button" accessibilityLabel={t("mobile_management.change_role", "Change role")} onPress={() => role.mutate({ id: member.id, value: member.role === "admin" ? "member" : "admin" })} className="rounded bg-secondary px-2 py-2"><Text className="text-xs text-foreground">{member.role === "admin" ? t("mobile_management.make_member", "Member") : t("mobile_management.make_admin", "Admin")}</Text></Pressable>
            <Pressable accessibilityRole="button" accessibilityLabel={t("mobile_management.remove", "Remove")} onPress={() => confirmRemove(member.id, member.email)} className="rounded bg-secondary px-2 py-2"><Text className="text-xs text-destructive">{t("mobile_management.remove", "Remove")}</Text></Pressable>
          </> : null}
        </View>)}
        {saveName.isError || invite.isError || role.isError || remove.isError ? <Text accessibilityRole="alert" className="text-sm text-destructive">{t("mobile_management.action_failed", "Action failed. Check your permissions and try again.")}</Text> : null}
      </View>
      <View className="gap-2">
        <Text className="text-base font-semibold text-foreground">{t("mobile_management.labels", "Labels")}</Text>
        <View className="flex-row gap-2"><TextInput accessibilityLabel={t("mobile_management.new_label", "New label")} value={labelName} onChangeText={setLabelName} className="min-h-11 flex-1 rounded border border-border px-3 text-foreground" placeholder={t("mobile_management.new_label", "New label")} /><Pressable accessibilityRole="button" disabled={!labelName.trim() || createLabel.isPending} onPress={() => createLabel.mutate()} className="justify-center rounded bg-primary px-3"><Text className="text-sm text-primary-foreground">{t("mobile_management.add", "Add")}</Text></Pressable></View>
        {labelsQuery.isLoading ? <ActivityIndicator /> : null}
        {(labelsQuery.data ?? []).map((label) => <View key={label.id} className="flex-row items-center gap-3 border-b border-border py-3"><View className="size-3 rounded-full" style={{ backgroundColor: label.color }} /><Text className="min-w-0 flex-1 text-sm text-foreground">{label.name}</Text><Pressable accessibilityRole="button" onPress={() => openEdit("label", label)} className="rounded bg-secondary px-2 py-2"><Text className="text-xs text-foreground">{t("mobile_management.edit", "Edit")}</Text></Pressable><Pressable accessibilityRole="button" onPress={() => confirmLabelDelete(label.id, label.name)} className="rounded bg-secondary px-2 py-2"><Text className="text-xs text-destructive">{t("mobile_management.delete_action", "Delete")}</Text></Pressable></View>)}
      </View>
      <View className="gap-2">
        <Text className="text-base font-semibold text-foreground">{t("mobile_management.issue_statuses", "Issue statuses")}</Text>
        <TextInput accessibilityLabel={t("mobile_management.new_status", "New status")} value={statusName} onChangeText={setStatusName} className="min-h-11 rounded border border-border px-3 text-foreground" placeholder={t("mobile_management.new_status", "New status")} />
        <View className="flex-row flex-wrap gap-2">{Array.from(new Set((statusesQuery.data ?? []).map((item) => item.category))).map((category) => <Pressable key={category} accessibilityRole="radio" accessibilityState={{ checked: statusCategory === category }} onPress={() => setStatusCategory(category)} className={`rounded px-2 py-2 ${statusCategory === category ? "bg-primary" : "bg-secondary"}`}><Text className={`text-xs ${statusCategory === category ? "text-primary-foreground" : "text-foreground"}`}>{String(t(`issues:status.${category}`, category))}</Text></Pressable>)}</View>
        <Pressable accessibilityRole="button" disabled={!statusName.trim() || createStatus.isPending} onPress={() => createStatus.mutate()} className="self-start rounded bg-primary px-3 py-2"><Text className="text-sm text-primary-foreground">{t("mobile_management.add_status", "Add status")}</Text></Pressable>
        {statusesQuery.isLoading ? <ActivityIndicator /> : null}
        {[...(statusesQuery.data ?? [])].sort((a, b) => a.category.localeCompare(b.category) || a.position - b.position).map((status) => {
          const custom = (statusesQuery.data ?? []).filter((item) => !item.is_system && !item.archived_at && item.category === status.category).sort((a, b) => a.position - b.position);
          const index = custom.findIndex((item) => item.id === status.id);
          return <View key={status.id} className="flex-row items-center gap-2 border-b border-border py-3"><View className="size-3 rounded-full" style={{ backgroundColor: status.color }} /><View className="min-w-0 flex-1"><Text className="text-sm text-foreground">{status.name}{status.is_system ? ` · ${t("mobile_management.system", "System")}` : ""}</Text><Text className="text-xs text-muted-foreground">{String(t(`issues:status.${status.category}`, status.category))}{status.archived_at ? ` · ${t("mobile_management.archived", "Archived")}` : ""}</Text></View>
            {!status.is_system && !status.archived_at ? <>
              <Pressable accessibilityRole="button" accessibilityLabel={t("mobile_management.move_up", "Move up")} disabled={index <= 0} onPress={() => { const moved = [...custom]; [moved[index - 1], moved[index]] = [moved[index], moved[index - 1]]; reorderStatuses.mutate({ category: status.category, ids: moved.map((item) => item.id) }); }} className="rounded bg-secondary px-2 py-2"><Text className="text-xs text-foreground">↑</Text></Pressable>
              <Pressable accessibilityRole="button" accessibilityLabel={t("mobile_management.move_down", "Move down")} disabled={index < 0 || index >= custom.length - 1} onPress={() => { const moved = [...custom]; [moved[index], moved[index + 1]] = [moved[index + 1], moved[index]]; reorderStatuses.mutate({ category: status.category, ids: moved.map((item) => item.id) }); }} className="rounded bg-secondary px-2 py-2"><Text className="text-xs text-foreground">↓</Text></Pressable>
              <Pressable accessibilityRole="button" onPress={() => openEdit("status", status)} className="rounded bg-secondary px-2 py-2"><Text className="text-xs text-foreground">{t("mobile_management.edit", "Edit")}</Text></Pressable>
              <Pressable accessibilityRole="button" onPress={() => confirmStatusArchive(status.id, status.name)} className="rounded bg-secondary px-2 py-2"><Text className="text-xs text-destructive">{t("mobile_management.archive", "Archive")}</Text></Pressable>
            </> : null}
          </View>;
        })}
      </View>
      {mine?.role === "owner" ? <View className="gap-2 border-t border-border pt-4"><Text className="text-base font-semibold text-destructive">{t("mobile_management.danger_zone", "Danger zone")}</Text><Text className="text-sm text-muted-foreground">{t("mobile_management.delete_workspace_warning", "Deleting this workspace removes it for all members and cannot be undone.")}</Text><Pressable accessibilityRole="button" onPress={() => { setDeleteTyped(""); setDeleteOpen(true); }} className="self-start rounded bg-destructive px-3 py-2"><Text className="text-sm text-destructive-foreground">{t("mobile_management.delete_workspace", "Delete workspace…")}</Text></Pressable></View> : null}
      {workspaceQuery.isError || membersQuery.isError || labelsQuery.isError || statusesQuery.isError ? <Text accessibilityRole="alert" className="text-sm text-destructive">{t("mobile_management.load_failed", "Could not load. Tap to retry.")}</Text> : null}
      {saveName.isError || invite.isError || role.isError || remove.isError || createLabel.isError || updateLabel.isError || deleteLabel.isError || createStatus.isError || updateStatus.isError || archiveStatus.isError || reorderStatuses.isError ? <Text accessibilityRole="alert" className="text-sm text-destructive">{t("mobile_management.action_failed", "Action failed. Check your permissions and try again.")}</Text> : null}
    </ScrollView>}
    <Modal visible={deleteOpen} transparent animationType="fade" onRequestClose={() => { if (!deleteWorkspace.isPending) setDeleteOpen(false); }}><View className="flex-1 items-center justify-center bg-black/50 px-6"><View className="w-full gap-3 rounded-lg bg-card p-4"><Text className="text-base font-semibold text-destructive">{t("mobile_management.delete_workspace_confirm", "Delete workspace")}</Text><Text className="text-sm text-muted-foreground">{t("mobile_management.type_workspace_name", "Type the exact workspace name to confirm:")} {workspace?.name}</Text><TextInput accessibilityLabel={t("mobile_management.type_workspace_name_placeholder", "Type workspace name")} value={deleteTyped} onChangeText={setDeleteTyped} autoCapitalize="none" autoCorrect={false} editable={!deleteWorkspace.isPending} placeholder={workspace?.name} className="min-h-11 rounded border border-border px-3 text-foreground" /><View className="flex-row justify-end gap-3"><Pressable accessibilityRole="button" disabled={deleteWorkspace.isPending} onPress={() => setDeleteOpen(false)} className="rounded bg-secondary px-3 py-2"><Text className="text-sm text-foreground">{t("mobile_management.cancel", "Cancel")}</Text></Pressable><Pressable accessibilityRole="button" disabled={deleteTyped !== workspace?.name || deleteWorkspace.isPending} onPress={() => deleteWorkspace.mutate()} className="rounded bg-destructive px-3 py-2"><Text className="text-sm text-destructive-foreground">{deleteWorkspace.isPending ? t("mobile_management.deleting", "Deleting…") : t("mobile_management.delete_action", "Delete")}</Text></Pressable></View>{deleteWorkspace.isError ? <Text accessibilityRole="alert" className="text-sm text-destructive">{t("mobile_management.action_failed", "Action failed. Check your permissions and try again.")}</Text> : null}</View></View></Modal>
    <Modal visible={editing !== null} transparent animationType="fade" onRequestClose={() => setEditing(null)}><View className="flex-1 items-center justify-center bg-black/50 px-6"><View className="w-full gap-3 rounded-lg bg-card p-4"><Text className="text-base font-semibold text-foreground">{t("mobile_management.edit_item", "Edit item")}</Text><TextInput accessibilityLabel={t("mobile_management.name", "Name")} value={editName} onChangeText={setEditName} className="min-h-11 rounded border border-border px-3 text-foreground" /><TextInput accessibilityLabel={t("mobile_management.color", "Color")} value={editColor} onChangeText={setEditColor} autoCapitalize="none" className="min-h-11 rounded border border-border px-3 text-foreground" /><View className="flex-row justify-end gap-3"><Pressable accessibilityRole="button" onPress={() => setEditing(null)} className="rounded bg-secondary px-3 py-2"><Text className="text-sm text-foreground">{t("mobile_management.cancel", "Cancel")}</Text></Pressable><Pressable accessibilityRole="button" disabled={!editName.trim() || !/^#[0-9a-fA-F]{6}$/.test(editColor)} onPress={() => editing?.kind === "label" ? updateLabel.mutate() : updateStatus.mutate()} className="rounded bg-primary px-3 py-2"><Text className="text-sm text-primary-foreground">{t("mobile_management.save", "Save")}</Text></Pressable></View></View></View></Modal>
  </View>;
}
