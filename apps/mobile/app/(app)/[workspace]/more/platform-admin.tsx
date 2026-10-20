import { useEffect, useState } from "react";
import { ActivityIndicator, Alert, Modal, Pressable, ScrollView, TextInput, View } from "react-native";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Stack } from "expo-router";
import { Text } from "@/components/ui/text";
import { PlatformAdminTabs } from "@/components/management/platform-admin-tabs";
import { api } from "@/data/api";
import { useAuthStore } from "@/data/auth-store";
import { useT } from "@/lib/use-t";

type AdminTab = "users" | "workspaces" | "mcp" | "oauth" | "file_read_policy" | "audit";
type Pending = { kind: "disable" | "enable" | "grant" | "revoke" | "impersonate" | "workspace-add" | "oauth-disable" | "oauth-enable" | "oauth-rotate" | "oauth-delete" | "oauth-revoke-grant"; id: string; name: string; email?: string; role?: "member" | "admin" };

export default function PlatformAdminPage() {
  const { t } = useT("settings");
  const user = useAuthStore((s) => s.user);
  const applySession = useAuthStore((s) => s.applySession);
  const [tab, setTab] = useState<AdminTab>("users");
  const [pending, setPending] = useState<Pending | null>(null);
  const [reason, setReason] = useState("");
  const [createName, setCreateName] = useState("");
  const [createUris, setCreateUris] = useState("");
  const [secretReveal, setSecretReveal] = useState<string | null>(null);
  const [editId, setEditId] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editUris, setEditUris] = useState("");
  const [inviteEmail, setInviteEmail] = useState("");
  const [inviteRole, setInviteRole] = useState<"member" | "admin">("member");
  const [policyRules, setPolicyRules] = useState<{ id: string; pattern: string; description: string; enabled: boolean; builtin: boolean }[]>([]);
  const [newPolicyPattern, setNewPolicyPattern] = useState("");
  const [newPolicyDescription, setNewPolicyDescription] = useState("");
  const [auditOffset, setAuditOffset] = useState(0);
  const qc = useQueryClient();
  const enabled = user?.is_super_admin === true;
  const users = useQuery({ queryKey: ["platform-admin", "users"], queryFn: ({ signal }) => api.adminListUsers({ signal }), enabled: enabled && tab === "users" });
  const workspaces = useQuery({ queryKey: ["platform-admin", "workspaces"], queryFn: ({ signal }) => api.adminListWorkspaces({ signal }), enabled: enabled && tab === "workspaces" });
  const mcp = useQuery({ queryKey: ["platform-admin", "mcp"], queryFn: ({ signal }) => api.adminMCPStatus({ signal }), enabled: enabled && tab === "mcp" });
  const oauth = useQuery({ queryKey: ["platform-admin", "oauth"], queryFn: async ({ signal }) => Promise.all([api.adminListOAuthClients({ signal }), api.adminListOAuthGrants({ signal })]), enabled: enabled && tab === "oauth" });
  const policy = useQuery({ queryKey: ["platform-admin", "file-read-policy"], queryFn: ({ signal }) => api.getAdminFileReadPolicy({ signal }), enabled: enabled && tab === "file_read_policy" });
  const audit = useQuery({ queryKey: ["platform-admin", "audit", auditOffset], queryFn: ({ signal }) => api.adminListAuditLogs({ signal, limit: 50, offset: auditOffset }), enabled: enabled && tab === "audit" });
  const savePolicy = useMutation({ mutationFn: () => api.updateAdminFileReadPolicy({ version: policy.data?.version ?? 0, rules: policyRules }), onSuccess: async (result) => { await qc.setQueryData(["platform-admin", "file-read-policy"], result); } });
  const restorePolicy = useMutation({ mutationFn: () => api.restoreAdminFileReadPolicy(policy.data?.version ?? 0), onSuccess: async (result) => { setPolicyRules(result.rules); await qc.setQueryData(["platform-admin", "file-read-policy"], result); } });
  useEffect(() => { if (policy.data) setPolicyRules(policy.data.rules); }, [policy.data]);
  const action = useMutation({
    mutationFn: async (item: Pending) => {
      const why = reason.trim();
      if (item.kind === "disable" || item.kind === "enable") return api.adminSetUserDisabled(item.id, item.kind === "disable", why);
      if (item.kind === "grant" || item.kind === "revoke") return api.adminSetUserSuperAdmin(item.id, item.kind === "grant", why);
      if (item.kind === "impersonate") {
        const session = await api.adminImpersonate(item.id, why);
        await applySession(session.token, session.user);
        return;
      }
      if (item.kind === "workspace-add") return api.adminAddWorkspaceMember(item.id, { email: item.email, role: item.role ?? "member", reason: why });
      if (item.kind === "oauth-disable" || item.kind === "oauth-enable") return api.adminSetOAuthClientDisabled(item.id, item.kind === "oauth-disable", why);
      if (item.kind === "oauth-rotate") return api.adminRotateOAuthClientSecret(item.id, why);
      if (item.kind === "oauth-delete") return api.adminDeleteOAuthClient(item.id, why);
      return api.adminRevokeOAuthGrant(item.id, why);
    },
    onSuccess: (data) => {
      if (data && typeof data === "object" && "secret" in data) setSecretReveal(String(data.secret));
      setPending(null);
      setReason("");
    },
    onSettled: async (_data, error, variables) => {
      if (variables.kind === "impersonate") {
        if (!error) await qc.clear();
      } else {
        await qc.invalidateQueries({ queryKey: ["platform-admin"] });
      }
    },
  });
  const stopImpersonation = useMutation({
    mutationFn: () => api.stopImpersonation(),
    onSuccess: async (session) => { await applySession(session.token, session.user); await qc.clear(); },
  });
  const createClient = useMutation({
    mutationFn: () => api.adminCreateOAuthClient({ name: createName.trim(), redirect_uris: createUris.split("\n").map((uri) => uri.trim()).filter(Boolean) }),
    onSuccess: (result) => { setSecretReveal(result.secret); setCreateName(""); setCreateUris(""); void qc.invalidateQueries({ queryKey: ["platform-admin", "oauth"] }); },
  });
  const updateClient = useMutation({
    mutationFn: () => api.adminUpdateOAuthClient(editId!, { name: editName.trim(), redirect_uris: editUris.split("\n").map((uri) => uri.trim()).filter(Boolean) }),
    onSuccess: async () => { setEditId(null); await qc.invalidateQueries({ queryKey: ["platform-admin", "oauth"] }); },
  });
  const queue = (item: Pending) => { action.reset(); setReason(""); setPending(item); };
  const title = (item: Pending) => `${({ disable: "Disable user", enable: "Enable user", grant: "Grant super-admin", revoke: "Revoke super-admin", impersonate: "Impersonate", "workspace-add": "Add workspace member", "oauth-disable": "Disable OAuth client", "oauth-enable": "Enable OAuth client", "oauth-rotate": "Rotate OAuth secret", "oauth-delete": "Delete OAuth client", "oauth-revoke-grant": "Revoke OAuth grant" } as const)[item.kind]} · ${item.name}`;
  return <View className="flex-1 bg-background">
    <Stack.Screen options={{ title: t("mobile_management.platform_admin", "Platform administration") }} />
    {!enabled ? <View className="p-4"><Text className="text-sm text-muted-foreground">{t("mobile_management.admin_denied", "Platform administrator access required")}</Text></View> : <>
      {user?.impersonator_id ? <Pressable accessibilityRole="button" disabled={stopImpersonation.isPending} onPress={() => Alert.alert(t("mobile_management.stop_impersonation", "Stop impersonation"), t("mobile_management.return_confirm", { name: user.name || user.email }), [{ text: t("mobile_management.cancel", "Cancel"), style: "cancel" }, { text: t("mobile_management.stop", "Stop"), onPress: () => stopImpersonation.mutate() }])} className="m-4 rounded bg-destructive px-3 py-3"><Text className="text-sm text-destructive-foreground">{t("mobile_management.stop_impersonation", "Stop impersonation")}</Text></Pressable> : null}
      <PlatformAdminTabs
        tabs={(["users", "workspaces", "mcp", "oauth", "file_read_policy", "audit"] as AdminTab[]).map((value) => ({
          value,
          label: t(`mobile_management.tab_${value}`, value === "file_read_policy" ? "File policy" : value === "audit" ? "Audit" : value.toUpperCase()),
        }))}
        selected={tab}
        onSelect={(value) => setTab(value as AdminTab)}
      />
      <ScrollView className="flex-1" contentContainerClassName="gap-2 px-4 pb-8">
        {tab === "users" ? users.isLoading ? <ActivityIndicator /> : (users.data?.users ?? []).map((row) => <View key={row.id} className="gap-2 border-b border-border py-3"><View><Text className="text-sm font-medium text-foreground">{row.name || row.email}</Text><Text className="text-xs text-muted-foreground">{row.email} · {row.workspace_count} {t("mobile_management.workspaces_count", "workspaces")} · {row.disabled_at ? t("mobile_management.disabled", "disabled") : t("mobile_management.active", "active")} · {row.is_super_admin ? t("mobile_management.super_admin", "super-admin") : t("mobile_management.user", "user")}</Text></View><View className="flex-row flex-wrap gap-2">{row.disabled_at ? <Action label={t("mobile_management.enable_user", "Enable")} onPress={() => queue({ kind: "enable", id: row.id, name: row.email })} /> : <Action label={t("mobile_management.disable_user", "Disable…")} onPress={() => queue({ kind: "disable", id: row.id, name: row.email })} />}<Action label={row.is_super_admin ? t("mobile_management.revoke_admin", "Revoke super-admin…") : t("mobile_management.grant_admin", "Grant super-admin…")} onPress={() => queue({ kind: row.is_super_admin ? "revoke" : "grant", id: row.id, name: row.email })} /><Action label={t("mobile_management.impersonate", "Impersonate…")} onPress={() => queue({ kind: "impersonate", id: row.id, name: row.email })} /></View></View>) : null}
        {tab === "workspaces" ? workspaces.isLoading ? <ActivityIndicator /> : <><TextInput accessibilityLabel={t("mobile_management.member_email", "Member email")} value={inviteEmail} onChangeText={setInviteEmail} keyboardType="email-address" autoCapitalize="none" placeholder={t("mobile_management.member_email", "Member email")} className="min-h-11 rounded border border-border px-3 text-foreground" /><View className="flex-row gap-2"><Action label={t(inviteRole === "member" ? "mobile_management.role_member" : "mobile_management.role_admin", inviteRole)} onPress={() => setInviteRole((value) => value === "member" ? "admin" : "member")} /></View>{(workspaces.data?.workspaces ?? []).map((row) => <View key={row.id} className="flex-row items-center gap-3 border-b border-border py-3"><View className="min-w-0 flex-1"><Text className="text-sm font-medium text-foreground">{row.name}</Text><Text className="text-xs text-muted-foreground">/{row.slug} · {row.member_count} {t("mobile_management.members_count", "members")} · {row.owner_email || t("mobile_management.no_owner", "No owner")}</Text></View><Action label={t("mobile_management.add_member", "Add member…")} onPress={() => inviteEmail.includes("@") && queue({ kind: "workspace-add", id: row.id, name: row.name, email: inviteEmail.trim(), role: inviteRole })} /></View>)}</> : null}
        {tab === "mcp" ? mcp.isLoading ? <ActivityIndicator /> : <View className="gap-3 py-3"><Metric label={t("mobile_management.mcp_configured", "MCP configured")} value={String(mcp.data?.mcp.url_configured ?? false)} /><Metric label={t("mobile_management.mcp_reachable", "MCP reachable")} value={String(mcp.data?.mcp.reachable ?? false)} /><Metric label={t("mobile_management.mcp_version_tools", "MCP version / tools")} value={`${mcp.data?.mcp.version || "—"} / ${mcp.data?.mcp.tool_count ?? 0}`} /><Metric label={t("mobile_management.oauth_clients_grants", "OAuth clients / grants")} value={`${mcp.data?.clients.active ?? 0} / ${mcp.data?.grants.active ?? 0}`} /></View> : null}
        {tab === "oauth" ? oauth.isLoading ? <ActivityIndicator /> : <>
          <Text className="pt-3 text-base font-semibold text-foreground">{t("mobile_management.oauth_clients_title", "OAuth clients")}</Text>
          <TextInput accessibilityLabel={t("mobile_management.client_name", "Client name")} value={createName} onChangeText={setCreateName} placeholder={t("mobile_management.new_client", "New client name")} className="min-h-11 rounded border border-border px-3 text-foreground" />
          <TextInput accessibilityLabel={t("mobile_management.redirect_uris", "One redirect URI per line")} value={createUris} onChangeText={setCreateUris} placeholder={t("mobile_management.redirect_uris", "One redirect URI per line")} multiline className="min-h-16 rounded border border-border px-3 py-2 text-foreground" />
          <Action label={createClient.isPending ? t("mobile_management.creating", "Creating…") : t("mobile_management.create_client", "Create OAuth client")} onPress={() => createName.trim() && createClient.mutate()} />
          {(oauth.data?.[0].clients ?? []).map((row) => <View key={row.id} className="gap-2 border-b border-border py-3"><Text className="text-sm font-medium text-foreground">{row.name}</Text><Text className="text-xs text-muted-foreground">{row.client_id} · {row.active_grants} {t("mobile_management.active_grants", "active grants")} · {row.disabled_at ? t("mobile_management.disabled", "disabled") : t("mobile_management.active", "active")}</Text>
            {editId === row.id ? <><TextInput accessibilityLabel={t("mobile_management.client_name", "Client name")} value={editName} onChangeText={setEditName} className="min-h-10 rounded border border-border px-3 text-foreground" /><TextInput accessibilityLabel={t("mobile_management.redirect_uris", "One redirect URI per line")} value={editUris} onChangeText={setEditUris} multiline className="min-h-16 rounded border border-border px-3 py-2 text-foreground" /><Action label={t("mobile_management.save_changes", "Save changes")} onPress={() => updateClient.mutate()} /></> : <View className="flex-row flex-wrap gap-2"><Action label={t("mobile_management.edit", "Edit")} onPress={() => { setEditId(row.id); setEditName(row.name); setEditUris(row.redirect_uris.join("\n")); }} /><Action label={row.disabled_at ? t("mobile_management.enable_user", "Enable") : t("mobile_management.disable_user", "Disable…")} onPress={() => queue({ kind: row.disabled_at ? "oauth-enable" : "oauth-disable", id: row.id, name: row.name })} /><Action label={t("mobile_management.rotate_secret", "Rotate secret…")} onPress={() => queue({ kind: "oauth-rotate", id: row.id, name: row.name })} /><Action label={t("mobile_management.delete", "Delete…")} onPress={() => queue({ kind: "oauth-delete", id: row.id, name: row.name })} /></View>}
          </View>)}
          <Text className="pt-3 text-base font-semibold text-foreground">{t("mobile_management.oauth_grants_title", "OAuth grants")}</Text>
          {(oauth.data?.[1].grants ?? []).map((row) => <View key={row.id} className="flex-row items-center gap-3 border-b border-border py-3"><View className="min-w-0 flex-1"><Text className="text-sm text-foreground">{row.client_name || row.client_id} → {row.user_name || row.user_email || row.user_id}</Text><Text className="text-xs text-muted-foreground">{row.scope} · {row.revoked_at ? t("mobile_management.revoked", "revoked") : t("mobile_management.active", "active")}</Text></View>{!row.revoked_at ? <Action label={t("mobile_management.revoke", "Revoke…")} onPress={() => queue({ kind: "oauth-revoke-grant", id: row.id, name: row.user_email || row.user_id })} /> : null}</View>)}
        </> : null}
        {tab === "file_read_policy" ? policy.isLoading ? <ActivityIndicator /> : <>
          <Text className="pt-3 text-base font-semibold text-foreground">{t("mobile_management.file_policy", "File read policy")}</Text>
          {(policyRules ?? []).map((rule, index) => <View key={rule.id} className="gap-2 border-b border-border py-3"><Pressable accessibilityRole="checkbox" accessibilityState={{ checked: rule.enabled }} onPress={() => setPolicyRules((items) => items.map((item, i) => i === index ? { ...item, enabled: !item.enabled } : item))}><Text className="text-xs text-foreground">{t(rule.enabled ? "mobile_management.enabled" : "mobile_management.disabled", rule.enabled ? "Enabled" : "Disabled")}{rule.builtin ? ` · ${t("mobile_management.builtin", "Built-in")}` : ""}</Text></Pressable><TextInput accessibilityLabel={t("mobile_management.policy_pattern", "Pattern")} value={rule.pattern} onChangeText={(value) => setPolicyRules((items) => items.map((item, i) => i === index ? { ...item, pattern: value } : item))} className="min-h-10 rounded border border-border px-3 text-foreground" /><TextInput accessibilityLabel={t("mobile_management.policy_description", "Description")} value={rule.description} onChangeText={(value) => setPolicyRules((items) => items.map((item, i) => i === index ? { ...item, description: value } : item))} className="min-h-10 rounded border border-border px-3 text-foreground" /><Pressable accessibilityRole="button" onPress={() => setPolicyRules((items) => items.filter((_, i) => i !== index))} className="self-end px-2 py-1"><Text className="text-xs text-destructive">{t("mobile_management.remove", "Remove")}</Text></Pressable></View>)}
          <TextInput accessibilityLabel={t("mobile_management.policy_pattern", "Pattern")} value={newPolicyPattern} onChangeText={setNewPolicyPattern} placeholder={t("mobile_management.policy_pattern", "Pattern")} className="min-h-10 rounded border border-border px-3 text-foreground" />
          <TextInput accessibilityLabel={t("mobile_management.policy_description", "Description")} value={newPolicyDescription} onChangeText={setNewPolicyDescription} placeholder={t("mobile_management.policy_description", "Description")} className="min-h-10 rounded border border-border px-3 text-foreground" />
          <Action label={t("mobile_management.add", "Add")} onPress={() => { const pattern = newPolicyPattern.trim(); if (!pattern) return; setPolicyRules((items) => [...items, { id: `custom-${Date.now()}`, pattern, description: newPolicyDescription.trim(), enabled: true, builtin: false }]); setNewPolicyPattern(""); setNewPolicyDescription(""); }} />
          <View className="flex-row gap-2"><Action label={t("mobile_management.save", "Save")} onPress={() => Alert.alert(t("mobile_management.save", "Save"), t("mobile_management.policy_save_confirm", "Save file-read policy changes?"), [{ text: t("mobile_management.cancel", "Cancel"), style: "cancel" }, { text: t("mobile_management.save", "Save"), onPress: () => savePolicy.mutate() }])} /><Action label={t("mobile_management.restore_defaults", "Restore defaults")} onPress={() => Alert.alert(t("mobile_management.restore_defaults", "Restore defaults"), t("mobile_management.policy_restore_confirm", "Restore default file-read rules?"), [{ text: t("mobile_management.cancel", "Cancel"), style: "cancel" }, { text: t("mobile_management.restore_defaults", "Restore defaults"), style: "destructive", onPress: () => restorePolicy.mutate() }])} /></View>
          {policy.isError || savePolicy.isError || restorePolicy.isError ? <Text accessibilityRole="alert" className="text-sm text-destructive">{t("mobile_management.action_failed", "Action failed. Check your permissions and try again.")}</Text> : null}
        </> : null}
        {tab === "audit" ? audit.isLoading ? <ActivityIndicator /> : <>
          <Text className="pt-3 text-base font-semibold text-foreground">{t("mobile_management.audit_records", "Platform audit records")}</Text>
          {(audit.data?.events ?? []).map((event) => <View key={event.id} className="gap-1 border-b border-border py-3"><Text className="text-sm font-medium text-foreground">{event.action} · {event.target_type}</Text><Text className="text-xs text-muted-foreground">{event.actor_name || event.actor_email || event.actor_id} → {event.target_id || "—"} · {new Date(event.created_at).toLocaleString()}</Text>{event.reason ? <Text className="text-xs text-muted-foreground">{event.reason}</Text> : null}</View>)}
          {audit.data?.events.length === 0 ? <Text className="text-sm text-muted-foreground">{t("mobile_management.no_audit_records", "No audit records")}</Text> : null}
          <View className="flex-row items-center justify-between py-2"><Action disabled={auditOffset === 0} label={t("mobile_management.previous", "Previous")} onPress={() => setAuditOffset((offset) => Math.max(0, offset - 50))} /><Text className="text-xs text-muted-foreground">{audit.data?.total ? `${audit.data.offset + 1}–${Math.min(audit.data.offset + audit.data.events.length, audit.data.total)} / ${audit.data.total}` : "0 / 0"}</Text><Action disabled={!audit.data || audit.data.offset + audit.data.events.length >= audit.data.total} label={t("mobile_management.next", "Next")} onPress={() => setAuditOffset((offset) => offset + 50)} /></View>
        </> : null}
        {users.isError || workspaces.isError || mcp.isError || oauth.isError || policy.isError || audit.isError ? <Pressable accessibilityRole="button" onPress={() => { void users.refetch(); void workspaces.refetch(); void mcp.refetch(); void oauth.refetch(); void policy.refetch(); void audit.refetch(); }} className="py-3"><Text accessibilityRole="alert" className="text-sm text-destructive">{t("mobile_management.load_admin_failed", "Could not load the platform administration data. Tap to retry.")}</Text></Pressable> : null}
        {stopImpersonation.isError || createClient.isError || updateClient.isError ? <Text accessibilityRole="alert" className="py-2 text-sm text-destructive">{t("mobile_management.action_failed", "Action failed. Check your permissions and try again.")}</Text> : null}
      </ScrollView>
      <Modal visible={pending !== null} transparent animationType="fade" onRequestClose={() => { if (!action.isPending) setPending(null); }}><View className="flex-1 items-center justify-center bg-black/50 px-6"><View className="w-full gap-3 rounded-lg bg-card p-4"><Text className="text-base font-semibold text-foreground">{pending ? title(pending) : ""}</Text><Text className="text-sm text-muted-foreground">{t("mobile_management.confirm_reason", "This action may change account access or invalidate credentials. It is recorded in the platform audit log.")}</Text><TextInput accessibilityLabel={t("mobile_management.required_reason", "Required reason")} editable={!action.isPending} value={reason} onChangeText={setReason} placeholder={t("mobile_management.required_reason", "Required reason")} className="min-h-11 rounded border border-border px-3 text-foreground" /><View className="flex-row justify-end gap-3"><Pressable accessibilityRole="button" disabled={action.isPending} onPress={() => setPending(null)} className="rounded bg-secondary px-3 py-2"><Text className="text-sm text-foreground">{t("mobile_management.cancel", "Cancel")}</Text></Pressable><Pressable accessibilityRole="button" disabled={!reason.trim() || action.isPending} onPress={() => pending && action.mutate(pending)} className="rounded bg-destructive px-3 py-2"><Text className="text-sm text-destructive-foreground">{t("mobile_management.confirm", "Confirm")}</Text></Pressable></View>{action.isError ? <Text accessibilityRole="alert" className="text-sm text-destructive">{t("mobile_management.action_failed", "Action failed. Check your permissions and try again.")}</Text> : null}</View></View></Modal>
      <Modal visible={secretReveal !== null} transparent animationType="fade" onRequestClose={() => setSecretReveal(null)}><View className="flex-1 items-center justify-center bg-black/50 px-6"><View className="w-full gap-3 rounded-lg bg-card p-4"><Text className="text-base font-semibold text-foreground">{t("mobile_management.secret_once", "Client secret · shown once")}</Text><Text selectable className="text-sm text-foreground">{secretReveal}</Text><Pressable accessibilityRole="button" onPress={() => setSecretReveal(null)} className="self-end rounded bg-primary px-3 py-2"><Text className="text-sm text-primary-foreground">{t("mobile_management.close", "Close")}</Text></Pressable></View></View></Modal>
    </>}
  </View>;
}

function Action({ label, onPress, disabled = false }: { label: string; onPress: () => void; disabled?: boolean }) { return <Pressable accessibilityRole="button" disabled={disabled} onPress={onPress} className={`rounded px-2 py-2 ${disabled ? "bg-muted opacity-50" : "bg-secondary"}`}><Text className="text-xs text-foreground">{label}</Text></Pressable>; }
function Metric({ label, value }: { label: string; value: string }) { return <View className="flex-row justify-between border-b border-border py-3"><Text className="text-sm text-muted-foreground">{label}</Text><Text className="text-sm text-foreground">{value}</Text></View>; }
