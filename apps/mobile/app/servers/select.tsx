// Route remains available before authentication, but only while the gate is open.
import { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Alert, Pressable, ScrollView, View } from "react-native";
import { Redirect, router, useFocusEffect } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import { Ionicons } from "@expo/vector-icons";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { Header } from "@/components/ui/header";
import { IconButton } from "@/components/ui/icon-button";
import { probeServer } from "@/data/probe-server";
import { useServerStore } from "@/data/server-store";
import { useStartupServerStore } from "@/data/startup-server-store";
import { useT } from "@/lib/use-t";
import { THEME } from "@/lib/theme";
import { useColorScheme } from "@/lib/use-color-scheme";

const COUNTDOWN_SECONDS = 5;
type ProbeState = "checking" | "reachable" | "unreachable";

export default function ServerSelectScreen() {
  const { t } = useT("settings");
  const { colorScheme } = useColorScheme();
  const servers = useServerStore((s) => s.servers);
  const previousId = useStartupServerStore((s) => s.previousId);
  const phase = useStartupServerStore((s) => s.phase);
  const connectServer = useStartupServerStore((s) => s.connect);
  const [countdown, setCountdown] = useState<number | null>(previousId ? COUNTDOWN_SECONDS : null);
  const [connectingId, setConnectingId] = useState<string | null>(null);
  const [probes, setProbes] = useState<Record<string, ProbeState>>({});
  const controllers = useRef<AbortController[]>([]);
  const connecting = useRef(false);

  const recheck = useCallback(() => {
    controllers.current.forEach((controller) => controller.abort());
    const next = servers.map(() => new AbortController());
    controllers.current = next;
    setProbes(Object.fromEntries(servers.map((server) => [server.id, "checking"])));
    servers.forEach((server, index) => {
      const controller = next[index];
      void probeServer(server.apiUrl, controller.signal).then((reachable) => {
        if (controllers.current !== next) return;
        setProbes((current) => ({ ...current, [server.id]: reachable && !controller.signal.aborted ? "reachable" : "unreachable" }));
      });
    });
  }, [servers]);

  useFocusEffect(useCallback(() => {
    recheck();
    return () => {
      controllers.current.forEach((controller) => controller.abort());
      controllers.current = [];
    };
  }, [recheck]));

  const connect = useCallback(async (id: string) => {
    if (connecting.current) return;
    connecting.current = true;
    setCountdown(null);
    setConnectingId(id);
    try {
      await connectServer(id);
      router.replace("/");
    } catch {
      connecting.current = false;
      setConnectingId(null);
      Alert.alert(t("server.switch_failed_title"), t("server.switch_failed_message"));
    }
  }, [connectServer, t]);

  useEffect(() => {
    if (countdown === null) return;
    if (countdown === 0) {
      if (previousId && servers.some((server) => server.id === previousId)) void connect(previousId);
      else setCountdown(null);
      return;
    }
    const timer = setTimeout(() => setCountdown(countdown - 1), 1_000);
    return () => clearTimeout(timer);
  }, [countdown, previousId, servers, connect]);

  useEffect(() => {
    if (servers.length === 1 && !connecting.current) void connect(servers[0].id);
  }, [servers, connect]);

  const previous = servers.find((s) => s.id === previousId);
  const pending = Object.values(probes).some((state) => state === "checking");
  const interrupt = () => setCountdown(null);

  if (phase !== "select") return <Redirect href="/" />;

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <Header title={t("server.startup.title")} right={
        <IconButton name="add" onPress={() => { interrupt(); router.push("/server-settings/new"); }}
          accessibilityLabel={t("server.add")} />
      } />
      <ScrollView className="flex-1" contentContainerClassName="px-4 py-4 gap-4">
        {countdown !== null && previous ? (
          <View className="rounded-md border border-border bg-card p-3 gap-3">
            <Text accessibilityLiveRegion="polite" className="text-sm text-foreground">
              {t("server.startup.countdown", { n: countdown, name: previous.name || previous.apiUrl })}
            </Text>
            <View className="flex-row gap-2">
              <Button size="sm" disabled={connectingId !== null} onPress={() => void connect(previous.id)}>
                <Text>{t("server.startup.connect_now")}</Text>
              </Button>
              <Button size="sm" variant="ghost" onPress={interrupt}>
                <Text>{t("server.startup.cancel_auto")}</Text>
              </Button>
            </View>
            <View className="h-0.5 bg-muted">
              <View className="h-0.5 bg-primary" style={{ width: `${(countdown / COUNTDOWN_SECONDS) * 100}%` }} />
            </View>
          </View>
        ) : null}
        <View className="overflow-hidden rounded-md border border-border bg-card">
          {servers.map((server) => {
            const state = probes[server.id] ?? "checking";
            const statusLabel = state === "checking" ? t("server.startup.checking")
              : state === "reachable" ? t("server.startup.reachable")
                : t("server.startup.unreachable");
            return (
              <Pressable key={server.id} disabled={connectingId !== null} onPress={() => void connect(server.id)}
                accessibilityRole="button" accessibilityLabel={`${server.name || server.apiUrl}, ${statusLabel}`}
                className="flex-row items-center gap-3 px-4 py-3.5 active:bg-secondary">
                <View className="h-2 w-2 rounded-full" style={{
                  backgroundColor: state === "reachable" ? THEME[colorScheme].success
                    : state === "unreachable" ? THEME[colorScheme].destructive
                      : THEME[colorScheme].mutedForeground,
                }} />
                <View className="min-w-0 flex-1">
                  <Text className="text-base font-medium text-foreground" numberOfLines={1}>{server.name || server.apiUrl}</Text>
                  <Text className="text-xs text-muted-foreground" numberOfLines={1}>{server.apiUrl}</Text>
                </View>
                {server.builtIn ? <Text className="text-xs text-muted-foreground">{t("server.built_in")}</Text> : null}
                {connectingId === server.id ? <ActivityIndicator size="small" />
                  : countdown !== null && server.id === previousId ? <Text className="text-xs text-muted-foreground">{t("server.startup.next")}</Text> : null}
              </Pressable>
            );
          })}
        </View>
        <View className="flex-row items-center justify-between">
          <Button variant="ghost" size="sm" disabled={pending || connectingId !== null} onPress={() => { interrupt(); recheck(); }}>
            <Ionicons name="refresh" size={16} color={THEME[colorScheme].foreground} />
            <Text>{t("server.startup.recheck")}</Text>
          </Button>
          <Button variant="ghost" size="sm" onPress={() => { interrupt(); router.push("/server-settings"); }}>
            <Text>{t("server.manage_title")}</Text>
          </Button>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}
