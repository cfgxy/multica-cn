/**
 * Custom CLI args (`more/agents/[id]/custom-args`, RUYI-418 B2) — mobile
 * mirror of packages/views/agents/components/tabs/custom-args-tab.tsx: an
 * ordered list of raw argv strings appended to the runtime's launch header.
 * Add/edit happens inline (one editor at a time), remove is immediate like
 * web's trash button, and a single Save commits the whole `custom_args`
 * array. The launch-command preview joins the runtime's `launch_header`
 * with the quoted-when-spaces args, same as web's formatArgForPreview.
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { createSafeId } from "@multica/core/utils";
import { Text } from "@/components/ui/text";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { agentDetailOptions } from "@/data/queries/agents";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { useUpdateAgent } from "@/data/mutations/agents";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

interface ArgEntry {
  id: string;
  value: string;
}

function argsToEntries(args: string[]): ArgEntry[] {
  return args.map((value) => ({ id: createSafeId(), value }));
}

function entriesToArgs(entries: ArgEntry[]): string[] {
  return entries.map((entry) => entry.value.trim()).filter(Boolean);
}

function formatArgForPreview(value: string): string {
  return /\s/.test(value) ? JSON.stringify(value) : value;
}

export default function AgentCustomArgs() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const me = useAuthStore((s) => s.user);
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");
  const update = useUpdateAgent(agentId);

  const { data: agent } = useQuery(agentDetailOptions(wsId, agentId));
  const { data: runtimes } = useQuery(runtimeListOptions(wsId));
  const launchHeader = useMemo(() => {
    if (!agent || !runtimes) return null;
    const runtime = runtimes.find((r) => r.id === agent.runtime_id);
    return runtime?.launch_header ?? null;
  }, [agent, runtimes]);

  const [entries, setEntries] = useState<ArgEntry[]>([]);
  const [editor, setEditor] = useState<
    { kind: "add" } | { kind: "edit"; entryId: string } | null
  >(null);
  const [editorValue, setEditorValue] = useState("");
  const [seeded, setSeeded] = useState(false);

  if (!seeded && agent) {
    setEntries(argsToEntries(agent.custom_args ?? []));
    setSeeded(true);
  }

  const currentArgs = useMemo(() => entriesToArgs(entries), [entries]);
  const originalArgs = agent?.custom_args ?? [];
  const dirty =
    seeded && JSON.stringify(currentArgs) !== JSON.stringify(originalArgs);
  const canSave = dirty && editor === null && !update.isPending;

  const closeEditor = () => {
    setEditor(null);
    setEditorValue("");
  };

  const commitEditor = () => {
    const value = editorValue.trim();
    if (!editor || !value) return;
    if (editor.kind === "add") {
      setEntries((current) => [...current, { id: createSafeId(), value }]);
    } else {
      setEntries((current) =>
        current.map((entry) =>
          entry.id === editor.entryId ? { ...entry, value } : entry,
        ),
      );
    }
    closeEditor();
  };

  const removeEntry = (entryId: string) => {
    setEntries((current) => current.filter((entry) => entry.id !== entryId));
    if (editor?.kind === "edit" && editor.entryId === entryId) closeEditor();
  };

  const onSave = () => {
    if (!canSave) return;
    update.mutate(
      { custom_args: currentArgs },
      {
        onSuccess: () => router.back(),
        onError: (err) => {
          Alert.alert(
            t("tab_body.custom_args.save_failed_toast", "Failed to save custom arguments"),
            err instanceof Error ? err.message : undefined,
          );
        },
      },
    );
  };

  const launchCommand = launchHeader
    ? [launchHeader, ...currentArgs.map(formatArgForPreview)].join(" ")
    : null;
  // Edit is canManage-gated on the web inspector; non-owners get a read-only
  // list (same posture as the other agent settings screens here).
  const isOwner = !!agent && !!me && agent.owner_id === me.id;

  return (
    <View className="flex-1 bg-background">
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false） */}
      <View className="flex-row items-center px-4 pt-3 pb-2 border-b border-border">
        <Text className="flex-1 text-lg font-semibold text-foreground">
          {t("tabs.custom_args", "Custom Args")}
        </Text>
        {isOwner ? (
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
        ) : null}
      </View>

      <ScrollView
        className="flex-1"
        contentContainerClassName="px-4 pt-4 pb-8 gap-4"
        keyboardShouldPersistTaps="handled"
      >
        <Text className="text-xs text-muted-foreground leading-5">
          {t(
            "tab_body.custom_args.intro",
            "Append custom arguments to this agent's runtime launch command.",
          )}
        </Text>

        <View className="gap-1.5">
          <Text className="text-xs uppercase tracking-wider text-muted-foreground">
            {t("tab_body.custom_args.arguments_label", "Arguments")}
          </Text>

          {entries.length === 0 && editor?.kind !== "add" ? (
            <View className="rounded-md border border-border px-4 py-6 items-center gap-1">
              <Text className="text-sm font-medium text-foreground">
                {t("tab_body.custom_args.empty_title", "No arguments yet")}
              </Text>
              <Text className="text-xs text-muted-foreground text-center">
                {t(
                  "tab_body.custom_args.empty_hint",
                  "Add the first token to build the launch command.",
                )}
              </Text>
            </View>
          ) : null}

          {entries.map((entry, index) =>
            editor?.kind === "edit" && editor.entryId === entry.id ? (
              <EditorCard
                key={entry.id}
                value={editorValue}
                onChange={setEditorValue}
                onCancel={closeEditor}
                onSubmit={commitEditor}
                placeholder={t(
                  "tab_body.custom_args.input_placeholder",
                  "--profile",
                )}
                submitLabel={t("tab_body.custom_args.update_action", "Update")}
                cancelLabel={t("tab_body.custom_args.cancel_action", "Cancel")}
              />
            ) : (
              <View
                key={entry.id}
                className="flex-row items-center gap-3 rounded-md bg-muted/45 px-3 py-2.5"
              >
                <Text className="w-5 text-center text-xs tabular-nums text-muted-foreground">
                  {index + 1}
                </Text>
                <Text className="flex-1 text-sm text-foreground font-mono" numberOfLines={2}>
                  {entry.value}
                </Text>
                {isOwner && editor === null ? (
                  <View className="flex-row items-center">
                    <Pressable
                      onPress={() => {
                        setEditor({ kind: "edit", entryId: entry.id });
                        setEditorValue(entry.value);
                      }}
                      className="px-2 py-1.5"
                      hitSlop={4}
                      accessibilityRole="button"
                      accessibilityLabel={t(
                        "tab_body.custom_args.edit_aria",
                        "Edit argument {{index}}",
                        { index: index + 1 },
                      )}
                    >
                      <Ionicons
                        name="pencil-outline"
                        size={16}
                        color={THEME[colorScheme].mutedForeground}
                      />
                    </Pressable>
                    <Pressable
                      onPress={() => removeEntry(entry.id)}
                      className="px-2 py-1.5"
                      hitSlop={4}
                      accessibilityRole="button"
                      accessibilityLabel={t(
                        "tab_body.custom_args.remove_aria",
                        "Remove argument {{index}}",
                        { index: index + 1 },
                      )}
                    >
                      <Ionicons
                        name="trash-outline"
                        size={16}
                        color={THEME[colorScheme].destructive}
                      />
                    </Pressable>
                  </View>
                ) : null}
              </View>
            ),
          )}

          {editor?.kind === "add" ? (
            <EditorCard
              value={editorValue}
              onChange={setEditorValue}
              onCancel={closeEditor}
              onSubmit={commitEditor}
              placeholder={t(
                "tab_body.custom_args.input_placeholder",
                "--profile",
              )}
              submitLabel={t("tab_body.custom_args.add_action", "Add")}
              cancelLabel={t("tab_body.custom_args.cancel_action", "Cancel")}
            />
          ) : null}

          {isOwner && editor === null ? (
            <Pressable
              onPress={() => {
                setEditor({ kind: "add" });
                setEditorValue("");
              }}
              className="flex-row items-center justify-center gap-2 rounded-md border border-dashed border-border px-3 py-2.5 active:bg-secondary"
              accessibilityRole="button"
              accessibilityLabel={t(
                "tab_body.custom_args.add_argument_action",
                "Add argument",
              )}
            >
              <Ionicons
                name="add"
                size={16}
                color={THEME[colorScheme].mutedForeground}
              />
              <Text className="text-sm text-muted-foreground">
                {t("tab_body.custom_args.add_argument_action", "Add argument")}
              </Text>
            </Pressable>
          ) : null}
        </View>

        {launchCommand ? (
          <View className="gap-1.5">
            <Text className="text-xs uppercase tracking-wider text-muted-foreground">
              {t(
                "tab_body.custom_args.command_preview_label",
                "Command preview",
              )}
            </Text>
            <View className="rounded-md bg-muted/45 px-3 py-2.5">
              <Text className="text-xs text-foreground font-mono" selectable>
                {launchCommand}
              </Text>
            </View>
          </View>
        ) : null}
      </ScrollView>
    </View>
  );
}

function EditorCard({
  value,
  onChange,
  onCancel,
  onSubmit,
  placeholder,
  submitLabel,
  cancelLabel,
}: {
  value: string;
  onChange: (v: string) => void;
  onCancel: () => void;
  onSubmit: () => void;
  placeholder: string;
  submitLabel: string;
  cancelLabel: string;
}) {
  return (
    <View className="rounded-md border border-border px-3 py-2.5 gap-2">
      <TextInput
        value={value}
        onChangeText={onChange}
        placeholder={placeholder}
        placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
        autoCapitalize="none"
        autoCorrect={false}
        autoFocus
        onSubmitEditing={onSubmit}
        className="text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2 font-mono"
      />
      <View className="flex-row justify-end gap-4">
        <Pressable onPress={onCancel} className="px-2 py-1" hitSlop={4}>
          <Text className="text-sm text-muted-foreground">{cancelLabel}</Text>
        </Pressable>
        <Pressable
          onPress={onSubmit}
          disabled={!value.trim()}
          className={`px-2 py-1 ${value.trim() ? "" : "opacity-40"}`}
          hitSlop={4}
        >
          <Text className="text-sm text-brand font-semibold">{submitLabel}</Text>
        </Pressable>
      </View>
    </View>
  );
}
