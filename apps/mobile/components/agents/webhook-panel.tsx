/**
 * Agent webhook panel (RUYI-346) — mobile counterpart of web's
 * `packages/views/agents/components/tabs/webhooks-tab.tsx`. Semantics mirror:
 *
 *  - Managers (agent owner / workspace admin) get full CRUD; non-managers
 *    get a read-only list whose URLs render as the fixed-width mask — the
 *    server strips the credential fields from their responses entirely, and
 *    the add/manage affordances don't render at all (hidden, not disabled).
 *  - Every link is a bearer credential: copy is one tap, rotation and delete
 *    are Alert-confirms, and the one-time post-create panel warns the link
 *    will be masked again once the panel is dismissed.
 *  - Caps and validation match the server (20 per agent; name ≤ 50,
 *    prompt ≤ 4000, both required) — the server stays the boundary.
 */
import { useState } from "react";
import { Alert, Pressable, TextInput, View } from "react-native";
import * as Clipboard from "expo-clipboard";
import * as Haptics from "expo-haptics";
import { Ionicons } from "@expo/vector-icons";
import type { AgentWebhook } from "@multica/core/types";
import {
  buildAgentWebhookUrl,
  maskedAgentWebhookUrlPreview,
} from "@multica/core/agents";
import { Text } from "@/components/ui/text";
import { Switch } from "@/components/ui/switch";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { agentWebhooksOptions } from "@/data/queries/agents";
import {
  useCreateAgentWebhook,
  useDeleteAgentWebhook,
  useRotateAgentWebhook,
  useToggleAgentWebhook,
  useUpdateAgentWebhook,
} from "@/data/mutations/agents";
import { useWorkspaceStore } from "@/data/workspace-store";
import { getApiUrl } from "@/data/server-store";
import { useQuery } from "@tanstack/react-query";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

const MAX_AGENT_WEBHOOKS = 20;
const NAME_MAX_LEN = 50;
const PROMPT_MAX_LEN = 4000;
// Char counter appears only near the limit — a permanent counter on an
// optional-feeling free-text field is noise (same rule as web).
const PROMPT_COUNTER_THRESHOLD = Math.floor(PROMPT_MAX_LEN * 0.8);

interface Props {
  agentId: string;
  canManage: boolean;
}

/** One-time post-create / post-rotate reveal — dismissed by Done. */
interface RevealedLink {
  title: string;
  url: string;
}

export function WebhookPanel({ agentId, canManage }: Props) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");

  const webhooksQuery = useQuery(agentWebhooksOptions(wsId, agentId));
  const webhooks = webhooksQuery.data ?? [];
  const atLimit = webhooks.length >= MAX_AGENT_WEBHOOKS;
  const hasEverLoaded = webhooksQuery.isSuccess || webhooksQuery.isError;

  const create = useCreateAgentWebhook(agentId);
  const update = useUpdateAgentWebhook(agentId);
  const toggle = useToggleAgentWebhook(agentId);
  const rotate = useRotateAgentWebhook(agentId);
  const remove = useDeleteAgentWebhook(agentId);

  const [formMode, setFormMode] = useState<
    { kind: "create" } | { kind: "edit"; webhook: AgentWebhook } | null
  >(null);
  const [revealed, setRevealed] = useState<RevealedLink | null>(null);

  const resolveUrl = (webhook: AgentWebhook) =>
    canManage
      ? buildAgentWebhookUrl({ webhook, apiBaseUrl: getApiUrl() }) ??
        maskedAgentWebhookUrlPreview({ apiBaseUrl: getApiUrl() })
      : maskedAgentWebhookUrlPreview({ apiBaseUrl: getApiUrl() });

  const copyLink = async (url: string) => {
    await Clipboard.setStringAsync(url);
    Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success).catch(
      () => {},
    );
  };

  const confirmRotate = (webhook: AgentWebhook) => {
    Alert.alert(
      t("webhooks.rotate_title", "Regenerate link?"),
      t(
        "webhooks.rotate_description",
        "The old link stops working immediately and must be updated in the external system (such as GitHub). Copy the new link after regenerating.",
      ),
      [
        { text: t("webhooks.cancel", "Cancel"), style: "cancel" },
        {
          text: t("webhooks.rotate_confirm", "Regenerate"),
          style: "destructive",
          onPress: () =>
            rotate.mutate(webhook.id, {
              onSuccess: (next) =>
                setRevealed({
                  title: t("mobile.webhooks.rotated_title", "New link generated"),
                  url: resolveUrl(next),
                }),
              onError: () =>
                Alert.alert(
                  t("webhooks.form_submit_failed", "Something went wrong. Please try again."),
                ),
            }),
        },
      ],
    );
  };

  const confirmDelete = (webhook: AgentWebhook) => {
    Alert.alert(
      t("webhooks.delete_title", 'Delete webhook "{{name}}"?', {
        name: webhook.name,
      }),
      t(
        "webhooks.delete_description",
        "The link stops working immediately and external integrations (such as GitHub) will fail to trigger. The bound prompt is removed too. This cannot be undone.",
      ),
      [
        { text: t("webhooks.cancel", "Cancel"), style: "cancel" },
        {
          text: t("webhooks.delete_confirm", "Delete"),
          style: "destructive",
          onPress: () =>
            remove.mutate(webhook.id, {
              onError: () =>
                Alert.alert(
                  t("webhooks.form_submit_failed", "Something went wrong. Please try again."),
                ),
            }),
        },
      ],
    );
  };

  // --- One-time link reveal panel (post-create / post-rotate) ---
  if (revealed) {
    return (
      <View className="flex-1 px-4 pt-4 gap-3">
        <View className="rounded-lg border border-border px-3 py-3 gap-2">
          <Text className="text-base font-semibold text-foreground">
            {revealed.title}
          </Text>
          <Text className="text-sm text-muted-foreground">
            {t(
              "webhooks.created_description",
              "Copy the link below and configure it in GitHub or any other external system. Every visit starts a new chat session with the bound prompt.",
            )}
          </Text>
          <Text className="text-caption font-medium text-muted-foreground mt-1">
            {t("webhooks.url_label", "Webhook link")}
          </Text>
          <View className="flex-row items-center gap-2 rounded-md bg-secondary px-3 py-2">
            <Text
              numberOfLines={1}
              className="flex-1 text-xs text-foreground"
              selectable
            >
              {revealed.url}
            </Text>
            <Pressable
              onPress={() => copyLink(revealed.url)}
              hitSlop={8}
              accessibilityRole="button"
              accessibilityLabel={t("mobile.webhooks.copy_aria", "Copy webhook link")}
            >
              <Ionicons
                name="copy-outline"
                size={18}
                color={THEME[colorScheme].primary}
              />
            </Pressable>
          </View>
          <Text className="text-xs text-muted-foreground">
            {t(
              "webhooks.created_warning",
              "A link is a credential — do not share it publicly. After this panel closes the link is masked in the list; you can copy it again at any time.",
            )}
          </Text>
          <Text className="text-xs text-muted-foreground">
            {t(
              "webhooks.created_test_hint",
              "Tip: open the link directly in a browser to test the trigger.",
            )}
          </Text>
          <Pressable
            onPress={() => setRevealed(null)}
            className="mt-1 self-stretch items-center rounded-lg bg-primary py-2.5 active:opacity-80"
            accessibilityRole="button"
            accessibilityLabel={t("webhooks.created_done", "Done")}
          >
            <Text className="text-sm font-medium text-primary-foreground">
              {t("webhooks.created_done", "Done")}
            </Text>
          </Pressable>
        </View>
      </View>
    );
  }

  return (
    <View className="flex-1" testID="webhook-panel">
      <View className="px-4 pt-3 gap-2">
        <Text className="text-xs text-muted-foreground leading-4">
          {t(
            "webhooks.intro",
            "Trigger this agent over HTTP: each webhook binds a fixed prompt and gets a unique, sign-in-free link. Every visit starts a new chat session with that prompt.",
          )}
        </Text>
        <Text className="text-xs text-muted-foreground leading-4">
          {t(
            "webhooks.security_hint",
            "⚠ A link is a credential: anyone who holds it can trigger this agent and consume run resources. Treat links like passwords.",
          )}
        </Text>
      </View>

      {webhooksQuery.isPending && !webhooksQuery.isError ? (
        <View className="px-4 pt-3 gap-2">
          <View className="rounded-lg border border-border px-3 py-3 gap-2">
            <View className="h-4 w-1/3 rounded bg-secondary" />
            <View className="h-3 w-2/3 rounded bg-secondary" />
          </View>
        </View>
      ) : webhooksQuery.isError ? (
        <View className="px-4 pt-4">
          <Text className="text-sm text-muted-foreground text-center">
            {t("webhooks.load_failed", "Couldn't load webhooks")}
          </Text>
          <Pressable
            onPress={() => webhooksQuery.refetch()}
            className="mt-2 self-center rounded-lg border border-border px-4 py-2 active:opacity-80"
            accessibilityRole="button"
            accessibilityLabel={t("webhooks.retry", "Retry")}
          >
            <Text className="text-sm text-foreground">
              {t("webhooks.retry", "Retry")}
            </Text>
          </Pressable>
        </View>
      ) : webhooks.length === 0 ? (
        <View className="px-4 pt-8 items-center gap-2">
          <Ionicons
            name="git-branch-outline"
            size={36}
            color={THEME[colorScheme].mutedForeground}
          />
          <Text className="text-base font-medium text-foreground">
            {canManage
              ? t("webhooks.empty_title", "No webhooks yet")
              : t("webhooks.empty_readonly", "This agent has no webhooks configured.")}
          </Text>
          {canManage ? (
            <Text className="text-sm text-muted-foreground text-center">
              {t(
                "webhooks.empty_description",
                "Add one to get a sign-in-free unique link; every visit starts a new session with the bound prompt.",
              )}
            </Text>
          ) : null}
        </View>
      ) : (
        <View className="px-4 pt-3 gap-2">
          {webhooks.map((wh) => (
            <WebhookRow
              key={wh.id}
              webhook={wh}
              canManage={canManage}
              url={resolveUrl(wh)}
              togglePending={toggle.isPending && toggle.variables?.webhookId === wh.id}
              onCopy={() => copyLink(resolveUrl(wh))}
              onToggle={(enabled) => toggle.mutate({ webhookId: wh.id, enabled })}
              onEdit={() => setFormMode({ kind: "edit", webhook: wh })}
              onRotate={() => confirmRotate(wh)}
              onDelete={() => confirmDelete(wh)}
            />
          ))}
        </View>
      )}

      {canManage && hasEverLoaded ? (
        <View className="px-4 pt-3 pb-6 gap-1.5">
          {formMode?.kind === "create" ? null : (
            <>
              {atLimit ? (
                <Text className="text-xs text-muted-foreground text-center">
                  {t("webhooks.limit_reached", "Limit reached (max {{max}} per agent)", {
                    max: MAX_AGENT_WEBHOOKS,
                  })}
                </Text>
              ) : null}
              <Pressable
                onPress={() => setFormMode({ kind: "create" })}
                disabled={atLimit}
                className={`flex-row items-center justify-center gap-2 rounded-lg border border-dashed border-border py-2.5 ${
                  atLimit ? "opacity-40" : "active:bg-secondary"
                }`}
                accessibilityRole="button"
                accessibilityLabel={t("webhooks.add", "Add webhook")}
              >
                <Ionicons
                  name="add"
                  size={16}
                  color={THEME[colorScheme].mutedForeground}
                />
                <Text className="text-sm text-muted-foreground">
                  {t("webhooks.add", "Add webhook")}
                </Text>
              </Pressable>
            </>
          )}
        </View>
      ) : null}

      {formMode ? (
        <WebhookForm
          initial={formMode.kind === "edit" ? formMode.webhook : null}
          submitting={formMode.kind === "create" ? create.isPending : update.isPending}
          submitLabel={
            formMode.kind === "create"
              ? t("webhooks.form_create", "Create")
              : t("webhooks.form_save", "Save")
          }
          onSubmit={async ({ name, prompt }) => {
            if (formMode.kind === "create") {
              create.mutate(
                { name, prompt },
                {
                  onSuccess: (wh) => {
                    setFormMode(null);
                    setRevealed({
                      title: t("webhooks.created_title", "Webhook created"),
                      url: resolveUrl(wh),
                    });
                  },
                  onError: () =>
                    Alert.alert(
                      t("webhooks.form_submit_failed", "Something went wrong. Please try again."),
                    ),
                },
              );
            } else {
              update.mutate(
                { webhookId: formMode.webhook.id, body: { name, prompt } },
                {
                  onSuccess: () => setFormMode(null),
                  onError: () =>
                    Alert.alert(
                      t("webhooks.form_submit_failed", "Something went wrong. Please try again."),
                    ),
                },
              );
            }
          }}
          onCancel={() => setFormMode(null)}
        />
      ) : null}
    </View>
  );
}

// ── Row ─────────────────────────────────────────────────────────────────────

function WebhookRow({
  webhook,
  canManage,
  url,
  togglePending,
  onCopy,
  onToggle,
  onEdit,
  onRotate,
  onDelete,
}: {
  webhook: AgentWebhook;
  canManage: boolean;
  /** Resolved display URL — full link for managers, fixed mask otherwise. */
  url: string;
  togglePending: boolean;
  onCopy: () => void;
  onToggle: (enabled: boolean) => void;
  onEdit: () => void;
  onRotate: () => void;
  onDelete: () => void;
}) {
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");

  return (
    <View className="rounded-lg border border-border px-3 py-2.5 gap-1.5">
      <View className="flex-row items-center gap-2">
        <Text
          numberOfLines={1}
          className={`flex-1 text-sm font-medium text-foreground ${
            webhook.enabled ? "" : "opacity-60"
          }`}
        >
          {webhook.name}
        </Text>
        {!webhook.enabled ? (
          <View className="rounded bg-secondary px-1.5 py-0.5">
            <Text className="text-[10px] text-muted-foreground">
              {t("webhooks.disabled_badge", "Disabled")}
            </Text>
          </View>
        ) : null}
        {canManage ? (
          <Switch
            checked={webhook.enabled}
            disabled={togglePending}
            onCheckedChange={onToggle}
            aria-label={webhook.name}
          />
        ) : null}
      </View>
      <View className="flex-row items-center gap-2">
        <Text numberOfLines={1} className="flex-1 text-xs text-muted-foreground" selectable>
          {url}
        </Text>
        {canManage ? (
          <Pressable
            onPress={onCopy}
            hitSlop={8}
            accessibilityRole="button"
            accessibilityLabel={t("mobile.webhooks.copy_aria", "Copy webhook link")}
          >
            <Ionicons
              name="copy-outline"
              size={16}
              color={THEME[colorScheme].mutedForeground}
            />
          </Pressable>
        ) : null}
      </View>
      {webhook.prompt ? (
        <Text numberOfLines={2} className="text-xs text-muted-foreground">
          {t("webhooks.prompt_prefix", "Prompt: ")}
          {webhook.prompt}
        </Text>
      ) : null}
      {canManage ? (
        <View className="flex-row items-center gap-4 pt-1">
          <RowAction icon="pencil-outline" label={t("webhooks.edit", "Edit name and prompt")} onPress={onEdit} />
          <RowAction icon="refresh-outline" label={t("webhooks.rotate", "Regenerate link")} onPress={onRotate} />
          <RowAction
            icon="trash-outline"
            label={t("webhooks.delete", "Delete")}
            color={THEME[colorScheme].destructive}
            onPress={onDelete}
          />
        </View>
      ) : null}
    </View>
  );
}

function RowAction({
  icon,
  label,
  color,
  onPress,
}: {
  icon: React.ComponentProps<typeof Ionicons>["name"];
  label: string;
  color?: string;
  onPress: () => void;
}) {
  const { colorScheme } = useColorScheme();
  return (
    <Pressable
      onPress={onPress}
      hitSlop={4}
      className="flex-row items-center gap-1 active:opacity-60"
      accessibilityRole="button"
      accessibilityLabel={label}
    >
      <Ionicons name={icon} size={14} color={color ?? THEME[colorScheme].mutedForeground} />
      <Text className="text-xs" style={{ color: color ?? THEME[colorScheme].mutedForeground }}>
        {label}
      </Text>
    </Pressable>
  );
}

// ── Create / edit form ──────────────────────────────────────────────────────

export interface WebhookFormValues {
  name: string;
  prompt: string;
}

export function WebhookForm({
  initial,
  submitting,
  submitLabel,
  onSubmit,
  onCancel,
}: {
  initial: Pick<AgentWebhook, "name" | "prompt"> | null;
  submitting: boolean;
  submitLabel: string;
  onSubmit: (values: WebhookFormValues) => void;
  onCancel: () => void;
}) {
  const { t } = useT("agents");
  const [name, setName] = useState(initial?.name ?? "");
  const [prompt, setPrompt] = useState(initial?.prompt ?? "");

  // Same validation contract as web: both trimmed fields required, length
  // caps mirror the server. First error wins.
  const nameError =
    name.trim().length > NAME_MAX_LEN
      ? t("webhooks.form_name_too_long", "Name must be at most 50 characters")
      : null;
  const promptError =
    prompt.trim().length > PROMPT_MAX_LEN
      ? t("webhooks.form_prompt_too_long", "Prompt must be at most 4000 characters")
      : null;
  const canSubmit =
    !nameError &&
    !promptError &&
    name.trim().length > 0 &&
    prompt.trim().length > 0 &&
    !submitting;

  return (
    <View className="mx-4 mb-6 mt-2 rounded-lg border border-border px-3 py-3 gap-2.5">
      {initial ? (
        <Text className="text-sm font-semibold text-foreground">
          {t("webhooks.edit_title", "Edit webhook")}
        </Text>
      ) : (
        <Text className="text-sm font-semibold text-foreground">
          {t("webhooks.form_title", "Add webhook")}
        </Text>
      )}
      {initial ? (
        <Text className="text-xs text-muted-foreground">
          {t(
            "webhooks.edit_url_unchanged",
            "The link and its token stay the same — only the name and prompt change.",
          )}
        </Text>
      ) : null}

      <Text className="text-xs font-medium text-muted-foreground">
        {t("webhooks.form_name_label", "Name")}
      </Text>
      <TextInput
        value={name}
        onChangeText={setName}
        placeholder={t("webhooks.form_name_placeholder", "e.g. GitHub push trigger")}
        placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
        maxLength={NAME_MAX_LEN + 10}
        editable={!submitting}
        className="rounded-md border border-border px-3 py-2 text-sm text-foreground"
      />
      {nameError ? <Text className="text-xs text-destructive">{nameError}</Text> : null}

      <Text className="text-xs font-medium text-muted-foreground">
        {t("webhooks.form_prompt_label", "Prompt")}
      </Text>
      <TextInput
        value={prompt}
        onChangeText={setPrompt}
        placeholder={t(
          "webhooks.form_prompt_placeholder",
          "When the webhook is visited, the agent starts a new session with this prompt. e.g. Check the repository's latest commits and summarize changes and risks.",
        )}
        placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
        multiline
        textAlignVertical="top"
        className="rounded-md border border-border px-3 py-2 text-sm text-foreground min-h-20"
        editable={!submitting}
      />
      {promptError ? (
        <Text className="text-xs text-destructive">{promptError}</Text>
      ) : prompt.trim().length > PROMPT_COUNTER_THRESHOLD ? (
        <Text className="text-xs text-muted-foreground self-end">
          {prompt.trim().length}/{PROMPT_MAX_LEN}
        </Text>
      ) : null}

      <View className="flex-row items-center justify-end gap-2 pt-1">
        <Pressable
          onPress={onCancel}
          disabled={submitting}
          className="rounded-lg border border-border px-4 py-2 active:opacity-80"
          accessibilityRole="button"
          accessibilityLabel={t("webhooks.cancel", "Cancel")}
        >
          <Text className="text-sm text-foreground">
            {t("webhooks.cancel", "Cancel")}
          </Text>
        </Pressable>
        <Pressable
          onPress={() => onSubmit({ name: name.trim(), prompt: prompt.trim() })}
          disabled={!canSubmit}
          className={`rounded-lg bg-primary px-4 py-2 ${
            canSubmit ? "active:opacity-80" : "opacity-50"
          }`}
          accessibilityRole="button"
          accessibilityLabel={submitLabel}
        >
          <Text className="text-sm font-medium text-primary-foreground">
            {submitting
              ? t("webhooks.form_saving", "Saving…")
              : submitLabel}
          </Text>
        </Pressable>
      </View>
    </View>
  );
}
