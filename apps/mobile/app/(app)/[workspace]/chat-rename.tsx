/**
 * Chat session rename sheet — presented as a formSheet by the parent Stack
 * (RUYI-51). Reached from the chat header's ⋯ DropdownMenu ("Rename chat").
 *
 * Self-contained route body per apps/mobile CLAUDE.md Lesson 5 rule 3: reads
 * the session from the TanStack Query cache (the chat tab has it warm), owns
 * its mutation, and router.back()s on commit. No callbacks up to a parent.
 *
 * Container choice: a text input is "anything with a keyboard" → formSheet
 * route (Lesson 5 table). Isolated sheet with no chip-row neighbours, so it
 * overrides `sheetAllowedDetents` with "fitToContents" (see the SHEET_OPTIONS
 * registration in the workspace _layout).
 */
import { useEffect, useState } from "react";
import { View } from "react-native";
import { router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { Text } from "@/components/ui/text";
import { TextField } from "@/components/ui/text-field";
import { Button } from "@/components/ui/button";
import { chatSessionsOptions } from "@/data/queries/chat";
import { useUpdateChatSession } from "@/data/mutations/chat";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";

// Same cap as web's inline rename input (chat-session-header.tsx).
const TITLE_MAX_LENGTH = 200;

export default function ChatRenameRoute() {
  const { sessionId } = useLocalSearchParams<{ sessionId: string }>();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { data: sessions = [] } = useQuery(chatSessionsOptions(wsId));
  const session = sessions.find((s) => s.id === sessionId);
  const { t } = useT("chat");
  const renameSession = useUpdateChatSession();

  const [draft, setDraft] = useState(session?.title ?? "");
  const [saving, setSaving] = useState(false);

  // Re-seed the draft if the authoritative title lands after first render
  // (e.g. the list refetch raced the sheet animation), so the field never
  // edits from a stale seed.
  useEffect(() => {
    if (session) setDraft(session.title ?? "");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session?.id, session?.title]);

  const trimmed = draft.trim();
  const unchanged = !session || trimmed === (session.title ?? "");

  const handleSave = () => {
    if (!session || saving) return;
    if (!trimmed || trimmed === (session.title ?? "")) {
      router.back();
      return;
    }
    setSaving(true);
    // onSettled: dismiss whether the PATCH succeeded or failed — the
    // mutation already rolled the optimistic title back on error and the
    // settle invalidate refetches truth, so a failed rename reads as
    // "nothing changed" instead of trapping the user in the sheet.
    renameSession.mutate(
      { sessionId: session.id, title: trimmed },
      { onSettled: () => router.back() },
    );
  };

  return (
    <View className="flex-1">
      {/* Body-rendered header — SHEET_OPTIONS sets headerShown: false so the
          native bar doesn't fight the grabber (see _layout SHEET_OPTIONS). */}
      <View className="px-4 pt-4 pb-3">
        <Text className="text-base font-semibold text-foreground">
          {t("header.rename", "Rename chat")}
        </Text>
      </View>
      <View className="flex-1 gap-4 px-4">
        <TextField
          value={draft}
          onChangeText={setDraft}
          maxLength={TITLE_MAX_LENGTH}
          placeholder={t("mobile.sessions.untitled", "Untitled chat")}
          autoCapitalize="sentences"
          autoCorrect={false}
          returnKeyType="done"
          onSubmitEditing={handleSave}
        />
        <Button onPress={handleSave} disabled={unchanged || saving}>
          <Text>{t("common:save", "Save")}</Text>
        </Button>
      </View>
    </View>
  );
}
