/**
 * Voice runtimes (`more/runtimes`, RUYI-425 §4.3 mobile) — the mobile
 * configuration surface for voice-protocol runtime instances (Gemini Live).
 * Desktop hosts this on the runtimes page + detail card; mobile gives voice
 * instances their own page reached from the More dropdown, per the stage-4
 * dispatch (独立移动端页面承载，不依赖桌面窄屏响应式).
 *
 * The list shows ONLY voice-protocol instances (`isVoiceProtocolRuntime`,
 * desktop `capabilities.realtime_voice` parity) — CLI/daemon instances keep
 * their existing surfaces (agent slots). Rows surface the three states the
 * §4.3/§4.5 semantics turn on: online/offline, the credential tri-state
 * badge, and the disabled flag (disabled instances stay configured but are
 * excluded from new agent bindings). The create entry is always available —
 * voice is workspace-level, not per-row (desktop runtimes-page parity).
 */
import { useMemo } from "react";
import { FlatList, Pressable, Text as RNText, View } from "react-native";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import { runtimeCredentialStatus, runtimeDisplayName } from "@multica/core/runtimes";
import type { RuntimeDevice } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { isVoiceProtocolRuntime } from "@/lib/voice-runtime";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";
import { cn } from "@/lib/utils";

export default function VoiceRuntimesPage() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { colorScheme } = useColorScheme();
  const { t } = useT("runtimes");

  const { data: runtimes, isLoading } = useQuery(runtimeListOptions(wsId));

  const voiceRuntimes = useMemo(() => {
    const list = (runtimes ?? []).filter(isVoiceProtocolRuntime);
    // Online first (the monitoring order the agents list uses), name asc
    // inside each band. Display name = custom_name override, else name
    // (runtimeDisplayName, MUL-4217 parity).
    return list.sort((a, b) => {
      const rankA = a.status === "online" ? 0 : 1;
      const rankB = b.status === "online" ? 0 : 1;
      if (rankA !== rankB) return rankA - rankB;
      return runtimeDisplayName(a).localeCompare(runtimeDisplayName(b));
    });
  }, [runtimes]);

  const loading = isLoading && voiceRuntimes.length === 0;

  if (loading) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <Text className="text-sm text-muted-foreground">
          {t("mobile.list_loading", "Loading…")}
        </Text>
      </View>
    );
  }

  return (
    <View className="flex-1 bg-background">
      <FlatList
        className="flex-1"
        data={voiceRuntimes}
        keyExtractor={(runtime) => runtime.id}
        ItemSeparatorComponent={() => <View className="h-px bg-border ml-4" />}
        contentContainerClassName="pb-6"
        ListHeaderComponent={
          // Always-available create entry — voice is workspace-level
          // (desktop runtimes page keeps the button outside any row too).
          <Pressable
            onPress={() => {
              if (!wsSlug) return;
              router.push({
                pathname: "/[workspace]/more/runtimes/new",
                params: { workspace: wsSlug },
              });
            }}
            className="flex-row items-center gap-3 px-4 py-3.5 active:bg-secondary border-b border-border"
            accessibilityRole="button"
            accessibilityLabel={t("voice_instance_create.action")}
          >
            <View className="size-8 rounded-full bg-brand/10 items-center justify-center">
              <Ionicons
                name="add"
                size={20}
                color={THEME[colorScheme].brand}
              />
            </View>
            <Text className="flex-1 text-sm font-medium text-brand">
              {t("voice_instance_create.action")}
            </Text>
          </Pressable>
        }
        ListEmptyComponent={
          <View className="flex-1 items-center justify-center px-8 gap-2 pt-20">
            <Ionicons
              name="mic-outline"
              size={42}
              color={THEME[colorScheme].mutedForeground}
            />
            <Text className="text-base font-medium text-foreground text-center">
              {t("mobile.empty.title", "No voice instances yet")}
            </Text>
            <Text className="text-sm text-muted-foreground text-center">
              {t(
                "voice_instance_create.description",
              )}
            </Text>
          </View>
        }
        renderItem={({ item: runtime }) => (
          <VoiceRuntimeRow
            runtime={runtime}
            onPress={() => {
              if (!wsSlug) return;
              router.push({
                pathname: "/[workspace]/more/runtimes/[id]",
                params: { workspace: wsSlug, id: runtime.id },
              });
            }}
          />
        )}
      />
    </View>
  );
}

/**
 * Row: display name + fixed family line, then the tri-state summary on the
 * right (online/offline · credential badge · 已停用 marker). Text-only
 * badges, same convention as the agent form's credential badge.
 */
function VoiceRuntimeRow({
  runtime,
  onPress,
}: {
  runtime: RuntimeDevice;
  onPress: () => void;
}) {
  const { colorScheme } = useColorScheme();
  const { t } = useT("runtimes");
  const credential = runtimeCredentialStatus(runtime);
  const disabled = runtime.metadata?.disabled === true;

  return (
    <Pressable
      onPress={onPress}
      className="flex-row items-center gap-3 bg-background active:bg-secondary px-4 py-3"
      accessibilityLabel={runtimeDisplayName(runtime)}
    >
      <View className="size-10 rounded-full bg-secondary items-center justify-center">
        <Ionicons
          name="mic-outline"
          size={20}
          color={THEME[colorScheme].foreground}
        />
      </View>
      <View className="flex-1 min-w-0 gap-0.5">
        <RNText
          className="text-sm font-medium text-foreground"
          numberOfLines={1}
        >
          {runtimeDisplayName(runtime)}
        </RNText>
        <RNText className="text-xs text-muted-foreground" numberOfLines={1}>
          {runtime.protocol_family || runtime.provider}
          {" · "}
          {runtime.status === "online"
            ? t("mobile.status.online", "Online")
            : t("mobile.status.offline", "Offline")}
        </RNText>
      </View>
      <View className="items-end gap-0.5">
        <CredentialBadge status={credential} />
        {disabled ? (
          <RNText className="text-xs text-muted-foreground">
            {t("mobile.status.disabled", "Disabled")}
          </RNText>
        ) : null}
      </View>
      <Ionicons
        name="chevron-forward"
        size={16}
        color={THEME[colorScheme].mutedForeground}
      />
    </Pressable>
  );
}

// §4.5 credential tri-state — text badge, runtimes:voice_instance.badge_*
// (desktop CredentialBadge parity, RN text styling).
function CredentialBadge({
  status,
}: {
  status: "not_configured" | "configured" | "invalid";
}) {
  const { t } = useT("runtimes");
  return (
    <RNText
      className={cn(
        "text-xs",
        status === "configured" && "text-success",
        status === "invalid" && "text-destructive",
        status === "not_configured" && "text-muted-foreground",
      )}
    >
      {status === "configured"
        ? t("voice_instance.badge_configured")
        : status === "invalid"
          ? t("voice_instance.badge_invalid")
          : t("voice_instance.badge_not_configured")}
    </RNText>
  );
}
