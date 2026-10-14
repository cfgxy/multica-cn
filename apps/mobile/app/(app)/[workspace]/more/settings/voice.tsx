/**
 * Workspace voice management subscreen (RUYI-749 B-series): the provider
 * pool (list + create/rename/enable/delete, API key PUT with 保存即探针,
 * manual re-probe) plus the two chain levels every member can read —
 * workspace default and ASR. Mobile mirror of web's voice-tab.tsx, sharing
 * the pure chain helpers from @multica/core/voice/chain so the eligibility
 * rule ("gemini + enabled + configured + not probe-invalid") can never
 * drift between platforms.
 *
 * Writes are owner/admin (server-enforced); members get a read-only view
 * with the admin hint, same as web. The settings PATCH replaces the whole
 * bag, so a chain save merges the current workspace.settings and sends it
 * whole (same merge discipline as web's saveChain).
 */
import { useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Modal,
  Pressable,
  ScrollView,
  TextInput,
  View,
} from "react-native";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useQuery } from "@tanstack/react-query";
import { SheetHeader } from "@/components/ui/sheet-header";
import { Text } from "@/components/ui/text";
import { Switch } from "@/components/ui/switch";
import { Separator } from "@/components/ui/separator";
import { TextField } from "@/components/ui/text-field";
import { Field, RadioDot } from "@/components/agents/agent-editor-fields";
import { useActionSheet, ActionSheetModal } from "@/components/ui/action-sheet";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { voiceProvidersOptions } from "@/data/voice-providers";
import { memberListOptions } from "@/data/queries/members";
import { workspaceListOptions } from "@/data/queries/workspaces";
import {
  useCreateVoiceProvider,
  useDeleteVoiceProvider,
  useDeleteVoiceProviderCredential,
  useProbeVoiceProviderCredential,
  usePutVoiceProviderCredential,
  useUpdateVoiceProvider,
  useUpdateWorkspaceSettings,
} from "@/data/mutations/voice-providers";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import {
  parseVoiceAdvancedDraft,
  parseWorkspaceVoiceSettings,
  voiceProviderChainEligible,
} from "@multica/core/voice/chain";
import type { VoiceProviderRef } from "@multica/core/types";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

const CREDENTIAL_KEY = "api_key";
/** Radio sentinel for "not configured" in the chain pickers. */
const NO_PROVIDER = "none";
const DEFAULT_VOICE_MODEL = "gemini-3.8-live";
const DEFAULT_ASR_MODEL = "gemini-2.5-flash";

type ChainLevel = "default" | "asr";

interface ChainDraft {
  providerId: string;
  model: string;
  advanced: string;
}

const EMPTY_DRAFT: ChainDraft = { providerId: NO_PROVIDER, model: "", advanced: "" };

function draftToRef(draft: ChainDraft): VoiceProviderRef | null {
  if (draft.providerId === NO_PROVIDER) return null;
  const advanced = parseVoiceAdvancedDraft(draft.advanced);
  return {
    provider_id: draft.providerId,
    ...(draft.model.trim() ? { model: draft.model.trim() } : {}),
    ...(advanced && advanced !== "invalid" ? { advanced } : {}),
  };
}

function refKey(ref: VoiceProviderRef | null): string {
  if (!ref) return NO_PROVIDER;
  return [ref.provider_id, ref.model ?? "", JSON.stringify(ref.advanced ?? null)].join("\0");
}

function draftKey(draft: ChainDraft): string {
  return refKey(draftToRef(draft)) + "\0" + draft.advanced;
}

export default function VoiceSettingsScreen() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const me = useAuthStore((s) => s.user);
  const { t } = useT("settings");
  const actionSheet = useActionSheet();
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];

  const { data: providers = [], isLoading: providersLoading } = useQuery(
    voiceProvidersOptions(wsId ?? ""),
  );
  const { data: members } = useQuery(memberListOptions(wsId));
  const { data: workspace } = useQuery({
    ...workspaceListOptions(),
    select: (list: Array<{ slug: string; settings?: Record<string, unknown> }>) =>
      list.find((w) => w.slug === wsSlug) ?? null,
  });

  const isAdmin = useMemo(() => {
    if (!me) return false;
    const mine = members?.find((m) => m.user_id === me.id);
    return mine?.role === "owner" || mine?.role === "admin";
  }, [members, me]);

  const createProvider = useCreateVoiceProvider();
  const updateProvider = useUpdateVoiceProvider();
  const deleteProvider = useDeleteVoiceProvider();
  const putCredential = usePutVoiceProviderCredential();
  const deleteCredential = useDeleteVoiceProviderCredential();
  const reprobe = useProbeVoiceProviderCredential();
  const updateSettings = useUpdateWorkspaceSettings();

  const voiceSettings = useMemo(
    () => parseWorkspaceVoiceSettings(workspace?.settings ?? null),
    [workspace],
  );

  // Chain drafts seed once from the workspace settings bag; after that the
  // form is the source of truth until saved (invalidation re-seeds are not
  // wanted mid-edit — same re-seed discipline as web's savedKey).
  const [seeded, setSeeded] = useState(false);
  const [defaultDraft, setDefaultDraft] = useState<ChainDraft>(EMPTY_DRAFT);
  const [asrDraft, setAsrDraft] = useState<ChainDraft>(EMPTY_DRAFT);

  useEffect(() => {
    if (!workspace || seeded) return;
    const toDraft = (ref: VoiceProviderRef | null): ChainDraft =>
      ref
        ? {
            providerId: ref.provider_id,
            model: ref.model ?? "",
            advanced: ref.advanced ? JSON.stringify(ref.advanced, null, 2) : "",
          }
        : { ...EMPTY_DRAFT };
    setDefaultDraft(toDraft(voiceSettings.default));
    setAsrDraft(toDraft(voiceSettings.asr));
    setSeeded(true);
  }, [workspace, seeded, voiceSettings]);

  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState("");
  const [createType, setCreateType] = useState("gemini");
  const [credentialTarget, setCredentialTarget] = useState<VoiceProviderRow | null>(null);
  const [credentialValue, setCredentialValue] = useState("");
  const [renameTarget, setRenameTarget] = useState<VoiceProviderRow | null>(null);
  const [renameValue, setRenameValue] = useState("");

  const eligible = useMemo(() => providers.filter(voiceProviderChainEligible), [providers]);

  const chainBusy =
    updateSettings.isPending || createProvider.isPending || updateProvider.isPending;

  const saveChain = (level: ChainLevel, draft: ChainDraft) => {
    if (!workspace || !isAdmin) return;
    if (parseVoiceAdvancedDraft(draft.advanced) === "invalid") return;
    const voice = { ...voiceSettings, [level]: draftToRef(draft) };
    updateSettings.mutate(
      { ...(workspace.settings ?? {}), voice },
      {
        onError: (err) =>
          Alert.alert(
            t("voice.save_failed_toast", "Failed to save"),
            err instanceof Error ? err.message : undefined,
          ),
      },
    );
  };

  const openRowActions = (p: VoiceProviderRow) => {
    actionSheet.show({
      title: p.display_name,
      options: [
        t("voice.action_set_credential", "Set API key…"),
        t("voice.action_reprobe", "Test connectivity"),
        t("voice.action_rename", "Rename…"),
        t("voice.action_delete", "Delete…"),
        t("voice.cancel", "Cancel"),
      ],
      cancelButtonIndex: 4,
      destructiveButtonIndex: 3,
      onSelect: (index) => {
        if (index === 0) {
          setCredentialValue("");
          setCredentialTarget(p);
        } else if (index === 1) {
          reprobe.mutate(p.id, {
            onError: (err) =>
              Alert.alert(
                t("voice.probe_failed_toast", "Connectivity test failed to run"),
                err instanceof Error ? err.message : undefined,
              ),
          });
        } else if (index === 2) {
          setRenameValue(p.display_name);
          setRenameTarget(p);
        } else if (index === 3) {
          Alert.alert(
            t("voice.delete_dialog_title", "Delete voice provider"),
            t(
              "voice.delete_dialog_body",
              "Agents overriding this provider fall back to the workspace default, and the workspace default / dictation references to it are cleared.",
            ),
            [
              { text: t("voice.cancel", "Cancel"), style: "cancel" },
              {
                text: t("voice.delete", "Delete"),
                style: "destructive",
                onPress: () =>
                  deleteProvider.mutate(p.id, {
                    onError: (err) =>
                      Alert.alert(
                        t("voice.delete_failed_toast", "Failed to delete provider"),
                        err instanceof Error ? err.message : undefined,
                      ),
                  }),
              },
            ],
          );
        }
      },
    });
  };

  const submitCreate = () => {
    const name = createName.trim();
    if (!name || createProvider.isPending) return;
    createProvider.mutate(
      { display_name: name, provider_type: createType },
      {
        onSuccess: () => {
          setCreateOpen(false);
          setCreateName("");
          setCreateType("gemini");
        },
        onError: (err) =>
          Alert.alert(
            t("voice.save_failed_toast", "Failed to save"),
            err instanceof Error ? err.message : undefined,
          ),
      },
    );
  };

  if (providersLoading) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <ActivityIndicator />
      </View>
    );
  }

  return (
    <KeyboardAvoidingView className="flex-1 bg-background" behavior="padding">
      <SheetHeader className="flex-row items-center px-4 pb-2 border-b border-border" gap={12}>
        <Text className="flex-1 text-lg font-semibold text-foreground">
          {t("voice.title", "Voice")}
        </Text>
      </SheetHeader>

      <ScrollView
        nestedScrollEnabled
        className="flex-1"
        contentContainerClassName="px-4 pt-4 pb-8 gap-4"
        keyboardShouldPersistTaps="handled"
      >
        {!isAdmin ? (
          <Text className="text-xs text-muted-foreground px-1">
            {t("voice.admin_only_hint", "Viewing only — workspace owners and admins manage voice settings.")}
          </Text>
        ) : null}

        <Section
          title={t("voice.pool_section_title", "Voice providers")}
          description={t(
            "voice.pool_section_hint",
            "The workspace pool of voice suppliers. The API key is stored server-side and never leaves the server — saving runs a connectivity probe.",
          )}
        >
          {providers.map((p, idx) => (
            <View key={p.id}>
              <ProviderRow
                p={p}
                theme={theme}
                canManage={isAdmin}
                busy={updateProvider.isPending || reprobe.isPending}
                onToggle={(enabled) =>
                  updateProvider.mutate(
                    { providerId: p.id, patch: { enabled } },
                    {
                      onError: (err) =>
                        Alert.alert(
                          t("voice.save_failed_toast", "Failed to save"),
                          err instanceof Error ? err.message : undefined,
                        ),
                    },
                  )
                }
                onPress={isAdmin ? () => openRowActions(p) : undefined}
              />
              {idx < providers.length - 1 ? <Separator /> : null}
            </View>
          ))}
          {providers.length === 0 ? (
            <Text className="px-4 py-3 text-sm text-muted-foreground">
              {t("voice.no_eligible_providers", "No eligible provider yet — add a Gemini provider with a working API key first.")}
            </Text>
          ) : null}
          {isAdmin ? (
            <>
              {providers.length > 0 ? <Separator /> : null}
              <Pressable
                onPress={() => {
                  setCreateName("");
                  setCreateType("gemini");
                  setCreateOpen(true);
                }}
                className="px-4 py-3 active:bg-secondary"
                accessibilityRole="button"
                accessibilityLabel={t("voice.add_provider", "Add provider")}
              >
                <Text className="text-base text-brand font-medium">
                  {t("voice.add_provider", "Add provider")}
                </Text>
              </Pressable>
            </>
          ) : null}
        </Section>

        <ChainSection
          title={t("voice.default_section_title", "Workspace default voice")}
          description={t(
            "voice.default_section_hint",
            "Used by every agent without its own voice override. Only Gemini providers can serve realtime voice today.",
          )}
          draft={defaultDraft}
          onDraft={setDefaultDraft}
          saved={voiceSettings.default}
          eligible={eligible}
          canManage={isAdmin}
          busy={chainBusy}
          modelPlaceholder={t("voice.model_placeholder_default", {
            defaultValue: `Default: ${DEFAULT_VOICE_MODEL}`,
            model: DEFAULT_VOICE_MODEL,
          })}
          onSave={() => saveChain("default", defaultDraft)}
        />

        <ChainSection
          title={t("voice.asr_section_title", "Voice dictation (ASR)")}
          description={t(
            "voice.asr_section_hint",
            "Workspace-wide dictation transcription. Once configured, dictation is available to the whole workspace. Only Gemini providers can serve ASR today.",
          )}
          draft={asrDraft}
          onDraft={setAsrDraft}
          saved={voiceSettings.asr}
          eligible={eligible}
          canManage={isAdmin}
          busy={chainBusy}
          modelPlaceholder={t("voice.model_placeholder_default", {
            defaultValue: `Default: ${DEFAULT_ASR_MODEL}`,
            model: DEFAULT_ASR_MODEL,
          })}
          onSave={() => saveChain("asr", asrDraft)}
        />
      </ScrollView>

      {/* Create dialog */}
      <Modal
        visible={createOpen}
        transparent
        animationType="fade"
        onRequestClose={() => setCreateOpen(false)}
      >
        <DialogShell onClose={() => setCreateOpen(false)}>
          <Text className="text-base font-semibold text-foreground">
            {t("voice.create_dialog_title", "Add voice provider")}
          </Text>
          <Field label={t("voice.provider_name_label", "Display name")}>
            <TextField
              value={createName}
              onChangeText={setCreateName}
              placeholder={t("voice.provider_name_placeholder", "e.g. Gemini (workspace)")}
              autoFocus
            />
          </Field>
          <Field label={t("voice.provider_type_label", "Type")}>
            <View className="rounded-md border border-border overflow-hidden">
              {(["gemini", "chatgpt"] as const).map((type, idx) => (
                <Pressable
                  key={type}
                  onPress={() => setCreateType(type)}
                  className={`flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary ${
                    idx > 0 ? "border-t border-border" : ""
                  }`}
                  accessibilityRole="radio"
                  accessibilityState={{ selected: createType === type }}
                >
                  <RadioDot selected={createType === type} />
                  <Text className="flex-1 text-sm text-foreground">
                    {t(`voice.provider_type_${type}`, type)}
                  </Text>
                </Pressable>
              ))}
            </View>
          </Field>
          <Text className="text-xs text-muted-foreground">
            {t(
              "voice.gemini_only_hint",
              "Realtime voice and dictation currently speak the Gemini protocol; other provider types can be configured in the pool but fall back to the official form.",
            )}
          </Text>
          <View className="flex-row justify-end gap-2 pt-1">
            <DialogButton
              label={t("voice.cancel", "Cancel")}
              onPress={() => setCreateOpen(false)}
            />
            <DialogButton
              label={t("voice.create", "Create")}
              primary
              disabled={!createName.trim() || createProvider.isPending}
              onPress={submitCreate}
            />
          </View>
        </DialogShell>
      </Modal>

      {/* Credential dialog — the key travels once in the PUT body and is
          never stored in client state beyond this field. */}
      <Modal
        visible={credentialTarget !== null}
        transparent
        animationType="fade"
        onRequestClose={() => setCredentialTarget(null)}
      >
        <DialogShell onClose={() => setCredentialTarget(null)}>
          <Text className="text-base font-semibold text-foreground">
            {t("voice.credential_dialog_title", "API key")}
          </Text>
          <Field label={t("voice.credential_input_label", "API key")}>
            <TextField
              value={credentialValue}
              onChangeText={setCredentialValue}
              secureTextEntry
              autoCapitalize="none"
              autoCorrect={false}
              autoFocus
            />
          </Field>
          <View className="flex-row justify-end gap-2 pt-1">
            {credentialTarget?.credential_status === "configured" ? (
              <DialogButton
                label={t("voice.credential_remove", "Remove stored key")}
                destructive
                disabled={deleteCredential.isPending || putCredential.isPending}
                onPress={() => {
                  if (!credentialTarget) return;
                  deleteCredential.mutate(
                    { providerId: credentialTarget.id, credentialKey: CREDENTIAL_KEY },
                    {
                      onSuccess: () => setCredentialTarget(null),
                      onError: (err) =>
                        Alert.alert(
                          t("voice.save_failed_toast", "Failed to save"),
                          err instanceof Error ? err.message : undefined,
                        ),
                    },
                  );
                }}
              />
            ) : null}
            <DialogButton
              label={t("voice.credential_save", "Save & test")}
              primary
              disabled={!credentialValue.trim() || putCredential.isPending}
              onPress={() => {
                if (!credentialTarget) return;
                putCredential.mutate(
                  {
                    providerId: credentialTarget.id,
                    credentialKey: CREDENTIAL_KEY,
                    value: credentialValue.trim(),
                  },
                  {
                    onSuccess: () => {
                      setCredentialTarget(null);
                      setCredentialValue("");
                    },
                    onError: (err) =>
                      Alert.alert(
                        t("voice.save_failed_toast", "Failed to save"),
                        err instanceof Error ? err.message : undefined,
                      ),
                  },
                );
              }}
            />
          </View>
        </DialogShell>
      </Modal>

      {/* Rename dialog */}
      <Modal
        visible={renameTarget !== null}
        transparent
        animationType="fade"
        onRequestClose={() => setRenameTarget(null)}
      >
        <DialogShell onClose={() => setRenameTarget(null)}>
          <Text className="text-base font-semibold text-foreground">
            {t("voice.rename_dialog_title", "Rename provider")}
          </Text>
          <Field label={t("voice.provider_name_label", "Display name")}>
            <TextField
              value={renameValue}
              onChangeText={setRenameValue}
              autoFocus
              onSubmitEditing={() => {
                if (!renameTarget || !renameValue.trim() || updateProvider.isPending) return;
                updateProvider.mutate(
                  { providerId: renameTarget.id, patch: { display_name: renameValue.trim() } },
                  {
                    onSuccess: () => setRenameTarget(null),
                    onError: (err) =>
                      Alert.alert(
                        t("voice.save_failed_toast", "Failed to save"),
                        err instanceof Error ? err.message : undefined,
                      ),
                  },
                );
              }}
            />
          </Field>
          <View className="flex-row justify-end gap-2 pt-1">
            <DialogButton
              label={t("voice.cancel", "Cancel")}
              onPress={() => setRenameTarget(null)}
            />
            <DialogButton
              label={t("voice.save", "Save")}
              primary
              disabled={!renameValue.trim() || updateProvider.isPending}
              onPress={() => {
                if (!renameTarget) return;
                updateProvider.mutate(
                  { providerId: renameTarget.id, patch: { display_name: renameValue.trim() } },
                  {
                    onSuccess: () => setRenameTarget(null),
                    onError: (err) =>
                      Alert.alert(
                        t("voice.save_failed_toast", "Failed to save"),
                        err instanceof Error ? err.message : undefined,
                      ),
                  },
                );
              }}
            />
          </View>
        </DialogShell>
      </Modal>

      {/* Row action sheet — the hook only holds state; without this the
          Android bottom sheet never renders (iOS uses ActionSheetIOS). */}
      <ActionSheetModal {...actionSheet.modalProps} />
    </KeyboardAvoidingView>
  );
}

type VoiceProviderRow = {
  id: string;
  display_name: string;
  provider_type: string;
  enabled: boolean;
  credential_status: string;
  credential_probe?: { status: string; checked_at: string } | null;
};

const STATUS_COLOR: Record<string, keyof typeof THEME.light> = {
  configured: "foreground",
  missing: "destructive",
  not_configured: "mutedForeground",
  ok: "foreground",
  invalid: "destructive",
  unreachable: "destructive",
  skipped: "mutedForeground",
};

function ProviderRow({
  p,
  theme,
  canManage,
  busy,
  onToggle,
  onPress,
}: {
  p: VoiceProviderRow;
  theme: (typeof THEME)["light"];
  canManage: boolean;
  busy: boolean;
  onToggle: (enabled: boolean) => void;
  onPress?: () => void;
}) {
  const { t } = useT("settings");
  const probeTime = p.credential_probe?.checked_at
    ? new Intl.DateTimeFormat(undefined, {
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      }).format(new Date(p.credential_probe.checked_at))
    : null;

  return (
    <Pressable
      onPress={onPress}
      disabled={!onPress}
      className="flex-row items-center px-4 py-3 gap-3"
      accessibilityRole={onPress ? "button" : undefined}
    >
      <View className="flex-1 gap-0.5">
        <Text className="text-base font-medium text-foreground" numberOfLines={1}>
          {p.display_name}
        </Text>
        <View className="flex-row flex-wrap items-center gap-x-2">
          <Text className="text-xs text-muted-foreground">
            {t(`voice.provider_type_${p.provider_type === "chatgpt" ? "chatgpt" : "gemini"}`, p.provider_type)}
          </Text>
          <Text
            className="text-xs"
            style={{ color: theme[STATUS_COLOR[p.credential_status] ?? "mutedForeground"] }}
          >
            {t(`voice.status_${p.credential_status}`, p.credential_status)}
          </Text>
          {p.credential_probe ? (
            <Text
              className="text-xs"
              style={{ color: theme[STATUS_COLOR[p.credential_probe.status] ?? "mutedForeground"] }}
            >
              {t(`voice.probe_${p.credential_probe.status}`, p.credential_probe.status)}
              {probeTime
                ? ` · ${t("voice.probe_checked_at", { defaultValue: "Checked {{time}}", time: probeTime })}`
                : ""}
            </Text>
          ) : null}
        </View>
      </View>
      <Switch
        checked={p.enabled}
        disabled={!canManage || busy}
        onCheckedChange={onToggle}
      />
    </Pressable>
  );
}

function ChainSection({
  title,
  description,
  draft,
  onDraft,
  saved,
  eligible,
  canManage,
  busy,
  modelPlaceholder,
  onSave,
}: {
  title: string;
  description: string;
  draft: ChainDraft;
  onDraft: (next: ChainDraft) => void;
  saved: VoiceProviderRef | null;
  eligible: VoiceProviderRow[];
  canManage: boolean;
  busy: boolean;
  modelPlaceholder: string;
  onSave: () => void;
}) {
  const { t } = useT("settings");
  const advancedState = parseVoiceAdvancedDraft(draft.advanced);
  const advancedInvalid = advancedState === "invalid";
  const dirty = draftKey(draft) !== refKey(saved);
  const canSave = canManage && dirty && !advancedInvalid && !busy;

  return (
    <Section title={title} description={description}>
      <View className="gap-4 p-4">
        <Field label={t("voice.provider_picker_label", "Provider")}>
          <View className="rounded-md border border-border overflow-hidden">
            <Pressable
              onPress={() => canManage && onDraft({ ...draft, providerId: NO_PROVIDER })}
              className="flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary"
              accessibilityRole="radio"
              accessibilityState={{ selected: draft.providerId === NO_PROVIDER }}
            >
              <RadioDot selected={draft.providerId === NO_PROVIDER} />
              <Text className="flex-1 text-sm text-foreground">
                {t("voice.provider_picker_placeholder", "Not configured")}
              </Text>
            </Pressable>
            {eligible.map((p) => (
              <Pressable
                key={p.id}
                onPress={() => canManage && onDraft({ ...draft, providerId: p.id })}
                className="flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary border-t border-border"
                accessibilityRole="radio"
                accessibilityState={{ selected: draft.providerId === p.id }}
              >
                <RadioDot selected={draft.providerId === p.id} />
                <Text className="flex-1 text-sm text-foreground" numberOfLines={1}>
                  {p.display_name}
                </Text>
              </Pressable>
            ))}
          </View>
          {eligible.length === 0 ? (
            <Text className="text-xs text-muted-foreground mt-1">
              {t("voice.no_eligible_providers", "No eligible provider yet — add a Gemini provider with a working API key first.")}
            </Text>
          ) : null}
        </Field>

        <Field label={t("voice.model_label", "Model")}>
          <TextInput
            value={draft.model}
            onChangeText={(model) => onDraft({ ...draft, model })}
            placeholder={modelPlaceholder}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            autoCapitalize="none"
            autoCorrect={false}
            editable={canManage && draft.providerId !== NO_PROVIDER}
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2"
          />
        </Field>

        <Field label={t("voice.advanced_label", "Advanced parameters (JSON)")}>
          <TextInput
            value={draft.advanced}
            onChangeText={(advanced) => onDraft({ ...draft, advanced })}
            placeholder={t("voice.advanced_placeholder", 'e.g. {"temperature": 0.5}')}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            multiline
            textAlignVertical="top"
            autoCapitalize="none"
            autoCorrect={false}
            editable={canManage && draft.providerId !== NO_PROVIDER}
            className={`text-base text-foreground bg-secondary/50 rounded-md px-3 py-2 min-h-20 ${
              advancedInvalid ? "border border-destructive" : ""
            }`}
          />
          {advancedInvalid ? (
            <Text className="text-xs text-destructive mt-1">
              {t("voice.advanced_invalid", "Advanced parameters must be a valid JSON object")}
            </Text>
          ) : null}
        </Field>

        <Pressable
          onPress={onSave}
          disabled={!canSave}
          className={`self-start px-4 py-2 rounded-md bg-primary ${canSave ? "active:bg-secondary" : "opacity-40"}`}
          accessibilityRole="button"
          accessibilityLabel={t("voice.save", "Save")}
        >
          <Text className="text-sm font-medium text-primary-foreground">
            {t("voice.save", "Save")}
          </Text>
        </Pressable>
      </View>
    </Section>
  );
}

function Section({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <View className="gap-2">
      <View className="px-1">
        <Text className="text-xs uppercase tracking-wider text-muted-foreground">
          {title}
        </Text>
        {description ? (
          <Text className="text-xs text-muted-foreground mt-1">{description}</Text>
        ) : null}
      </View>
      <View className="rounded-md border border-border bg-card overflow-hidden">
        {children}
      </View>
    </View>
  );
}

function DialogShell({
  children,
  onClose,
}: {
  children: React.ReactNode;
  onClose: () => void;
}) {
  return (
    <Pressable className="flex-1 bg-black/40" onPress={onClose}>
      <KeyboardAvoidingView
        className="flex-1 items-center justify-center px-6"
        behavior="padding"
        pointerEvents="box-none"
      >
        <Pressable onPress={() => {}} className="w-full max-w-sm">
          <View className="bg-popover rounded-2xl overflow-hidden px-4 py-4 gap-3">
            {children}
          </View>
        </Pressable>
      </KeyboardAvoidingView>
    </Pressable>
  );
}

function DialogButton({
  label,
  onPress,
  primary,
  destructive,
  disabled,
}: {
  label: string;
  onPress: () => void;
  primary?: boolean;
  destructive?: boolean;
  disabled?: boolean;
}) {
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      className={`px-3 py-2 rounded-md ${primary ? "bg-primary" : ""} ${
        disabled ? "opacity-40" : "active:bg-secondary"
      }`}
      accessibilityRole="button"
      accessibilityLabel={label}
    >
      <Text
        className={`text-sm font-medium ${
          destructive ? "text-destructive" : primary ? "text-primary-foreground" : "text-foreground"
        }`}
      >
        {label}
      </Text>
    </Pressable>
  );
}
