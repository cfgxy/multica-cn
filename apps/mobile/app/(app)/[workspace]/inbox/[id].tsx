import { useState } from "react";
import { Alert, ActivityIndicator, Linking, ScrollView, View } from "react-native";
import { router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import {
  resolveBillingRecovery,
  type BillingRecoveryKind,
} from "@multica/core/billing/recovery";
import { BILLING_WORKSPACE_SUBSCRIPTIONS_FLAG } from "@multica/core/feature-flags";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { IconButton } from "@/components/ui/icon-button";
import { inboxListOptions } from "@/data/queries/inbox";
import { useRetrySourceContextQuickCreate } from "@/data/mutations/inbox";
import { ApiError } from "@/data/api";
import { getQuickCreateRetryPlan } from "@/lib/quick-create-retry";
import { getQuickCreateEditSeed } from "@/lib/quick-create-edit";
import { seedNewIssuePrefill } from "@/data/stores/new-issue-prefill-store";
import {
  appConfigOptions,
  workspaceSubscriptionSummaryOptions,
} from "@/data/queries/billing";
import { useWorkspaceStore } from "@/data/workspace-store";
import {
  getAutopilotQuotaBody,
  getInboxDisplayTitle,
} from "@/lib/inbox-display";
import { useTypeLabels } from "@/components/inbox/detail-label";
import { IssueLimitRecoveryDialog } from "@/components/billing/issue-limit-recovery";
import { timeAgo } from "@/lib/time-ago";
import { useT } from "@/lib/use-t";

function BillingRecovery({
  recovery,
  billingUrl,
}: {
  recovery: BillingRecoveryKind;
  billingUrl: string | null;
}) {
  const { t } = useT("inbox");
  switch (recovery) {
    case "checking":
      return <ActivityIndicator />;
    case "billing_disabled":
      return (
        <Text className="text-sm leading-5 text-muted-foreground">
          {t(
            "detail.billing_disabled",
            "Billing changes are unavailable for this workspace. Contact your workspace administrator for help.",
          )}
        </Text>
      );
    case "contact_admin":
      return (
        <Text className="text-sm leading-5 text-muted-foreground">
          {t(
            "detail.contact_admin",
            "Ask a workspace owner or admin to review the billing options.",
          )}
        </Text>
      );
    case "checkout":
    case "portal":
    case "billing":
    case "billing_unavailable":
      return billingUrl ? (
        <Button
          onPress={() => void Linking.openURL(billingUrl)}
          accessibilityLabel={t(
            "detail.review_billing_options",
            "Review billing options",
          )}
        >
          <Text>
            {t("detail.review_billing_options", "Review billing options")}
          </Text>
        </Button>
      ) : (
        <Text className="text-sm leading-5 text-muted-foreground">
          {t(
            "detail.review_billing_on_web",
            "Open Multica on the web to review billing options.",
          )}
        </Text>
      );
  }
}

export default function InboxNoticeDetail() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { t } = useT("inbox");
  // RUYI-605 delta: an issue-limit rejection from the retry path opens the
  // shared recovery dialog (desktop IssueLimitUpgradeDialog semantics)
  // instead of the bare title alert.
  const [issueLimitOpen, setIssueLimitOpen] = useState(false);
  const { data: items, isLoading } = useQuery(inboxListOptions(wsId));

  // Read the raw workspace-scoped cache: deduplication can replace a row,
  // but a sheet already opened for a specific notification must remain stable.
  const item = items?.find(
    (candidate) => candidate.id === id && candidate.workspace_id === wsId,
  );
  const isQuotaNotice = item?.type === "autopilot_quota_exceeded";
  const configQuery = useQuery({
    ...appConfigOptions(),
    enabled: isQuotaNotice,
  });
  const billingEnabled =
    configQuery.data?.feature_flags?.[
      BILLING_WORKSPACE_SUBSCRIPTIONS_FLAG
    ] === true;
  const summaryQuery = useQuery(
    workspaceSubscriptionSummaryOptions(wsId, isQuotaNotice && billingEnabled),
  );
  const checkingConfig =
    isQuotaNotice && !configQuery.data && configQuery.isFetching;
  const recovery = resolveBillingRecovery({
    actions: summaryQuery.data?.availableActions,
    billingEnabled: checkingConfig || billingEnabled,
    loading:
      checkingConfig ||
      (billingEnabled &&
        !summaryQuery.data?.availableActions &&
        summaryQuery.isFetching),
  });
  const webUrl = process.env.EXPO_PUBLIC_WEB_URL?.replace(/\/+$/, "");
  const billingUrl =
    webUrl && wsSlug ? `${webUrl}/${wsSlug}/settings?tab=billing` : null;
  const typeLabels = useTypeLabels();
  const body = item ? getAutopilotQuotaBody(item) : null;
  const originalPrompt =
    item?.type === "quick_create_failed" ||
    item?.type === "quick_create_unconfirmed"
      ? (item.details?.original_prompt ?? null)
      : null;
  // Retry gate mirrors web's detail pane
  // (packages/views/inbox/components/inbox-page.tsx): only a failed
  // quick-create whose details still carry the source-context linkage gets
  // the button. The server reuses the stored original input; the client
  // never resends the prompt.
  const retryPlan = item ? getQuickCreateRetryPlan(item) : null;
  const retryMutation = useRetrySourceContextQuickCreate();
  // RUYI-605: "edit in the full form" recovery — every quick-create outcome
  // (failed AND unconfirmed) gets the button, mirroring web's detail pane
  // (packages/views/inbox/components/inbox-page.tsx, isQuickCreateOutcome
  // gate with no original_prompt requirement). Tapping hands the stored
  // prompt + agent to the new-issue screen via the one-shot prefill store.
  const editSeed = item ? getQuickCreateEditSeed(item) : null;

  const onEditAdvanced = () => {
    if (!editSeed || !wsSlug) return;
    seedNewIssuePrefill(editSeed);
    // Same navigation shape as the app header's create entry
    // (components/ui/app-header-actions.tsx).
    router.push(`/${wsSlug}/new-issue`);
  };

  const onRetry = async () => {
    if (!retryPlan || retryMutation.isPending) return;
    try {
      await retryMutation.mutateAsync(retryPlan.taskId);
      // Back to the inbox first — the settled invalidate refetches the list,
      // where the retried task's new entry appears. Mobile has no toast
      // layer, so the started-confirmation rides a native alert (same
      // feedback channel as the error branches below).
      router.back();
      Alert.alert(
        t(
          "toasts.source_context_retry_started",
          "Retry started with the original context",
        ),
      );
    } catch (err) {
      // Structured 4xx bodies, same decode as quick-create-panel's submit
      // catch — the server is the trust boundary for every gate.
      const code =
        err instanceof ApiError &&
        err.body &&
        typeof err.body === "object" &&
        "code" in err.body
          ? String((err.body as { code: unknown }).code)
          : null;
      if (code === "source_context_retry_unavailable") {
        Alert.alert(
          t(
            "errors.source_context_retry_unavailable",
            "This context can no longer be retried. Start again from the branch point.",
          ),
        );
        return;
      }
      if (code === "issue_limit_reached") {
        // Same key as web's upgrade prompt; the shared dialog carries the
        // per-state description and the cloud-authorized billing action.
        setIssueLimitOpen(true);
        return;
      }
      Alert.alert(
        t(
          "errors.source_context_retry_failed",
          "Could not retry with the original context",
        ),
      );
    }
  };

  return (
    <View className="flex-1 bg-background">
      <View className="flex-row items-center border-b border-border px-4 py-3">
        <Text className="flex-1 text-lg font-semibold text-foreground">
          {item
            ? getInboxDisplayTitle(item)
            : t("detail.fallback_title", "Notification")}
        </Text>
        <IconButton
          name="close"
          variant="secondary"
          className="size-7 rounded-full"
          onPress={() => router.back()}
          accessibilityLabel={t("detail.close_notification", "Close notification")}
        />
      </View>

      {isLoading ? (
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator />
        </View>
      ) : !item ? (
        <View className="px-4 py-8">
          <Text className="text-sm text-muted-foreground text-center">
            {t("detail.unavailable", "This notification is no longer available.")}
          </Text>
        </View>
      ) : (
        <ScrollView
          className="flex-1"
          contentContainerClassName="gap-5 px-4 py-5"
          showsVerticalScrollIndicator={false}
        >
          {/* Type label + relative time, mirroring web's detail pane header
              (packages/views/inbox/components/inbox-page.tsx). */}
          <Text className="text-xs text-muted-foreground">
            {typeLabels[item.type] ?? item.type} · {timeAgo(item.created_at)}
          </Text>

          {body ? (
            <Text className="text-base leading-6 text-foreground">
              {body}
            </Text>
          ) : null}

          {/* Quick-create outcomes carry the user's original prompt so they can
              read back what they asked for — same block web renders. */}
          {originalPrompt ? (
            <View className="rounded-md border border-border bg-muted/40 p-3 gap-1">
              <Text className="text-xs font-medium text-muted-foreground">
                {t("detail.original_input", "Original input")}
              </Text>
              <Text className="text-sm leading-5 text-foreground">
                {originalPrompt}
              </Text>
            </View>
          ) : null}

          {/* RUYI-527: retry a failed quick-create with its original input.
              Unconfirmed outcomes stay button-less (the issue may exist) —
              same gate web's detail pane applies. */}
          {retryPlan ? (
            <Button
              size="sm"
              disabled={retryMutation.isPending}
              onPress={onRetry}
              accessibilityLabel={t(
                "detail.retry_with_context",
                "Retry with context",
              )}
            >
              <Text>
                {t("detail.retry_with_context", "Retry with context")}
              </Text>
            </Button>
          ) : null}

          {/* RUYI-605: recover the original input in the manual form. The
              button is neutral on purpose — for unconfirmed outcomes this is
              result re-editing, not failure recovery. */}
          {editSeed ? (
            <Button
              size="sm"
              onPress={onEditAdvanced}
              accessibilityLabel={t(
                "detail.edit_advanced",
                "Edit as advanced form",
              )}
            >
              <Text>{t("detail.edit_advanced", "Edit as advanced form")}</Text>
            </Button>
          ) : null}

          {isQuotaNotice ? (
            <BillingRecovery recovery={recovery} billingUrl={billingUrl} />
          ) : null}
        </ScrollView>
      )}
      <IssueLimitRecoveryDialog
        visible={issueLimitOpen}
        onClose={() => setIssueLimitOpen(false)}
      />
    </View>
  );
}
