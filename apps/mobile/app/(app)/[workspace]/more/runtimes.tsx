import { useMemo, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { router } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { runtimeDisplayLabel } from "@multica/core/runtimes";
import type { RuntimeDevice } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Separator } from "@/components/ui/separator";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";
import { runtimeCredentialDisplayState, runtimeNodeKind } from "@/lib/runtime-node";

type NodeFilter = "all" | "voice" | "cli" | "daemon";

export default function RuntimeNodesPage() {
  const wsId = useWorkspaceStore((state) => state.currentWorkspaceId);
  const slug = useWorkspaceStore((state) => state.currentWorkspaceSlug);
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const { t } = useT("runtimes");
  const [filter, setFilter] = useState<NodeFilter>("all");
  const runtimes = useQuery(runtimeListOptions(wsId));
  const filtered = useMemo(
    () => (runtimes.data ?? []).filter((runtime) => filter === "all" || runtimeNodeKind(runtime) === filter),
    [filter, runtimes.data],
  );

  return (
    <ScrollView className="flex-1 bg-background" contentContainerClassName="gap-4 px-4 py-4">
      <View className="flex-row gap-2">
        {(["all", "voice", "cli", "daemon"] as const).map((value) => {
          const active = filter === value;
          return (
            <Pressable
              key={value}
              accessibilityRole="button"
              accessibilityState={{ selected: active }}
              onPress={() => setFilter(value)}
              className={`min-h-9 flex-1 items-center justify-center rounded-md border px-2 ${active ? "border-primary bg-primary" : "border-border bg-background"}`}
            >
              <Text className={`text-xs font-medium ${active ? "text-primary-foreground" : "text-foreground"}`}>
                {filterLabel(value, t)}
              </Text>
            </Pressable>
          );
        })}
      </View>

      {runtimes.isLoading ? (
        <View className="items-center py-10"><ActivityIndicator /></View>
      ) : runtimes.isError ? (
        <View className="items-center gap-2 py-10">
          <Text className="text-sm text-destructive">{t("page.load_failed", "Couldn't load runtimes")}</Text>
          <Pressable onPress={() => void runtimes.refetch()} accessibilityRole="button">
            <Text className="text-sm font-medium text-primary">{t("page.retry", "Retry")}</Text>
          </Pressable>
        </View>
      ) : filtered.length === 0 ? (
        <View className="items-center gap-1 py-10">
          <Text className="text-sm font-medium text-foreground">
            {(runtimes.data ?? []).length === 0
              ? t("page.empty.title", "No runtimes yet")
              : t("machine.no_matches_title", "No machines")}
          </Text>
          <Text className="text-xs text-muted-foreground">
            {(runtimes.data ?? []).length === 0
              ? t("page.empty.hint", "Desktop auto-scans this computer. To use another machine — a server, dev box, or any computer with a terminal — add it here.")
              : t("machine.no_matches_hint", "No machines match the current search or filter.")}
          </Text>
        </View>
      ) : (
        <View className="overflow-hidden rounded-lg border border-border bg-card">
          {filtered.map((runtime, index) => (
            <View key={runtime.id}>
              <Pressable
                accessibilityRole="button"
                onPress={() => slug && router.push(`/${slug}/more/runtimes/${runtime.id}`)}
                className="min-h-16 flex-row items-center gap-3 px-4 py-3 active:bg-secondary"
              >
                <View className="size-9 items-center justify-center rounded-md bg-secondary">
                  <Ionicons name={runtimeIcon(runtimeNodeKind(runtime))} size={19} color={theme.foreground} />
                </View>
                <View className="min-w-0 flex-1 gap-1">
                  <Text numberOfLines={1} className="text-sm font-medium text-foreground">
                    {runtimeDisplayLabel(runtime)}
                  </Text>
                  <Text numberOfLines={1} className="text-xs text-muted-foreground">
                    {`${filterLabel(runtimeNodeKind(runtime), t)} · ${runtime.status === "online" ? t("status.online", "Online") : t("status.offline", "Offline")}`}
                  </Text>
                </View>
                <CredentialBadge status={runtime.credential_status} />
                <Ionicons name="chevron-forward" size={17} color={theme.mutedForeground} />
              </Pressable>
              {index < filtered.length - 1 ? <Separator /> : null}
            </View>
          ))}
        </View>
      )}
    </ScrollView>
  );
}

function filterLabel(filter: NodeFilter, t: (key: string, fallback: string) => string): string {
  switch (filter) {
    case "all": return t("machine.filters.all", "All");
    case "voice": return t("mobile_nodes.voice", "Voice");
    case "cli": return t("machine.metrics.cli", "CLI");
    case "daemon": return t("machine.metrics.local_daemon", "Local daemon");
    default: return t("machine.filters.all", "All");
  }
}

function runtimeIcon(type: Exclude<NodeFilter, "all">): React.ComponentProps<typeof Ionicons>["name"] {
  switch (type) {
    case "voice": return "mic-outline";
    case "cli": return "terminal-outline";
    case "daemon": return "hardware-chip-outline";
    default: return "hardware-chip-outline";
  }
}

function CredentialBadge({ status }: { status: RuntimeDevice["credential_status"] }) {
  const { t } = useT("runtimes");
  const key = runtimeCredentialDisplayState(status);
  const text = key === "configured"
    ? t("mobile_nodes.credential_configured", "Configured")
    : key === "error"
      ? t("mobile_nodes.credential_error", "Issue")
      : t("mobile_nodes.credential_missing", "Not configured");
  const tone = key === "configured" ? "text-success" : key === "error" ? "text-destructive" : "text-muted-foreground";
  return <Text className={`text-[10px] ${tone}`}>{text}</Text>;
}
