"use client";

import { useMemo, useState } from "react";
import { Info, MoreHorizontal, Pencil, Plus, Trash2, Volume2, KeyRound } from "lucide-react";
import { Input } from "@multica/ui/components/ui/input";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import { Badge } from "@multica/ui/components/ui/badge";
import { Textarea } from "@multica/ui/components/ui/textarea";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@multica/ui/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@multica/ui/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@multica/ui/components/ui/dropdown-menu";
import { Alert, AlertDescription } from "@multica/ui/components/ui/alert";
import { toast } from "sonner";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useCurrentWorkspace } from "@multica/core/paths";
import { memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import {
  parseVoiceAdvancedDraft,
  parseWorkspaceVoiceSettings,
  voiceProviderChainEligible,
  voiceProviderListOptions,
} from "@multica/core/voice/providers";
import {
  useCreateVoiceProvider,
  useDeleteVoiceProvider,
  useDeleteVoiceProviderCredential,
  useProbeVoiceProviderCredential,
  usePutVoiceProviderCredential,
  useUpdateVoiceProvider,
} from "@multica/core/voice/mutations";
import type { VoiceProvider } from "@multica/core/types";
import { useLocale, useT } from "../../i18n";
import {
  SettingsCard,
  SettingsRow,
  SettingsSection,
  SettingsTab,
} from "./settings-layout";

/** Server default when a chain level stores no explicit model. */
const DEFAULT_VOICE_MODEL = "gemini-3.8-live";
const DEFAULT_ASR_MODEL = "gemini-2.5-flash";
/** The credential key both provider kinds store under — one credential per
 *  provider row, so the key name is fixed by convention (server contract). */
const CREDENTIAL_KEY = "api_key";

const PROVIDER_TYPE_KEYS = {
  gemini: "provider_type_gemini",
  chatgpt: "provider_type_chatgpt",
} as const;

function providerTypeKey(type: string) {
  return type in PROVIDER_TYPE_KEYS
    ? PROVIDER_TYPE_KEYS[type as keyof typeof PROVIDER_TYPE_KEYS]
    : null;
}

interface ChainDraft {
  providerId: string;
  model: string;
  advanced: string;
}

function draftFor(
  ref: { provider_id: string; model?: string; advanced?: Record<string, unknown> } | null,
): ChainDraft {
  return {
    providerId: ref?.provider_id ?? "none",
    model: ref?.model ?? "",
    advanced: ref?.advanced ? JSON.stringify(ref.advanced, null, 2) : "",
  };
}

function parseAdvanced(raw: string): Record<string, unknown> | null | "invalid" {
  return parseVoiceAdvancedDraft(raw);
}



/** One chain-level configuration card (workspace default / ASR): provider
 *  picker + optional model + optional advanced JSON. Saves through
 *  `onSave(voice)` with the whole merged voice bag — the workspace settings
 *  PATCH replaces the stored blob, so callers merge, never patch. */
function ChainConfigCard({
  ref_,
  eligible,
  savePending,
  defaultModel,
  onSave,
  canManage,
}: {
  ref_: { provider_id: string; model?: string; advanced?: Record<string, unknown> } | null;
  eligible: VoiceProvider[];
  savePending: boolean;
  defaultModel: string;
  onSave: (ref: { provider_id: string; model?: string; advanced?: Record<string, unknown> } | null) => void;
  canManage: boolean;
}) {
  const { t } = useT("settings");
  const [draft, setDraft] = useState<ChainDraft>(() => draftFor(ref_));
  const [advancedInvalid, setAdvancedInvalid] = useState(false);

  // Re-seed when the saved ref changes underneath (post-save refetch, or a
  // provider delete that nulled the level server-side).
  const savedKey = ref_ ? `${ref_.provider_id}\u0000${ref_.model ?? ""}\u0000${JSON.stringify(ref_.advanced ?? null)}` : "none";
  const [seenSavedKey, setSeenSavedKey] = useState(savedKey);
  if (savedKey !== seenSavedKey) {
    setSeenSavedKey(savedKey);
    setDraft(draftFor(ref_));
    setAdvancedInvalid(false);
  }

  // The picker only offers eligible rows; a saved selection that no longer
  // qualifies (key removed, provider disabled) drops out of the list while
  // the draft keeps its id — the server's chain gate reads live provider
  // state, not this draft.
  const options = eligible;

  const handleSave = () => {
    if (draft.providerId === "none") {
      onSave(null);
      return;
    }
    const advanced = parseAdvanced(draft.advanced);
    if (advanced === "invalid") {
      setAdvancedInvalid(true);
      return;
    }
    setAdvancedInvalid(false);
    onSave({
      provider_id: draft.providerId,
      ...(draft.model.trim() ? { model: draft.model.trim() } : {}),
      ...(advanced ? { advanced } : {}),
    });
  };

  const dirty =
    draft.providerId !== (ref_?.provider_id ?? "none") ||
    draft.model.trim() !== (ref_?.model ?? "") ||
    (parseAdvanced(draft.advanced) === "invalid"
      ? true
      : JSON.stringify(parseAdvanced(draft.advanced) ?? null) !==
        JSON.stringify(ref_?.advanced ?? null));

  return (
    <SettingsCard>
      <SettingsRow label={t(($) => $.voice.provider_picker_label)} size="select-wide">
        <Select
          items={[
            { value: "none", label: t(($) => $.voice.provider_picker_placeholder) },
            ...options.map((p) => ({ value: p.id, label: p.display_name })),
          ]}
          value={draft.providerId}
          onValueChange={(value) => {
            if (value != null) setDraft((d) => ({ ...d, providerId: value }));
          }}
          disabled={!canManage}
        >
          <SelectTrigger aria-label={t(($) => $.voice.provider_picker_label)}>
            <SelectValue placeholder={t(($) => $.voice.provider_picker_placeholder)} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="none">
              {t(($) => $.voice.provider_picker_placeholder)}
            </SelectItem>
            {options.map((p) => (
              <SelectItem key={p.id} value={p.id}>
                {p.display_name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </SettingsRow>
      <SettingsRow label={t(($) => $.voice.model_label)} size="text">
        <Input
          type="text"
          value={draft.model}
          onChange={(e) => setDraft((d) => ({ ...d, model: e.target.value }))}
          disabled={!canManage || draft.providerId === "none"}
          placeholder={t(($) => $.voice.model_placeholder_default, { model: defaultModel })}
        />
      </SettingsRow>
      <SettingsRow
        label={t(($) => $.voice.advanced_label)}
        size="text"
        align="start"
      >
        <div>
          <Textarea
            value={draft.advanced}
            onChange={(e) => setDraft((d) => ({ ...d, advanced: e.target.value }))}
            disabled={!canManage || draft.providerId === "none"}
            rows={3}
            placeholder={t(($) => $.voice.advanced_placeholder)}
            aria-invalid={advancedInvalid || undefined}
            className="resize-y font-mono text-caption"
          />
          {advancedInvalid ? (
            <p className="mt-1 text-caption text-destructive">
              {t(($) => $.voice.advanced_invalid)}
            </p>
          ) : null}
        </div>
      </SettingsRow>
      <SettingsRow label="" size="none">
        <Button size="sm" onClick={handleSave} disabled={!canManage || !dirty || savePending}>
          {t(($) => $.voice.save)}
        </Button>
      </SettingsRow>
    </SettingsCard>
  );
}

export function VoiceTab() {
  const { t } = useT("settings");
  const locale = useLocale();
  const workspace = useCurrentWorkspace();
  const wsId = workspace?.id;
  const user = useAuthStore((s) => s.user);
  const queryClient = useQueryClient();

  const { data: members = [] } = useQuery({
    ...memberListOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const canManage = useMemo(() => {
    const currentMember = members.find((m) => m.user_id === user?.id) ?? null;
    return currentMember?.role === "owner" || currentMember?.role === "admin";
  }, [members, user?.id]);

  const { data: providers = [], isLoading: providersLoading } = useQuery({
    ...voiceProviderListOptions(wsId ?? ""),
    enabled: !!wsId,
  });

  const createProvider = useCreateVoiceProvider();
  const updateProvider = useUpdateVoiceProvider();
  const deleteProvider = useDeleteVoiceProvider();
  const putCredential = usePutVoiceProviderCredential();
  const deleteCredential = useDeleteVoiceProviderCredential();
  const probeCredential = useProbeVoiceProviderCredential();
  const [savingChain, setSavingChain] = useState<"default" | "asr" | null>(null);

  const settings = parseWorkspaceVoiceSettings(workspace?.settings);

  const saveChain = async (
    level: "default" | "asr",
    ref: { provider_id: string; model?: string; advanced?: Record<string, unknown> } | null,
  ) => {
    if (!wsId || !workspace) return;
    setSavingChain(level);
    try {
      await api.updateWorkspace(wsId, {
        settings: {
          ...workspace.settings,
          voice: { ...settings, [level]: ref },
        },
      });
      await queryClient.invalidateQueries({ queryKey: workspaceKeys.list() });
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t(($) => $.voice.save_failed_toast),
      );
    } finally {
      setSavingChain(null);
    }
  };

  // --- dialog state -------------------------------------------------------
  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState("");
  const [createType, setCreateType] = useState<"gemini" | "chatgpt">("gemini");
  const [credentialFor, setCredentialFor] = useState<VoiceProvider | null>(null);
  const [credentialValue, setCredentialValue] = useState("");
  const [renameFor, setRenameFor] = useState<VoiceProvider | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [deleteFor, setDeleteFor] = useState<VoiceProvider | null>(null);

  const openCredential = (p: VoiceProvider) => {
    setCredentialValue("");
    setCredentialFor(p);
  };
  const submitCredential = async () => {
    if (!credentialFor || !credentialValue.trim() || !wsId) return;
    try {
      await putCredential.mutateAsync({
        providerId: credentialFor.id,
        credentialKey: CREDENTIAL_KEY,
        value: credentialValue,
      });
      setCredentialFor(null);
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t(($) => $.voice.save_failed_toast),
      );
    }
  };
  const removeCredential = async (p: VoiceProvider) => {
    if (!wsId) return;
    try {
      await deleteCredential.mutateAsync({
        providerId: p.id,
        credentialKey: CREDENTIAL_KEY,
      });
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t(($) => $.voice.save_failed_toast),
      );
    }
  };
  const submitCreate = async () => {
    const name = createName.trim();
    if (!name) return;
    try {
      await createProvider.mutateAsync({ display_name: name, provider_type: createType });
      setCreateOpen(false);
      setCreateName("");
      setCreateType("gemini");
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t(($) => $.voice.save_failed_toast),
      );
    }
  };
  const submitRename = async () => {
    if (!renameFor) return;
    const name = renameValue.trim();
    if (!name) return;
    try {
      await updateProvider.mutateAsync({
        providerId: renameFor.id,
        data: { display_name: name },
      });
      setRenameFor(null);
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t(($) => $.voice.save_failed_toast),
      );
    }
  };
  const submitDelete = async () => {
    if (!deleteFor) return;
    try {
      await deleteProvider.mutateAsync(deleteFor.id);
      setDeleteFor(null);
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t(($) => $.voice.delete_failed_toast),
      );
    }
  };
  const reprobe = async (p: VoiceProvider) => {
    try {
      await probeCredential.mutateAsync(p.id);
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t(($) => $.voice.probe_failed_toast),
      );
    }
  };

  const credentialStatusBadge = (p: VoiceProvider) => {
    const labelKey =
      p.credential_status === "configured"
        ? "status_configured"
        : p.credential_status === "missing"
          ? "status_missing"
          : "status_not_configured";
    return (
      <Badge
        variant={p.credential_status === "configured" ? "secondary" : "outline"}
        className="shrink-0"
      >
        {t(($) => $.voice[labelKey])}
      </Badge>
    );
  };

  const probeBadge = (p: VoiceProvider) => {
    const probe = p.credential_probe;
    if (!probe) return null;
    const labelKey =
      probe.status === "ok"
        ? "probe_ok"
        : probe.status === "invalid"
          ? "probe_invalid"
          : probe.status === "unreachable"
            ? "probe_unreachable"
            : "probe_skipped";
    return (
      <Badge
        variant={probe.status === "ok" ? "secondary" : probe.status === "skipped" ? "outline" : "destructive"}
        className="shrink-0"
      >
        {t(($) => $.voice[labelKey])}
      </Badge>
    );
  };

  const eligible = providers.filter(voiceProviderChainEligible);

  return (
    <SettingsTab
      title={t(($) => $.voice.title)}
      description={t(($) => $.voice.description)}
    >
      {!canManage ? (
        <Alert>
          <Info className="h-4 w-4" />
          <AlertDescription>{t(($) => $.voice.admin_only_hint)}</AlertDescription>
        </Alert>
      ) : null}

      <SettingsSection
        title={t(($) => $.voice.pool_section_title)}
        description={t(($) => $.voice.pool_section_hint)}
        action={
          canManage ? (
            <Button size="sm" variant="outline" onClick={() => setCreateOpen(true)}>
              <Plus className="h-4 w-4" />
              {t(($) => $.voice.add_provider)}
            </Button>
          ) : null
        }
      >
        <SettingsCard>
          {providersLoading ? null : providers.length === 0 ? (
            <SettingsRow label={t(($) => $.voice.no_eligible_providers)} size="none">
              {null}
            </SettingsRow>
          ) : (
            providers.map((p) => {
              const typeKey = providerTypeKey(p.provider_type);
              return (
                <SettingsRow
                  key={p.id}
                  label={
                    <span className="flex min-w-0 flex-wrap items-center gap-2">
                      <span className="truncate font-medium">{p.display_name}</span>
                      {typeKey ? (
                        <Badge variant="outline" className="shrink-0">
                          {t(($) => $.voice[typeKey])}
                        </Badge>
                      ) : null}
                      {credentialStatusBadge(p)}
                      {probeBadge(p)}
                      {p.credential_probe?.checked_at ? (
                        <span className="text-caption text-muted-foreground">
                          {t(($) => $.voice.probe_checked_at, {
                            time: new Intl.DateTimeFormat(locale, {
                              dateStyle: "short",
                              timeStyle: "short",
                            }).format(new Date(p.credential_probe.checked_at)),
                          })}
                        </span>
                      ) : null}
                    </span>
                  }
                  description={
                    p.credential_status === "missing"
                      ? t(($) => $.voice.gemini_only_hint)
                      : undefined
                  }
                  size="none"
                >
                  <div className="flex items-center gap-2">
                    <span className="flex items-center gap-1.5 text-caption text-muted-foreground">
                      {t(($) => $.voice.enabled_label)}
                      <Switch
                        checked={p.enabled}
                        disabled={!canManage}
                        onCheckedChange={(enabled) =>
                          updateProvider.mutate(
                            { providerId: p.id, data: { enabled } },
                            {
                              onError: (error) =>
                                toast.error(
                                  error instanceof Error
                                    ? error.message
                                    : t(($) => $.voice.save_failed_toast),
                                ),
                            },
                          )
                        }
                        aria-label={`${p.display_name} ${t(($) => $.voice.enabled_label)}`}
                      />
                    </span>
                    {canManage ? (
                      <DropdownMenu>
                        <DropdownMenuTrigger
                          render={
                            <Button variant="ghost" size="icon-sm" aria-label={p.display_name}>
                              <MoreHorizontal className="h-4 w-4" />
                            </Button>
                          }
                        />
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onClick={() => openCredential(p)}>
                            <KeyRound className="h-4 w-4" />
                            {t(($) => $.voice.action_set_credential)}
                          </DropdownMenuItem>
                          <DropdownMenuItem onClick={() => reprobe(p)}>
                            <Volume2 className="h-4 w-4" />
                            {t(($) => $.voice.action_reprobe)}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => {
                              setRenameValue(p.display_name);
                              setRenameFor(p);
                            }}
                          >
                            <Pencil className="h-4 w-4" />
                            {t(($) => $.voice.action_rename)}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            variant="destructive"
                            onClick={() => setDeleteFor(p)}
                          >
                            <Trash2 className="h-4 w-4" />
                            {t(($) => $.voice.action_delete)}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    ) : null}
                  </div>
                </SettingsRow>
              );
            })
          )}
        </SettingsCard>
      </SettingsSection>

      <SettingsSection
        title={t(($) => $.voice.default_section_title)}
        description={t(($) => $.voice.default_section_hint)}
      >
        <ChainConfigCard
          ref_={settings.default}
          eligible={eligible}
          savePending={savingChain === "default"}
          defaultModel={DEFAULT_VOICE_MODEL}
          onSave={(ref) => void saveChain("default", ref)}
          canManage={canManage}
        />
      </SettingsSection>

      <SettingsSection
        title={t(($) => $.voice.asr_section_title)}
        description={t(($) => $.voice.asr_section_hint)}
      >
        <ChainConfigCard
          ref_={settings.asr}
          eligible={eligible}
          savePending={savingChain === "asr"}
          defaultModel={DEFAULT_ASR_MODEL}
          onSave={(ref) => void saveChain("asr", ref)}
          canManage={canManage}
        />
      </SettingsSection>

      {/* --- create dialog --- */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.voice.create_dialog_title)}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <label className="block space-y-1.5">
              <span className="text-caption font-medium">
                {t(($) => $.voice.provider_name_label)}
              </span>
              <Input
                type="text"
                value={createName}
                onChange={(e) => setCreateName(e.target.value)}
                placeholder={t(($) => $.voice.provider_name_placeholder)}
                autoFocus
              />
            </label>
            <div className="space-y-1.5">
              <span className="text-caption font-medium">
                {t(($) => $.voice.provider_type_label)}
              </span>
              <Select
                items={[
                  { value: "gemini", label: t(($) => $.voice.provider_type_gemini) },
                  { value: "chatgpt", label: t(($) => $.voice.provider_type_chatgpt) },
                ]}
                value={createType}
                onValueChange={(value) => {
                  if (value != null) setCreateType(value as "gemini" | "chatgpt");
                }}
              >
                <SelectTrigger aria-label={t(($) => $.voice.provider_type_label)}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="gemini">
                    {t(($) => $.voice.provider_type_gemini)}
                  </SelectItem>
                  <SelectItem value="chatgpt">
                    {t(($) => $.voice.provider_type_chatgpt)}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          <DialogFooter>
            <Button
              onClick={submitCreate}
              disabled={!createName.trim() || createProvider.isPending}
            >
              {t(($) => $.voice.create)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* --- credential dialog --- */}
      <Dialog
        open={credentialFor != null}
        onOpenChange={(open) => {
          if (!open) setCredentialFor(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {t(($) => $.voice.credential_dialog_title)} — {credentialFor?.display_name}
            </DialogTitle>
          </DialogHeader>
          <label className="block space-y-1.5">
            <span className="text-caption font-medium">
              {t(($) => $.voice.credential_input_label)}
            </span>
            <Input
              type="password"
              value={credentialValue}
              onChange={(e) => setCredentialValue(e.target.value)}
              autoComplete="off"
            />
          </label>
          <DialogFooter>
            {credentialFor?.credential_status === "configured" ? (
              <Button
                variant="ghost"
                onClick={() => {
                  const p = credentialFor;
                  setCredentialFor(null);
                  if (p) void removeCredential(p);
                }}
                disabled={deleteCredential.isPending}
              >
                {t(($) => $.voice.credential_remove)}
              </Button>
            ) : null}
            <Button
              onClick={submitCredential}
              disabled={!credentialValue.trim() || putCredential.isPending}
            >
              {t(($) => $.voice.credential_save)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* --- rename dialog --- */}
      <Dialog
        open={renameFor != null}
        onOpenChange={(open) => {
          if (!open) setRenameFor(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.voice.rename_dialog_title)}</DialogTitle>
          </DialogHeader>
          <Input
            type="text"
            value={renameValue}
            onChange={(e) => setRenameValue(e.target.value)}
            autoFocus
          />
          <DialogFooter>
            <Button onClick={submitRename} disabled={!renameValue.trim() || updateProvider.isPending}>
              {t(($) => $.voice.save)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* --- delete confirm --- */}
      <AlertDialog
        open={deleteFor != null}
        onOpenChange={(open) => {
          if (!open) setDeleteFor(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.voice.delete_dialog_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.voice.delete_dialog_body)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.voice.cancel)}</AlertDialogCancel>
            <AlertDialogAction onClick={submitDelete}>
              {t(($) => $.voice.delete)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SettingsTab>
  );
}
