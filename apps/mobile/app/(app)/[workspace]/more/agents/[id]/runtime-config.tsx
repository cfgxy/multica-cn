/**
 * OpenClaw routing config (`more/agents/[id]/runtime-config`, RUYI-418 B2) —
 * mobile mirror of packages/views/agents/components/tabs/runtime-config-tab.tsx:
 * local/gateway routing mode plus the gateway pin (host / port / token /
 * TLS), saved as `runtime_config` via UpdateAgentRequest.
 *
 * The mask-sentinel dance is load-bearing: the API substitutes
 * OPENCLAW_GATEWAY_TOKEN_MASK for a saved token on every read, so the form
 * shows an empty field with a "saved" placeholder and remembers
 * `tokenWasMasked` — submitting with the field untouched replays the
 * sentinel so the server preserves the persisted value. Any keystroke
 * clears the flag (an empty field then genuinely clears the token), and
 * switching modes also clears it (web CR for issue #3260).
 *
 * parse/serialize preserve `allow_subagents`, so saving routing never wipes
 * the subagent toggle set in the profile editor (see
 * openclaw-runtime-config.ts header comment).
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useQuery } from "@tanstack/react-query";
import { router, useLocalSearchParams } from "expo-router";
import {
  OPENCLAW_GATEWAY_TOKEN_MASK,
  openclawRuntimeConfigEquals,
  parseOpenclawRuntimeConfig,
  serializeOpenclawRuntimeConfig,
  type OpenclawRoutingMode,
} from "@multica/core/agents/openclaw-runtime-config";
import { Text } from "@/components/ui/text";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { Switch } from "@/components/ui/switch";
import { agentDetailOptions } from "@/data/queries/agents";
import { useUpdateAgent } from "@/data/mutations/agents";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";

export default function AgentRuntimeConfig() {
  const insets = useSafeAreaInsets();
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { t } = useT("agents");
  const update = useUpdateAgent(agentId);

  const { data: agent } = useQuery(agentDetailOptions(wsId, agentId));

  const [mode, setMode] = useState<OpenclawRoutingMode>("local");
  const [host, setHost] = useState("");
  const [port, setPort] = useState("");
  const [token, setToken] = useState("");
  const [tls, setTls] = useState(false);
  const [tokenWasMasked, setTokenWasMasked] = useState(false);
  const [seeded, setSeeded] = useState(false);

  // Reseed from the server payload whenever the agent has no in-flight local
  // edits (web's previousFormRef sync, simplified: mobile saves in one shot
  // and navigates back, so a straight "seed once per server value" is enough
  // — the form only drifts from the server between keystroke and save).
  const original = useMemo(
    () => parseOpenclawRuntimeConfig(agent?.runtime_config),
    [agent?.runtime_config],
  );
  if (!seeded && agent) {
    const masked = original.gateway?.token === OPENCLAW_GATEWAY_TOKEN_MASK;
    setMode(original.mode ?? "local");
    setHost(original.gateway?.host ?? "");
    setPort(original.gateway?.port ? String(original.gateway.port) : "");
    // Never render the sentinel into the input — it would let users
    // accidentally edit it (web configToForm).
    setToken(masked ? "" : (original.gateway?.token ?? ""));
    setTls(original.gateway?.tls === true);
    setTokenWasMasked(masked);
    setSeeded(true);
  }

  // Build the typed config from the draft (web formToConfig).
  const draft = useMemo(() => {
    const cfg = parseOpenclawRuntimeConfig(null);
    cfg.mode = mode;
    if (mode === "gateway") {
      const gw: NonNullable<
        ReturnType<typeof parseOpenclawRuntimeConfig>["gateway"]
      > = {};
      if (host.trim() !== "") gw.host = host.trim();
      const portNum = Number.parseInt(port, 10);
      if (Number.isFinite(portNum) && portNum > 0) gw.port = portNum;
      if (tls) gw.tls = true;
      if (tokenWasMasked && token === "") {
        // Untouched saved token — replay the sentinel so the server's
        // preserve hook keeps the persisted value.
        gw.token = OPENCLAW_GATEWAY_TOKEN_MASK;
      } else if (token !== "") {
        gw.token = token;
      }
      if (Object.keys(gw).length > 0) cfg.gateway = gw;
    }
    return cfg;
  }, [mode, host, port, token, tls, tokenWasMasked]);

  const dirty = seeded && !openclawRuntimeConfigEquals(original, draft);
  const portValid = port === "" || /^\d+$/.test(port);
  const canSave = seeded && dirty && portValid && !update.isPending;

  const onSave = () => {
    if (!canSave) return;
    update.mutate(
      { runtime_config: serializeOpenclawRuntimeConfig(draft) },
      {
        onSuccess: () => router.back(),
        onError: (err) => {
          Alert.alert(
            t("tab_body.runtime_config.save_failed_toast", "Failed to save routing config"),
            err instanceof Error ? err.message : undefined,
          );
        },
      },
    );
  };

  const isGateway = mode === "gateway";

  return (
    <KeyboardAvoidingView className="flex-1 bg-background" behavior="padding">
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false）；顶部让出系统状态栏（RUYI-563）。 */}
      <View
        className="flex-row items-center px-4 pb-2 border-b border-border"
        style={{ paddingTop: insets.top + 12 }}
      >
        <Text className="flex-1 text-lg font-semibold text-foreground">
          {t("tabs.runtime_config", "Routing")}
        </Text>
        <Pressable
          onPress={onSave}
          disabled={!canSave}
          className={`px-2 py-1 ${canSave ? "" : "opacity-40"}`}
          accessibilityRole="button"
          accessibilityLabel={t("tab_body.common.save", "Save")}
        >
          <Text className="text-base text-brand font-semibold">
            {update.isPending
              ? t("create_dialog.creating", "Creating...")
              : t("tab_body.common.save", "Save")}
          </Text>
        </Pressable>
      </View>

      <ScrollView
        className="flex-1"
        contentContainerClassName="px-4 pt-4 pb-8 gap-4"
        keyboardShouldPersistTaps="handled"
      >
        <Text className="text-xs text-muted-foreground leading-5">
          {t(
            "tab_body.runtime_config.intro",
            "Choose how the OpenClaw runtime executes this agent's turns.",
          )}
        </Text>

        <Field label={t("tab_body.runtime_config.mode_label", "Routing mode")}>
          <View className="flex-row gap-2">
            {(["local", "gateway"] as const).map((m) => {
              const selected = mode === m;
              return (
                <Pressable
                  key={m}
                  onPress={() => {
                    if (mode === m) return;
                    setMode(m);
                    // Mode flips clear the masked-replay flag — otherwise a
                    // gateway → local → gateway round trip would silently
                    // restore a token the user never intended to keep.
                    setTokenWasMasked(false);
                  }}
                  className={`rounded-md border px-3 py-1.5 ${
                    selected ? "border-brand bg-brand/10" : "border-border"
                  }`}
                  accessibilityRole="radio"
                  accessibilityState={{ selected }}
                >
                  <Text
                    className={`text-xs ${selected ? "text-brand font-medium" : "text-muted-foreground"}`}
                  >
                    {m === "local"
                      ? t("tab_body.runtime_config.mode_local", "Local")
                      : t("tab_body.runtime_config.mode_gateway", "Gateway")}
                  </Text>
                </Pressable>
              );
            })}
          </View>
          <Text className="text-xs text-muted-foreground leading-5">
            {isGateway
              ? t(
                  "tab_body.runtime_config.mode_gateway_hint",
                  "The daemon dials an OpenClaw Gateway. Leave the endpoint fields blank to inherit them from the daemon host's `~/.openclaw/openclaw.json`.",
                )
              : t(
                  "tab_body.runtime_config.mode_local_hint",
                  "Default. The daemon spawns `openclaw agent --local …` and the agent loop runs on this host.",
                )}
          </Text>
        </Field>

        <Field label={t("tab_body.runtime_config.gateway_legend", "Gateway endpoint")}>
          <View className={`gap-3 ${isGateway ? "" : "opacity-50"}`}>
            <View className="gap-1">
              <Text className="text-xs text-muted-foreground">
                {t("tab_body.runtime_config.host_label", "Host")}
              </Text>
              <TextInput
                value={host}
                onChangeText={setHost}
                placeholder={t("tab_body.runtime_config.host_placeholder", "gw.internal")}
                placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
                autoCapitalize="none"
                autoCorrect={false}
                className="text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2 font-mono"
                editable={isGateway && !update.isPending}
              />
            </View>
            <View className="gap-1">
              <Text className="text-xs text-muted-foreground">
                {t("tab_body.runtime_config.port_label", "Port")}
              </Text>
              <TextInput
                value={port}
                onChangeText={(v) => setPort(v.replace(/[^0-9]/g, ""))}
                placeholder="18789"
                placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
                keyboardType="number-pad"
                className="text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2 font-mono"
                editable={isGateway && !update.isPending}
              />
              {!portValid ? (
                <Text className="text-xs text-destructive">
                  {t("tab_body.runtime_config.port_invalid", "Port must be a positive integer.")}
                </Text>
              ) : null}
            </View>
            <View className="gap-1">
              <Text className="text-xs text-muted-foreground">
                {t("tab_body.runtime_config.token_label", "Auth token")}
              </Text>
              <TextInput
                value={token}
                onChangeText={(v) => {
                  setToken(v);
                  // User touched the field — a later empty input genuinely
                  // clears the persisted token instead of preserving it.
                  setTokenWasMasked(false);
                }}
                secureTextEntry
                autoComplete="off"
                placeholder={
                  tokenWasMasked
                    ? t(
                        "tab_body.runtime_config.token_masked_placeholder",
                        "Saved — submit a new value to rotate, or leave blank to keep",
                      )
                    : t(
                        "tab_body.runtime_config.token_placeholder",
                        "Bearer token issued by the gateway",
                      )
                }
                placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
                autoCapitalize="none"
                autoCorrect={false}
                className="text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2 font-mono"
                editable={isGateway && !update.isPending}
              />
            </View>
            <View className="flex-row items-center justify-between gap-3 pt-1">
              <View className="flex-1 gap-0.5">
                <Text className="text-sm font-medium text-foreground">
                  {t("tab_body.runtime_config.tls_label", "Use TLS")}
                </Text>
                <Text className="text-xs text-muted-foreground">
                  {t("tab_body.runtime_config.tls_hint", "Dial https:// instead of http://")}
                </Text>
              </View>
              <Switch
                checked={tls}
                disabled={!isGateway || update.isPending}
                onCheckedChange={(checked) => setTls(checked)}
                aria-label={t("tab_body.runtime_config.tls_label", "Use TLS")}
              />
            </View>
          </View>
        </Field>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <View className="gap-1.5">
      <Text className="text-xs uppercase tracking-wider text-muted-foreground">
        {label}
      </Text>
      {children}
    </View>
  );
}
