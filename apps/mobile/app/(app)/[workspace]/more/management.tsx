import { Pressable, ScrollView, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { router } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { Text } from "@/components/ui/text";
import { Separator } from "@/components/ui/separator";
import { memberListOptions } from "@/data/queries/members";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

type ManagementRoute = {
  key: string;
  title: string;
  subtitle: string;
  path: string;
};

export default function ManagementPage() {
  const { t } = useT("settings");
  const wsId = useWorkspaceStore((state) => state.currentWorkspaceId);
  const slug = useWorkspaceStore((state) => state.currentWorkspaceSlug);
  const user = useAuthStore((state) => state.user);
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const members = useQuery(memberListOptions(wsId));
  const currentMember = members.data?.find((member) => member.user_id === user?.id);
  const canManageWorkspace =
    currentMember?.role === "owner" || currentMember?.role === "admin";

  const routes: ManagementRoute[] = [
    {
      key: "runtimes",
      title: t("mobile_management.runtimes", "Runtime nodes"),
      subtitle: t("mobile_management.runtimes_subtitle", "Connections, credentials and status"),
      path: "/more/runtimes",
    },
    ...(canManageWorkspace
      ? [
          {
            key: "workspace",
            title: t("mobile_management.workspace", "Workspace settings"),
            subtitle: t("mobile_management.workspace_subtitle", "Members, labels and statuses"),
            path: "/more/workspace-settings",
          },
        ]
      : []),
    {
      key: "integrations",
      title: t("mobile_management.integrations", "Integrations"),
      subtitle: t("mobile_management.integrations_subtitle", "Connected accounts and status"),
      path: "/more/integrations",
    },
    {
      key: "evolution",
      title: t("mobile_management.evolution", "Self-Evolution"),
      subtitle: t("mobile_management.evolution_subtitle", "Proposals, clauses and governance"),
      path: "/more/self-evolution",
    },
    {
      key: "skills",
      title: t("mobile_management.skills", "Skills"),
      subtitle: t("mobile_management.skills_subtitle", "Workspace skill catalog"),
      path: "/more/skills",
    },
    {
      key: "autopilots",
      title: t("mobile_management.autopilots", "Automations"),
      subtitle: t("mobile_management.autopilots_subtitle", "Schedules, runs and quota"),
      path: "/more/autopilots",
    },
    {
      key: "billing",
      title: t("page.tabs.billing", "Billing"),
      subtitle: t("mobile_management.web_only", "Continue in the web app"),
      path: "/more/web-only/billing",
    },
    {
      key: "marketplace",
      title: t("page.tabs.marketplace", "Marketplace"),
      subtitle: t("mobile_management.web_only", "Continue in the web app"),
      path: "/more/web-only/marketplace",
    },
    ...(user?.is_super_admin === true
      ? [
          {
            key: "admin",
            title: t("mobile_management.platform_admin", "Platform administration"),
            subtitle: t("mobile_management.platform_admin_subtitle", "Users, workspaces, MCP and OAuth clients"),
            path: "/more/platform-admin",
          },
        ]
      : []),
  ];

  return (
    <ScrollView className="flex-1 bg-background" contentContainerClassName="px-4 py-4">
      <View className="overflow-hidden rounded-lg border border-border bg-card">
        {routes.map((item, index) => (
          <View key={item.key}>
            <Pressable
              accessibilityRole="button"
              onPress={() => slug && router.push(`/${slug}${item.path}`)}
              className="min-h-16 flex-row items-center gap-3 px-4 py-3 active:bg-secondary"
            >
              <View className="size-9 items-center justify-center rounded-md bg-secondary">
                <Ionicons name={iconFor(item.key)} size={19} color={theme.foreground} />
              </View>
              <View className="min-w-0 flex-1 gap-0.5">
                <Text className="text-sm font-medium text-foreground">{item.title}</Text>
                <Text className="text-xs text-muted-foreground">{item.subtitle}</Text>
              </View>
              <Ionicons name="chevron-forward" size={17} color={theme.mutedForeground} />
            </Pressable>
            {index < routes.length - 1 ? <Separator /> : null}
          </View>
        ))}
      </View>
    </ScrollView>
  );
}

function iconFor(key: string): React.ComponentProps<typeof Ionicons>["name"] {
  switch (key) {
    case "runtimes": return "hardware-chip-outline";
    case "workspace": return "people-outline";
    case "integrations": return "git-network-outline";
    case "evolution": return "git-branch-outline";
    case "skills": return "sparkles-outline";
    case "autopilots": return "timer-outline";
    case "billing": return "card-outline";
    case "marketplace": return "storefront-outline";
    case "admin": return "shield-outline";
    default: return "ellipsis-horizontal";
  }
}
