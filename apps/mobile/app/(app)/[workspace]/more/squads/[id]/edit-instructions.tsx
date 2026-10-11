/**
 * Squad instructions editor (RUYI-541) — the dedicated formSheet window
 * behind the detail page's truncated preview. RUYI-346 S2 semantics moved
 * here from the detail page: seed once from the payload (never fight a
 * refetch mid-edit), unsaved-leaving guard via usePreventRemove, explicit
 * save feedback.
 *
 * Managers edit and save through useUpdateSquad, whose optimistic cache
 * patch re-paints the detail page behind the sheet — the preview there
 * reflects the saved value on pop. Readers get the full text read-only
 * (same canManage derivation as the detail page, MUL-4223): the preview on
 * the detail page truncates, this window is where the complete instructions
 * live.
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, View } from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { router, useLocalSearchParams } from "expo-router";
import { useNavigation, usePreventRemove } from "@react-navigation/native";
import { useQuery } from "@tanstack/react-query";
import { Text } from "@/components/ui/text";
import { AutosizeTextArea } from "@/components/ui/autosize-textarea";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { squadDetailOptions } from "@/data/queries/squads";
import { memberListOptions } from "@/data/queries/members";
import { useUpdateSquad } from "@/data/mutations/squads";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";

export default function SquadInstructionsEditor() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const squadId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const me = useAuthStore((s) => s.user);
  const { t } = useT("squads");
  const navigation = useNavigation();

  const { data: squad } = useQuery(squadDetailOptions(wsId, squadId));
  const { data: wsMembers } = useQuery(memberListOptions(wsId));
  const updateSquad = useUpdateSquad(squadId);

  const [instructions, setInstructions] = useState("");
  const [seeded, setSeeded] = useState(false);

  // Seed once the payload lands (squad detail page's seed-once pattern:
  // never fight a refetch mid-edit).
  if (squad && !seeded) {
    setInstructions(squad.instructions);
    setSeeded(true);
  }

  const isWorkspaceAdmin = useMemo(() => {
    if (!me) return false;
    const mine = wsMembers?.find((m) => m.user_id === me.id);
    return mine?.role === "owner" || mine?.role === "admin";
  }, [wsMembers, me]);

  const canManage =
    !!squad && (isWorkspaceAdmin || (!!me && squad.creator_id === me.id));

  // S2 (RUYI-418) guard, relocated from the detail page: leaving with
  // unsaved instructions silently discards them — prevent the removal
  // (back gesture, header back, router.back alike) and re-dispatch the
  // original action only after an explicit discard.
  const instructionsDirty = !!squad && instructions !== squad.instructions;
  usePreventRemove(!!instructionsDirty, ({ data }) => {
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
    if (!instructionsDirty || updateSquad.isPending) return;
    updateSquad.mutate(
      { instructions },
      {
        onSuccess: () => {
          Alert.alert(t("toasts.instructions_saved", "Instructions saved"));
          router.back();
        },
        onError: () =>
          Alert.alert(t("name_editor.save_failed", "Failed to save")),
      },
    );
  };

  return (
    <KeyboardAvoidingView className="flex-1 bg-background" behavior="padding">
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false） */}
      <View className="flex-row items-center px-4 pt-3 pb-2 border-b border-border">
        <Text className="flex-1 text-lg font-semibold text-foreground">
          {t("detail_tabs.instructions", "Instructions")}
        </Text>
        {canManage ? (
          <Pressable
            onPress={save}
            disabled={!instructionsDirty || updateSquad.isPending}
            className={`px-2 py-1 ${
              instructionsDirty && !updateSquad.isPending ? "" : "opacity-40"
            }`}
            accessibilityRole="button"
            accessibilityLabel={t("instructions_tab.save_button", "Save")}
          >
            <Text className="text-base text-brand font-semibold">
              {t("instructions_tab.save_button", "Save")}
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
              "instructions_tab.placeholder",
              "e.g. Always start by writing a failing test. Prefer small, atomic commits.",
            )}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            minHeight={320}
            maxHeight={640}
            className="text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2"
            editable={!updateSquad.isPending}
          />
        ) : squad ? (
          <Text selectable className="text-sm leading-5 text-foreground">
            {squad.instructions}
          </Text>
        ) : null}
      </ScrollView>
    </KeyboardAvoidingView>
  );
}
