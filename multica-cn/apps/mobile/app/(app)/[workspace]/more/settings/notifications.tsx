/**
 * Notification preferences subscreen. 6 inbox groups + system_notifications
 * toggle, each backed by an optimistic PATCH /api/notification-preferences.
 *
 * Copy mirrors packages/views/settings/components/notifications-tab.tsx but
 * hardcoded English (mobile has no i18n infra yet). The group labels MUST
 * stay in sync with web — they describe the same server-side semantics,
 * and divergent labels would violate behavioral parity (apps/mobile/CLAUDE.md).
 */
import { ActivityIndicator, ScrollView, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import type {
  NotificationGroupKey,
  NotificationPreferences,
} from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Switch } from "@/components/ui/switch";
import { Separator } from "@/components/ui/separator";
import { useWorkspaceStore } from "@/data/workspace-store";
import { notificationPreferenceOptions } from "@/data/queries/notification-preferences";
import { useUpdateNotificationPreferences } from "@/data/mutations/notification-preferences";
import { useT } from "@/lib/use-t";

const INBOX_GROUP_KEYS: Array<{
  key: Exclude<NotificationGroupKey, "system_notifications">;
  labelKey: string;
  labelFallback: string;
  descKey: string;
  descFallback: string;
}> = [
  {
    key: "assignments",
    labelKey: "notifications.groups.assignments.label",
    labelFallback: "Assignments",
    descKey: "notifications.groups.assignments.description",
    descFallback: "When you're assigned an issue or removed as assignee.",
  },
  {
    key: "status_changes",
    labelKey: "notifications.groups.status_changes.label",
    labelFallback: "Status changes",
    descKey: "notifications.groups.status_changes.description",
    descFallback: "When an issue's status changes.",
  },
  {
    key: "comments",
    labelKey: "notifications.groups.comments.label",
    labelFallback: "Comments",
    descKey: "notifications.groups.comments.description",
    descFallback: "New comments on issues you're subscribed to.",
  },
  {
    key: "mentions",
    labelKey: "notifications.groups.mentions.label",
    labelFallback: "Mentions",
    descKey: "notifications.groups.mentions.description",
    descFallback: "When someone @mentions you, including @all and @squad.",
  },
  {
    key: "updates",
    labelKey: "notifications.groups.updates.label",
    labelFallback: "Priority & Due date",
    descKey: "notifications.groups.updates.description",
    descFallback: "Edits to title, description, labels, priority, or due date.",
  },
  {
    key: "agent_activity",
    labelKey: "notifications.groups.agent_activity.label",
    labelFallback: "Agent activity",
    descKey: "notifications.groups.agent_activity.description",
    descFallback: "When an agent picks up, runs, or completes a task.",
  },
];

export default function NotificationsSettingsScreen() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { t } = useT("settings");
  const { data, isLoading, error } = useQuery(
    notificationPreferenceOptions(wsId),
  );
  const mutation = useUpdateNotificationPreferences();

  const preferences: NotificationPreferences = data?.preferences ?? {};

  const onToggle = (key: NotificationGroupKey, enabled: boolean) => {
    const next: NotificationPreferences = { ...preferences };
    if (enabled) {
      // Default is "all" — omitting the key keeps the object clean.
      delete next[key];
    } else {
      next[key] = "muted";
    }
    mutation.mutate(next);
  };

  const systemEnabled = preferences.system_notifications !== "muted";

  if (isLoading) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <ActivityIndicator />
      </View>
    );
  }

  if (error) {
    return (
      <View className="flex-1 items-center justify-center bg-background px-6">
        <Text className="text-sm text-destructive text-center">
          {t(
            "mobile.notifications.load_failed",
            "Failed to load notification preferences.",
          )}
        </Text>
      </View>
    );
  }

  return (
    <ScrollView
      className="flex-1 bg-background"
      contentContainerClassName="px-4 py-4 gap-6"
    >
      <Section
        title={t("notifications.title", "Inbox Notifications")}
        description={t("notifications.description", "Control which events generate inbox notifications. Muted event types are silently filtered — you can still see them by visiting the issue directly.")}
      >
        {INBOX_GROUP_KEYS.map((group, idx) => {
          const enabled = preferences[group.key] !== "muted";
          const isLast = idx === INBOX_GROUP_KEYS.length - 1;
          return (
            <View key={group.key}>
              <View className="flex-row items-center px-4 py-3 gap-3">
                <View className="flex-1">
                  <Text className="text-base font-medium text-foreground">
                    {t(group.labelKey, group.labelFallback)}
                  </Text>
                  <Text className="text-xs text-muted-foreground mt-0.5">
                    {t(group.descKey, group.descFallback)}
                  </Text>
                </View>
                <Switch
                  checked={enabled}
                  onCheckedChange={(checked) => onToggle(group.key, checked)}
                />
              </View>
              {!isLast ? <Separator /> : null}
            </View>
          );
        })}
      </Section>

      <Section
        title={t("notifications.system.title", "System Notifications")}
        description={t("notifications.system.description", "Control native OS notification banners shown when Multica is in the background.")}
      >
        <View className="flex-row items-center px-4 py-3 gap-3">
          <View className="flex-1">
            <Text className="text-base font-medium text-foreground">
              {t("notifications.system.label", "Show system notifications")}
            </Text>
            <Text className="text-xs text-muted-foreground mt-0.5">
              {t("notifications.system.hint", "Show a banner from your operating system for new inbox items when the app isn't focused.")}
            </Text>
          </View>
          <Switch
            checked={systemEnabled}
            onCheckedChange={(checked) =>
              onToggle("system_notifications", checked)
            }
          />
        </View>
      </Section>
    </ScrollView>
  );
}

function Section({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <View className="gap-2">
      <View className="px-1">
        <Text className="text-xs uppercase tracking-wider text-muted-foreground">
          {title}
        </Text>
        {description ? (
          <Text className="text-xs text-muted-foreground mt-1">
            {description}
          </Text>
        ) : null}
      </View>
      <View className="rounded-md border border-border bg-card overflow-hidden">
        {children}
      </View>
    </View>
  );
}
