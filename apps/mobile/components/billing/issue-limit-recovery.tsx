/**
 * Shared issue-limit recovery surface for mobile — the counterpart of
 * web/desktop's `IssueLimitUpgradeDialog`
 * (packages/views/modals/issue-limit-upgrade-dialog.tsx), opened on
 * `issue_limit_reached` from both create paths (quick-create submit and
 * inbox retry). Same semantics: one title, description per
 * `resolveBillingRecovery` state, the billing action Cloud's
 * `availableActions` authorizes, and Close. Two deliberate mobile
 * divergences, both mirroring the quota-notice block in
 * `app/(app)/[workspace]/inbox/[id].tsx`:
 *   - the container is a centered RN `Modal` card (same idiom as
 *     `components/voice/voice-session-overlay.tsx`) instead of the desktop
 *     centered dialog;
 *   - every billing action (checkout / portal / purchaseSeats) opens the
 *     workspace billing-settings page on the web — desktop's portal branch
 *     opens the Stripe portal directly, which mobile v1 does not replicate.
 */
import { Linking, Modal, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import {
  resolveBillingRecovery,
} from "@multica/core/billing/recovery";
import { BILLING_WORKSPACE_SUBSCRIPTIONS_FLAG } from "@multica/core/feature-flags";
import {
  appConfigOptions,
  workspaceSubscriptionSummaryOptions,
} from "@/data/queries/billing";
import { useWorkspaceStore } from "@/data/workspace-store";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/use-t";

/** Same gating the quota-notice block runs before opening a recovery surface. */
function useIssueLimitRecovery(visible: boolean) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const configQuery = useQuery({ ...appConfigOptions(), enabled: visible });
  const billingEnabled =
    configQuery.data?.feature_flags?.[
      BILLING_WORKSPACE_SUBSCRIPTIONS_FLAG
    ] === true;
  const summaryQuery = useQuery(
    workspaceSubscriptionSummaryOptions(wsId, visible && billingEnabled),
  );
  const checkingConfig = visible && !configQuery.data && configQuery.isFetching;
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
  return { recovery, billingUrl };
}

/**
 * The testable presentation unit — everything inside the modal card. Split
 * from the Modal wrapper because RN's Modal cannot render in an open state
 * under the jest harness (host-component stub crashes RNTL), so the copy /
 * action / close semantics are pinned on this component.
 */
export function IssueLimitRecoveryCard({ onClose }: { onClose: () => void }) {
  const { t } = useT("modals");
  const { t: tCommon } = useT("common");
  const { recovery, billingUrl } = useIssueLimitRecovery(true);

  // Mirrors desktop's resolveRecoveryPresentation
  // (packages/views/modals/issue-limit-upgrade-dialog.tsx): state → copy,
  // never authorization.
  let description: string;
  let actionLabel: string | null = null;
  switch (recovery) {
    case "checking":
      description = t(
        "create_issue.issue_limit.checking_description",
        "Checking the billing actions available for this workspace…",
      );
      break;
    case "billing_disabled":
      description = t(
        "create_issue.issue_limit.billing_disabled_description",
        "Delete an existing issue to free space, or contact your workspace administrator.",
      );
      break;
    case "contact_admin":
      description = t(
        "create_issue.issue_limit.contact_description",
        "Ask a workspace owner or admin to upgrade to Pro.",
      );
      break;
    case "checkout":
      description = t(
        "create_issue.issue_limit.upgrade_description",
        "Upgrade to Pro to keep creating issues.",
      );
      actionLabel = t("create_issue.issue_limit.upgrade_action", "Upgrade to Pro");
      break;
    case "portal":
      description = t(
        "create_issue.issue_limit.portal_description",
        "Open Billing Portal to manage this workspace's subscription and restore issue creation.",
      );
      actionLabel = t(
        "create_issue.issue_limit.portal_action",
        "Open Billing Portal",
      );
      break;
    case "billing":
      description = t(
        "create_issue.issue_limit.billing_description",
        "Open Billing to see the upgrade options available for this workspace.",
      );
      actionLabel = t("create_issue.issue_limit.billing_action", "View Billing");
      break;
    case "billing_unavailable":
      description = t(
        "create_issue.issue_limit.billing_unavailable_description",
        "Billing actions could not be loaded. Open Billing to try again.",
      );
      actionLabel = t("create_issue.issue_limit.billing_action", "View Billing");
      break;
  }

  const openBilling = () => {
    // Desktop closes the surface when following its billing action
    // (closeForBillingAction in IssueLimitUpgradeDialog).
    onClose();
    if (billingUrl) void Linking.openURL(billingUrl);
  };

  return (
    <View className="w-full max-w-sm rounded-2xl bg-background p-5 gap-3">
      <Text className="text-lg font-semibold text-foreground">
        {t(
          "create_issue.issue_limit.title",
          "This workspace has reached its issue limit",
        )}
      </Text>
      <Text className="text-sm leading-5 text-muted-foreground">
        {description}
      </Text>
      {actionLabel && billingUrl ? (
        <Button onPress={openBilling}>
          <Text>{actionLabel}</Text>
        </Button>
      ) : null}
      <Button variant="secondary" onPress={onClose}>
        <Text>{tCommon("mobile.common.close", "Close")}</Text>
      </Button>
    </View>
  );
}

export function IssueLimitRecoveryDialog({
  visible,
  onClose,
}: {
  visible: boolean;
  onClose: () => void;
}) {
  // Closed state renders nothing, so the recovery queries stay disabled
  // until an issue-limit failure opens the surface.
  if (!visible) return null;
  return (
    <Modal visible transparent animationType="fade" onRequestClose={onClose}>
      <View className="flex-1 items-center justify-center bg-black/50 p-6">
        <IssueLimitRecoveryCard onClose={onClose} />
      </View>
    </Modal>
  );
}
