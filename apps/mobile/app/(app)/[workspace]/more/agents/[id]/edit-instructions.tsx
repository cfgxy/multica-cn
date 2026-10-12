/**
 * Agent instructions editor (`more/agents/[id]/edit-instructions`, RUYI-624)
 * — the dedicated formSheet window behind the detail page's truncated
 * preview, mirroring the squad editor (more/squads/[id]/edit-instructions,
 * RUYI-541). Before this screen every instructions tap-through landed in
 * edit-profile, whose half-height sheet opens on the instructions textarea —
 * owners read it as "every entry opens the same instruction box".
 *
 * Seed once from the payload (never fight a refetch mid-edit), unsaved-
 * leaving guard via usePreventRemove, explicit save feedback. Managers edit
 * and save through useUpdateAgent, whose optimistic cache patch re-paints
 * the detail page behind the sheet; readers tap through to the same window
 * in its read-only mode (hide-not-disable permission rule, RUYI-346).
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, View } from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { router, useLocalSearchParams } from "expo-router";
import { useNavigation, usePreventRemove } from "@react-navigation/native";
import { useQuery } from "@tanstack/react-query";
import * as Haptics from "expo-haptics";
import { Text } from "@/components/ui/text";
import { AutosizeTextArea } from "@/components/ui/autosize-textarea";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { agentDetailOptions } from "@/data/queries/agents";
import { memberListOptions } from "@/data/queries/members";
import { useUpdateAgent } from "@/data/mutations/agents";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";

export default function AgentInstructionsEditor() {
  const insets = useSafeAreaInsets();
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const me = useAuthStore((s) => s.user);
  const { t } = useT("agents");
  const navigation = useNavigation();

  const { data: agent } = useQuery(agentDetailOptions(wsId, agentId));
  const { data: wsMembers } = useQuery(memberListOptions(wsId));
  const update = useUpdateAgent(agentId);

  const [instructions, setInstructions] = useState("");
  const [seeded, setSeeded] = useState(false);

  // Seed once the payload lands (detail page's seed-once pattern: never
  // fight a refetch mid-edit).
  if (agent && agent.id && !seeded) {
    setInstructions(agent.instructions);
    setSeeded(true);
  }

  // Same canManage derivation as the detail page: workspace admin or owner.
  const isWorkspaceAdmin = useMemo(() => {
    if (!me) return false;
    const mine = wsMembers?.find((m) => m.user_id === me.id);
    return mine?.role === "owner" || mine?.role === "admin";
  }, [wsMembers, me]);
  const canManage =
    !!agent && (isWorkspaceAdmin || (!!me && agent.owner_id === me.id));

  // S2 (RUYI-418) guard, same as the squad editor: leaving with unsaved
  // instructions silently discards them — prevent the removal and
  // re-dispatch the original action only after an explicit discard.
  const dirty =
    !!agent && agent.id && seeded && instructions !== agent.instructions;
  usePreventRemove(!!dirty, ({ data }) => {
    Alert.alert(
      t("mobile.detail.unsaved_title", "Unsaved changes"),
      t(
        "mobile.detail.unsaved_body",
        "Your edits to the instructions haven't been saved yet.",
      ),
      [
        {
          text: t("mobile.detail.unsaved_keep", "Keep editing"),
          style: "cancel",
        },
        {
          text: t("mobile.detail.unsaved_discard", "Discard"),
          style: "destructive",
          onPress: () => navigation.dispatch(data.action),
        },
      ],
    );
  });

  const save = () => {
    if (!dirty || update.isPending) return;
    update.mutate(
      { instructions },
      {
        onSuccess: () => {
          Haptics.notificationAsync(
            Haptics.NotificationFeedbackType.Success,
          ).catch(() => {});
          Alert.alert(
            t("mobile.detail.instructions_saved", "Instructions saved"),
          );
          router.back();
        },
        onError: (err) => {
          Alert.alert(
            t("detail.update_failed_toast", "Failed to update agent"),
            err instanceof Error ? err.message : undefined,
          );
        },
      },
    );
  };

  return (
    <KeyboardAvoidingView className="flex-1 bg-background" behavior="padding">
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false）；顶部让出系统状态栏（RUYI-563）。 */}
      <View
        className="flex-row items-center px-4 pb-2 border-b border-border"
        style={{ paddingTop: insets.top + 12 }}
      >
        <Text className="flex-1 text-lg font-semibold text-foreground">
          {t("tabs.instructions", "Instructions")}
        </Text>
        {canManage ? (
          <Pressable
            onPress={save}
            disabled={!dirty || update.isPending}
            className={`px-2 py-1 ${
              dirty && !update.isPending ? "" : "opacity-40"
            }`}
            accessibilityRole="button"
            accessibilityLabel={t("tab_body.common.save", "Save")}
          >
            <Text className="text-base text-brand font-semibold">
              {t("tab_body.common.save", "Save")}
            </Text>
          </Pressable>
        ) : null}
      </View>
      <ScrollView
        nestedScrollEnabled
        className="flex-1"
        contentContainerClassName="px-4 pt-4 pb-8"
        keyboardShouldPersistTaps="handled"
      >
        {canManage ? (
          <AutosizeTextArea
            value={instructions}
            onChangeText={setInstructions}
            placeholder={t(
              "create_dialog.instructions.editor_placeholder",
              "Write what this agent should do, what to focus on, what to avoid…",
            )}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            minHeight={320}
            maxHeight={640}
            className="text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2"
            editable={!update.isPending}
          />
        ) : agent ? (
          <Text selectable className="text-sm leading-5 text-foreground">
            {agent.instructions}
          </Text>
        ) : null}
      </ScrollView>
    </KeyboardAvoidingView>
  );
}
