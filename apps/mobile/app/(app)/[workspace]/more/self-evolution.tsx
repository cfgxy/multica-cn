import { useState } from "react";
import { ActivityIndicator, Alert, Modal, Pressable, RefreshControl, ScrollView, TextInput, View } from "react-native";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Stack } from "expo-router";
import { Text } from "@/components/ui/text";
import { api } from "@/data/api";
import { memberListOptions } from "@/data/queries/members";
import { clauseGovernanceOptions, managementKeys, promptProposalsOptions, retrospectiveRunsOptions, selfEvolutionOverviewOptions } from "@/data/queries/management";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";

export default function SelfEvolutionPage() {
  const { t } = useT("self-evolution");
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const userId = useAuthStore((s) => s.user?.id);
  const qc = useQueryClient();
  const [busyId, setBusyId] = useState<string | null>(null);
  const [rejectId, setRejectId] = useState<string | null>(null);
  const [rejectReason, setRejectReason] = useState("");
  const [review, setReview] = useState<{ id: string; diff: string } | null>(null);
  const overview = useQuery(selfEvolutionOverviewOptions(wsId));
  const proposals = useQuery(promptProposalsOptions(wsId));
  const clauses = useQuery(clauseGovernanceOptions(wsId));
  const retrospectives = useQuery(retrospectiveRunsOptions(wsId));
  const members = useQuery(memberListOptions(wsId));
  const member = members.data?.find((row) => row.user_id === userId);
  const canApprove = member?.role === "owner";
  const approve = useMutation({
    mutationFn: (id: string) => api.approvePromptProposal(id),
    onSuccess: async () => { await Promise.all([qc.invalidateQueries({ queryKey: managementKeys.proposals(wsId) }), qc.invalidateQueries({ queryKey: managementKeys.selfEvolution(wsId) })]); setReview(null); },
  });
  const reject = useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) => api.rejectPromptProposal(id, reason),
    onSuccess: async () => {
      await Promise.all([qc.invalidateQueries({ queryKey: managementKeys.proposals(wsId) }), qc.invalidateQueries({ queryKey: managementKeys.selfEvolution(wsId) })]);
      setRejectId(null);
      setRejectReason("");
    },
  });

  const previewAndApprove = async (id: string) => {
    approve.reset();
    setBusyId(id);
    try {
      const preview = await api.previewPromptProposal(id);
      const diff = preview.diff.map((line) => `${line.kind === "add" ? "+" : line.kind === "del" ? "-" : " "}${line.text}`).join("\n");
      setReview({ id, diff: diff || t("mobile.empty_diff", "No diff lines") });
    } catch {
      Alert.alert(t("mobile.error", "Could not load proposal preview"));
    } finally {
      setBusyId(null);
    }
  };

  const confirmReject = (id: string) => { reject.reset(); setRejectId(id); setRejectReason(""); };

  const refreshing = overview.isRefetching || proposals.isRefetching || clauses.isRefetching || retrospectives.isRefetching;
  const refresh = () => {
    void Promise.all([overview.refetch(), proposals.refetch(), clauses.refetch(), retrospectives.refetch()]);
  };
  const error = overview.isError || proposals.isError || clauses.isError || retrospectives.isError;
  return <View className="flex-1 bg-background">
    <Stack.Screen options={{ title: t("mobile.title", "Self-Evolution") }} />
    <ScrollView refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />} contentContainerClassName="gap-4 px-4 py-4">
      {overview.isLoading ? <ActivityIndicator /> : null}
      {error ? <Pressable accessibilityRole="button" onPress={refresh} className="rounded border border-border p-3"><Text className="text-sm text-destructive">{t("mobile.load_error", "Some data could not be loaded. Tap to retry.")}</Text></Pressable> : null}
      <View className="gap-2">
        <Text className="text-base font-semibold text-foreground">{t("mobile.overview", "Overview")}</Text>
        <View className="flex-row flex-wrap gap-2">
          <Metric label={t("mobile.versions", "Versions")} value={String(overview.data?.versions.length ?? 0)} />
          <Metric label={t("mobile.proposals", "Proposals")} value={String(overview.data?.proposals.total ?? 0)} />
          <Metric label={t("mobile.pending_owner", "Awaiting owner")} value={String(overview.data?.proposals.pending_owner ?? 0)} />
          <Metric label={t("mobile.skills", "Skills") } value={String(overview.data?.skills.count ?? 0)} />
        </View>
      </View>
      <View className="gap-2">
        <Text className="text-base font-semibold text-foreground">{t("mobile.retrospective", "Retrospective runs")}</Text>
        {(retrospectives.data ?? []).map((run) => <View key={run.id} className="gap-1 border-b border-border py-2"><Text className="text-sm font-medium text-foreground">{run.status} · {run.trigger}</Text><Text className="text-xs text-muted-foreground">{new Date(run.created_at).toLocaleString()} · {run.issues_analyzed}/{run.issues_scanned} {t("mobile.issues_analyzed", "tasks analyzed")} · {run.proposals_created} {t("mobile.proposal_count_label", "proposals")}</Text>{run.error ? <Text className="text-xs text-destructive">{run.error}</Text> : null}</View>)}
        {retrospectives.data?.length === 0 ? <Text className="text-sm text-muted-foreground">{t("mobile.no_retrospectives", "No retrospective runs")}</Text> : null}
      </View>
      <View className="gap-2">
        <Text className="text-base font-semibold text-foreground">{t("mobile.proposals", "Proposals")}</Text>
        {(proposals.data ?? []).map((proposal) => <View key={proposal.id} className="gap-2 rounded border border-border p-3">
          <Text className="text-sm font-medium text-foreground">{proposal.clause_name || proposal.target_section || proposal.id}</Text>
          <Text className="text-xs text-muted-foreground">{proposal.carrier_scope} · {proposal.status} · {proposal.change_kind}</Text>
          <Text className="text-sm text-foreground" numberOfLines={4}>{proposal.clause_text}</Text>
          {canApprove && proposal.status === "pending_owner" ? <View className="flex-row gap-3">
            <Pressable accessibilityRole="button" disabled={busyId === proposal.id || approve.isPending} onPress={() => void previewAndApprove(proposal.id)} className="rounded bg-primary px-3 py-2"><Text className="text-sm text-primary-foreground">{t("mobile.review_approve", "Review and approve")}</Text></Pressable>
            <Pressable accessibilityRole="button" onPress={() => confirmReject(proposal.id)} className="rounded bg-secondary px-3 py-2"><Text className="text-sm text-foreground">{t("mobile.reject", "Reject")}</Text></Pressable>
          </View> : null}
        </View>)}
        {proposals.data?.length === 0 ? <Text className="text-sm text-muted-foreground">{t("mobile.no_proposals", "No proposals")}</Text> : null}
      </View>
      <View className="gap-2">
        <Text className="text-base font-semibold text-foreground">{t("mobile.clauses", "Clause governance")}</Text>
        {(clauses.data?.clauses ?? []).map((clause) => <View key={clause.id} className="flex-row items-center justify-between gap-3 border-b border-border py-3">
          <View className="min-w-0 flex-1"><Text className="text-sm font-medium text-foreground">{clause.title || clause.slug}</Text><Text className="text-xs text-muted-foreground">{clause.status} · v{clause.latest_version}</Text></View>
          <Text className="text-xs text-muted-foreground">{clause.injections} {t("mobile.injections", "uses")}</Text>
        </View>)}
        {clauses.data?.clauses.length === 0 ? <Text className="text-sm text-muted-foreground">{t("mobile.no_clauses", "No governed clauses")}</Text> : null}
      </View>
    </ScrollView>
    <Modal visible={review !== null} transparent animationType="slide" onRequestClose={() => setReview(null)}>
      <View className="flex-1 justify-end bg-black/50"><View className="max-h-[90%] gap-3 rounded-t-lg bg-card p-4">
        <Text className="text-base font-semibold text-foreground">{t("mobile.review_diff", "Review proposal diff")}</Text>
        <ScrollView className="max-h-[65%]" contentContainerClassName="gap-1"><Text selectable className="font-mono text-xs text-foreground">{review?.diff}</Text></ScrollView>
        <View className="flex-row justify-end gap-3"><Pressable accessibilityRole="button" disabled={approve.isPending} onPress={() => setReview(null)} className="rounded bg-secondary px-3 py-2"><Text className="text-sm text-foreground">{t("mobile.cancel", "Cancel")}</Text></Pressable><Pressable accessibilityRole="button" disabled={approve.isPending || !review} onPress={() => review && approve.mutate(review.id)} className="rounded bg-primary px-3 py-2"><Text className="text-sm text-primary-foreground">{approve.isPending ? t("mobile.approving", "Approving…") : t("mobile.approve", "Approve")}</Text></Pressable></View>
        {approve.isError ? <Text accessibilityRole="alert" className="text-sm text-destructive">{t("mobile.action_failed", "Action failed. Your changes were not saved. Review and try again.")}</Text> : null}
      </View></View>
    </Modal>
    <Modal visible={rejectId !== null} transparent animationType="fade" onRequestClose={() => setRejectId(null)}>
      <View className="flex-1 items-center justify-center bg-black/50 px-6">
        <View className="w-full gap-3 rounded-lg bg-card p-4">
          <Text className="text-base font-semibold text-foreground">{t("mobile.reject_reason", "Reason for rejection")}</Text>
          <TextInput accessibilityLabel={t("mobile.reject_reason", "Reason for rejection")} value={rejectReason} onChangeText={setRejectReason} multiline className="min-h-24 rounded border border-border p-3 text-foreground" />
          <View className="flex-row justify-end gap-3">
            <Pressable accessibilityRole="button" onPress={() => setRejectId(null)} className="rounded bg-secondary px-3 py-2"><Text className="text-sm text-foreground">{t("mobile.cancel", "Cancel")}</Text></Pressable>
            <Pressable accessibilityRole="button" disabled={!rejectReason.trim() || reject.isPending} onPress={() => { if (rejectId && rejectReason.trim()) reject.mutate({ id: rejectId, reason: rejectReason.trim() }); }} className="rounded bg-destructive px-3 py-2"><Text className="text-sm text-destructive-foreground">{reject.isPending ? t("mobile.rejecting", "Rejecting…") : t("mobile.reject", "Reject")}</Text></Pressable>
          </View>
          {reject.isError ? <Text accessibilityRole="alert" className="text-sm text-destructive">{t("mobile.action_failed", "Action failed. Your changes were not saved. Review and try again.")}</Text> : null}
        </View>
      </View>
    </Modal>
  </View>;
}

function Metric({ label, value }: { label: string; value: string }) {
  return <View className="min-w-[45%] flex-1 gap-1 rounded border border-border p-3"><Text className="text-xs text-muted-foreground">{label}</Text><Text className="text-lg font-semibold text-foreground">{value}</Text></View>;
}
