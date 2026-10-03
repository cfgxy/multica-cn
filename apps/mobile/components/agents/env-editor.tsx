/**
 * Agent env editor (RUYI-346, design §6) — mobile counterpart of web's
 * `packages/views/agents/components/tabs/env-tab.tsx`. Semantics mirror:
 *
 *  - Until the user explicitly confirms, no plaintext reaches this component
 *    — the agent payload carries only `custom_env_key_count` (MUL-2600) and
 *    the `/env` endpoint writes an audit row on every call, so reveal is
 *    always an Alert-confirm away, never a mount effect.
 *  - Revealed plaintext lives in component state only: leaving the screen
 *    destroys it (design §6 "揭示后…离开子屏即销毁内存态").
 *  - Save replaces the map wholesale (PUT /env). We never emit the "****"
 *    mask — the editor only holds revealed plaintext.
 *  - Without manage permission the reveal/edit affordances don't render at
 *    all (hidden, not disabled — web row-actions parity).
 */
import { useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
import * as Haptics from "expo-haptics";
import { Ionicons } from "@expo/vector-icons";
import { Text } from "@/components/ui/text";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { api } from "@/data/api";
import {
  envMapToRows,
  envRowsToMap,
  hasDuplicateKeys,
  newEnvRow,
  type EnvRow,
} from "@/lib/env-rows";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

interface Props {
  agentId: string;
  canManage: boolean;
  /** From the agent payload (`custom_env_key_count`); undefined on older
   *  servers → render the generic not-revealed empty copy. */
  keyCount?: number;
}

export function EnvEditor({ agentId, canManage, keyCount }: Props) {
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");

  // null = not revealed yet; {} (no rows) is a legitimate revealed-empty.
  const [rows, setRows] = useState<EnvRow[] | null>(null);
  const [revealing, setRevealing] = useState(false);
  const [saving, setSaving] = useState(false);

  const reveal = () => {
    Alert.alert(
      t("tab_body.env.reveal_action", "Reveal & edit"),
      t(
        "tab_body.env.not_revealed_hint",
        "Values stay masked until you reveal them. Every reveal and edit is recorded in the workspace audit log.",
      ),
      [
        { text: t("create_dialog.cancel", "Cancel"), style: "cancel" },
        {
          text: t("tab_body.env.reveal_action", "Reveal & edit"),
          onPress: async () => {
            setRevealing(true);
            try {
              const env = await api.getAgentEnv(agentId);
              setRows(envMapToRows(env.custom_env ?? {}));
              Haptics.notificationAsync(
                Haptics.NotificationFeedbackType.Success,
              ).catch(() => {});
            } catch {
              Alert.alert(
                t("tab_body.env.reveal_failed_toast", "Failed to load env values"),
              );
            } finally {
              setRevealing(false);
            }
          },
        },
      ],
    );
  };

  const save = async () => {
    if (rows === null || saving) return;
    if (hasDuplicateKeys(rows)) {
      Alert.alert(
        t("tab_body.env.duplicate_keys_toast", "Duplicate environment variable keys"),
      );
      return;
    }
    setSaving(true);
    try {
      await api.updateAgentEnv(agentId, {
        custom_env: envRowsToMap(rows),
      });
      Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success).catch(
        () => {},
      );
      Alert.alert(t("tab_body.env.saved_toast", "Environment variables saved"));
    } catch {
      Alert.alert(
        t(
          "tab_body.env.save_failed_toast",
          "Failed to save environment variables",
        ),
      );
    } finally {
      setSaving(false);
    }
  };

  // --- Not revealed yet ---
  if (rows === null) {
    return (
      <View className="flex-1 px-4 pt-6 gap-3">
        <Ionicons
          name="lock-closed-outline"
          size={40}
          color={THEME[colorScheme].mutedForeground}
          style={{ alignSelf: "center" }}
        />
        <Text className="text-base font-medium text-foreground text-center">
          {keyCount && keyCount > 0
            ? t("tab_body.env.not_revealed_title", "{{count}} variables configured", {
                count: keyCount,
              })
            : t(
                "tab_body.env.not_revealed_empty",
                "No environment variables configured.",
              )}
        </Text>
        <Text className="text-sm text-muted-foreground text-center">
          {t(
            "tab_body.env.not_revealed_hint",
            "Values stay masked until you reveal them. Every reveal and edit is recorded in the workspace audit log.",
          )}
        </Text>
        {canManage ? (
          <Pressable
            onPress={reveal}
            disabled={revealing}
            className="mt-2 self-center flex-row items-center gap-2 rounded-lg bg-primary px-4 py-2.5 active:opacity-80"
            accessibilityRole="button"
            accessibilityLabel={t("tab_body.env.reveal_action", "Reveal & edit")}
          >
            <Ionicons
              name="eye-outline"
              size={16}
              color={THEME[colorScheme].primaryForeground}
            />
            <Text className="text-sm font-medium text-primary-foreground">
              {revealing
                ? t("tab_body.env.revealing", "Revealing…")
                : t("tab_body.env.reveal_action", "Reveal & edit")}
            </Text>
          </Pressable>
        ) : null}
      </View>
    );
  }

  // --- Revealed: editable rows ---
  return (
    <ScrollView className="flex-1" contentContainerClassName="px-4 pt-3 pb-8 gap-3">
      {rows.length === 0 ? (
        <Text className="text-sm text-muted-foreground py-2">
          {t(
            "tab_body.env.empty_editable",
            "No environment variables configured. Add one to inject it into the agent at launch.",
          )}
        </Text>
      ) : null}
      {rows.map((row) => (
        <View
          key={row.rowId}
          className="rounded-lg border border-border px-3 py-2 gap-1.5"
        >
          <View className="flex-row items-center gap-2">
            <TextInput
              value={row.key}
              onChangeText={(v) =>
                setRows((prev) =>
                  (prev ?? []).map((r) =>
                    r.rowId === row.rowId ? { ...r, key: v } : r,
                  ),
                )
              }
              placeholder={t("tab_body.env.key_placeholder", "KEY")}
              placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
              autoCapitalize="none"
              autoCorrect={false}
              className="flex-1 text-sm font-medium text-foreground"
              editable={!saving}
            />
            <Pressable
              onPress={() =>
                setRows((prev) => (prev ?? []).filter((r) => r.rowId !== row.rowId))
              }
              hitSlop={8}
              accessibilityRole="button"
              accessibilityLabel={t("tab_body.env.remove_aria", "Remove variable")}
            >
              <Ionicons
                name="trash-outline"
                size={16}
                color={THEME[colorScheme].destructive}
              />
            </Pressable>
          </View>
          <TextInput
            value={row.value}
            onChangeText={(v) =>
              setRows((prev) =>
                (prev ?? []).map((r) =>
                  r.rowId === row.rowId ? { ...r, value: v } : r,
                ),
              )
            }
            placeholder={t("tab_body.env.value_placeholder", "value")}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            autoCapitalize="none"
            autoCorrect={false}
            multiline
            className="text-sm text-foreground min-h-8"
            textAlignVertical="top"
            editable={!saving}
          />
        </View>
      ))}
      <Pressable
        onPress={() => setRows((prev) => [...(prev ?? []), newEnvRow()])}
        className="flex-row items-center justify-center gap-2 rounded-lg border border-dashed border-border py-2.5 active:bg-secondary"
        accessibilityRole="button"
        accessibilityLabel={t("mobile.env.add_row", "Add variable")}
      >
        <Ionicons
          name="add"
          size={16}
          color={THEME[colorScheme].mutedForeground}
        />
        <Text className="text-sm text-muted-foreground">
          {t("mobile.env.add_row", "Add variable")}
        </Text>
      </Pressable>
      <Pressable
        onPress={save}
        disabled={saving}
        className={`self-center rounded-lg bg-primary px-6 py-2.5 mt-1 ${
          saving ? "opacity-50" : "active:opacity-80"
        }`}
        accessibilityRole="button"
        accessibilityLabel={t("tab_body.common.save", "Save")}
      >
        <Text className="text-sm font-medium text-primary-foreground">
          {saving
            ? t("webhooks.form_saving", "Saving…")
            : t("tab_body.common.save", "Save")}
        </Text>
      </Pressable>
    </ScrollView>
  );
}
