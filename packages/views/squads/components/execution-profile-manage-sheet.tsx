"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { ChevronLeft, Loader2, Plus, Trash2 } from "lucide-react";
import { useAuthStore } from "@multica/core/auth";
import { isAgentRuntimeBound } from "@multica/core/agents";
import type { ExecutionProfileEntry } from "@multica/core/types/execution-profile";
import type { Agent } from "@multica/core/types";
import {
  executionProfileDetailOptions,
  executionProfileListOptions,
  parseExecutionProfileNameConflict,
  useCreateExecutionProfile,
  useDeleteExecutionProfile,
  useDeleteExecutionProfileEntry,
  useUpdateExecutionProfile,
  useUpsertExecutionProfileEntry,
} from "@multica/core/execution-profiles/queries";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
import {
  agentListOptions,
  memberListOptions,
} from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Separator } from "@multica/ui/components/ui/separator";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
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
import { RuntimePicker } from "../../agents/components/inspector/runtime-picker";
import { ModelPicker } from "../../agents/components/inspector/model-picker";
import { ThinkingSettingField } from "../../agents/components/inspector/thinking-prop-row";
import { useT } from "../../i18n";

/**
 * Profile management drawer (RUYI-57, F4).
 *
 * Two views in one sheet: the profile list, and one profile's per-member
 * configuration. A drawer rather than a page because the members roster
 * underneath is the reference the user is configuring against — sending them
 * to a separate route would make them memorise the roster first.
 */
export function ExecutionProfileManageSheet({
  wsId,
  open,
  initialProfileId,
  onOpenChange,
}: {
  wsId: string;
  open: boolean;
  initialProfileId: string | null;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("squads");
  const [editingId, setEditingId] = useState<string | null>(initialProfileId);

  // The caller decides which view opens (create lands straight in the editor);
  // syncing on `open` keeps a reopened sheet from showing the previous target.
  useEffect(() => {
    if (open) setEditingId(initialProfileId);
  }, [open, initialProfileId]);

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="w-full gap-0 p-0 sm:max-w-[480px] data-[side=right]:sm:max-w-[480px]"
      >
        <SheetHeader className="px-5 pt-5 pb-3">
          <SheetTitle>
            {editingId
              ? t(($) => $.execution_profile.sheet_edit_title)
              : t(($) => $.execution_profile.sheet_title)}
          </SheetTitle>
          <SheetDescription>
            {editingId
              ? t(($) => $.execution_profile.sheet_edit_description)
              : t(($) => $.execution_profile.sheet_description)}
          </SheetDescription>
        </SheetHeader>
        <Separator />
        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {editingId ? (
            <ProfileEditor
              wsId={wsId}
              profileId={editingId}
              onBack={() => setEditingId(null)}
              onDeleted={() => setEditingId(null)}
            />
          ) : (
            <ProfileList wsId={wsId} onEdit={setEditingId} />
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

function ProfileList({
  wsId,
  onEdit,
}: {
  wsId: string;
  onEdit: (profileId: string) => void;
}) {
  const { t } = useT("squads");
  const listQuery = useQuery(executionProfileListOptions(wsId));
  const create = useCreateExecutionProfile(wsId);

  const handleCreate = async () => {
    try {
      const created = await create.mutateAsync({
        name: t(($) => $.execution_profile.default_new_name),
      });
      onEdit(created.id);
    } catch {
      toast.error(t(($) => $.execution_profile.create_failed));
    }
  };

  if (listQuery.isPending) {
    return (
      <div className="space-y-2">
        <Skeleton className="h-14 w-full" />
        <Skeleton className="h-14 w-full" />
      </div>
    );
  }

  if (listQuery.isError) {
    return (
      <div className="space-y-3 py-6 text-center">
        <p className="text-caption text-destructive">
          {t(($) => $.execution_profile.load_failed)}
        </p>
        <Button size="sm" variant="outline" onClick={() => listQuery.refetch()}>
          {t(($) => $.execution_profile.retry)}
        </Button>
      </div>
    );
  }

  const profiles = listQuery.data?.execution_profiles ?? [];

  return (
    <div className="space-y-3">
      {profiles.length === 0 ? (
        <div className="rounded-lg border border-dashed px-4 py-10 text-center">
          <p className="text-body font-medium">
            {t(($) => $.execution_profile.empty_title)}
          </p>
          <p className="mt-1 text-caption text-muted-foreground">
            {t(($) => $.execution_profile.empty_description)}
          </p>
        </div>
      ) : (
        <div className="space-y-2">
          {profiles.map((profile) => (
            <button
              key={profile.id}
              type="button"
              onClick={() => onEdit(profile.id)}
              className="flex w-full items-center gap-3 rounded-lg border p-3 text-left transition-colors hover:bg-accent"
            >
              <span className="min-w-0 flex-1">
                <span className="flex items-center gap-2">
                  <span className="truncate text-body font-medium">
                    {profile.name}
                  </span>
                  {profile.is_active && (
                    <span className="shrink-0 rounded-full bg-success/15 px-2 py-0.5 text-caption text-success">
                      {t(($) => $.execution_profile.active_badge)}
                    </span>
                  )}
                </span>
                <span className="mt-0.5 block truncate text-caption text-muted-foreground">
                  {profile.description ||
                    t(($) => $.execution_profile.entry_count, {
                      count: profile.entry_count,
                    })}
                </span>
              </span>
            </button>
          ))}
        </div>
      )}

      <Button
        size="sm"
        variant="outline"
        className="w-full"
        onClick={handleCreate}
        disabled={create.isPending}
      >
        {create.isPending ? (
          <Loader2 className="size-3.5 mr-1.5 animate-spin" />
        ) : (
          <Plus className="size-3.5 mr-1.5" />
        )}
        {t(($) => $.execution_profile.create_action)}
      </Button>
    </div>
  );
}

function ProfileEditor({
  wsId,
  profileId,
  onBack,
  onDeleted,
}: {
  wsId: string;
  profileId: string;
  onBack: () => void;
  onDeleted: () => void;
}) {
  const { t } = useT("squads");
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);

  const detailQuery = useQuery(executionProfileDetailOptions(wsId, profileId));
  const agentsQuery = useQuery(agentListOptions(wsId));
  const runtimesQuery = useQuery(runtimeListOptions(wsId));
  const membersQuery = useQuery(memberListOptions(wsId));

  const update = useUpdateExecutionProfile(wsId);
  const remove = useDeleteExecutionProfile(wsId);
  const upsertEntry = useUpsertExecutionProfileEntry(wsId);
  const deleteEntry = useDeleteExecutionProfileEntry(wsId);

  const profile = detailQuery.data ?? null;
  const [name, setName] = useState("");
  const [nameError, setNameError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  // Seed the rename field from the server value once it lands, and re-seed
  // whenever the sheet switches to a different profile.
  useEffect(() => {
    if (profile) setName(profile.name);
    setNameError(null);
  }, [profile?.id, profile?.name]);

  const entryByAgent = useMemo(() => {
    const map = new Map<string, ExecutionProfileEntry>();
    for (const entry of profile?.entries ?? []) map.set(entry.agent_id, entry);
    return map;
  }, [profile]);

  // Archived agents stay listed only when the profile already names them, so
  // an existing entry can be removed instead of silently failing on the next
  // activation; they are never offered as a new row to add.
  const agents = useMemo(() => {
    const all = agentsQuery.data ?? [];
    return all.filter(
      (a) => a.archived_at === null || entryByAgent.has(a.id),
    );
  }, [agentsQuery.data, entryByAgent]);

  const commitName = async () => {
    const next = name.trim();
    if (!profile || next === profile.name) return;
    if (next === "") {
      setName(profile.name);
      setNameError(null);
      return;
    }
    try {
      await update.mutateAsync({ profileId, patch: { name: next } });
      setNameError(null);
    } catch (err) {
      const conflict = parseExecutionProfileNameConflict(err);
      setNameError(conflict ?? t(($) => $.execution_profile.rename_failed));
    }
  };

  const handleDelete = async () => {
    try {
      await remove.mutateAsync(profileId);
      setConfirmDelete(false);
      onDeleted();
    } catch {
      setConfirmDelete(false);
      toast.error(t(($) => $.execution_profile.delete_failed));
    }
  };

  if (detailQuery.isPending) {
    return (
      <div className="space-y-3">
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-20 w-full" />
        <Skeleton className="h-20 w-full" />
      </div>
    );
  }

  if (detailQuery.isError || !profile) {
    return (
      <div className="space-y-3 py-6 text-center">
        <p className="text-caption text-destructive">
          {t(($) => $.execution_profile.load_failed)}
        </p>
        <Button size="sm" variant="outline" onClick={() => detailQuery.refetch()}>
          {t(($) => $.execution_profile.retry)}
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <Button size="sm" variant="ghost" className="-ml-2" onClick={onBack}>
        <ChevronLeft className="size-3.5 mr-1" />
        {t(($) => $.execution_profile.back_to_list)}
      </Button>

      <div className="space-y-1.5">
        <Label htmlFor="execution-profile-name">
          {t(($) => $.execution_profile.name_label)}
        </Label>
        <Input
          id="execution-profile-name"
          value={name}
          maxLength={50}
          onChange={(e) => setName(e.target.value)}
          onBlur={() => void commitName()}
          onKeyDown={(e) => {
            if (e.key === "Enter") e.currentTarget.blur();
          }}
        />
        {nameError && (
          <p className="text-caption text-destructive">{nameError}</p>
        )}
      </div>

      <Separator />

      <div className="space-y-3">
        <p className="text-caption text-muted-foreground">
          {t(($) => $.execution_profile.members_hint)}
        </p>
        {agents.length === 0 ? (
          <p className="text-caption text-muted-foreground">
            {t(($) => $.execution_profile.no_agents)}
          </p>
        ) : (
          agents.map((agent) => (
            <MemberEntryCard
              key={agent.id}
              agent={agent}
              entry={entryByAgent.get(agent.id) ?? null}
              runtimes={runtimesQuery.data ?? []}
              members={membersQuery.data ?? []}
              currentUserId={currentUserId}
              busy={upsertEntry.isPending || deleteEntry.isPending}
              onSave={async (body) => {
                try {
                  await upsertEntry.mutateAsync({ profileId, body });
                } catch {
                  toast.error(t(($) => $.execution_profile.entry_save_failed));
                }
              }}
              onRemove={async () => {
                try {
                  await deleteEntry.mutateAsync({
                    profileId,
                    agentId: agent.id,
                  });
                } catch {
                  toast.error(t(($) => $.execution_profile.entry_remove_failed));
                }
              }}
            />
          ))
        )}
      </div>

      <Separator />

      <Button
        size="sm"
        variant="outline"
        className="w-full text-destructive"
        onClick={() => setConfirmDelete(true)}
        disabled={remove.isPending}
      >
        <Trash2 className="size-3.5 mr-1.5" />
        {t(($) => $.execution_profile.delete_action)}
      </Button>

      <AlertDialog
        open={confirmDelete}
        onOpenChange={(next) => {
          if (!next && !remove.isPending) setConfirmDelete(false);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.execution_profile.delete_confirm_title, {
                name: profile.name,
              })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.execution_profile.delete_confirm_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {profile.is_active && (
            // Deleting the active profile is not a rollback: the agents keep
            // whatever the activation wrote. Saying so up front prevents a
            // user from deleting it expecting the old configuration back.
            <p className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-caption text-warning-foreground">
              {t(($) => $.execution_profile.delete_active_warning)}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={remove.isPending}>
              {t(($) => $.execution_profile.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={remove.isPending}
              onClick={(e) => {
                e.preventDefault();
                void handleDelete();
              }}
            >
              {remove.isPending && (
                <Loader2 className="size-3.5 mr-1.5 animate-spin" />
              )}
              {t(($) => $.execution_profile.delete_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

/**
 * One member's slot. The three pickers are staged locally and written as one
 * PUT: the server refuses a half-filled entry on purpose (an entry must be
 * activatable), so per-field autosave would fail on the first click for any
 * member not already in the profile.
 */
function MemberEntryCard({
  agent,
  entry,
  runtimes,
  members,
  currentUserId,
  busy,
  onSave,
  onRemove,
}: {
  agent: Agent;
  entry: ExecutionProfileEntry | null;
  runtimes: Parameters<typeof RuntimePicker>[0]["runtimes"];
  members: Parameters<typeof RuntimePicker>[0]["members"];
  currentUserId: string | null;
  busy: boolean;
  onSave: (body: {
    agent_id: string;
    runtime_id: string;
    model: string;
    thinking_level?: string;
  }) => Promise<void>;
  onRemove: () => Promise<void>;
}) {
  const { t } = useT("squads");
  // Draft seeded from the stored entry, or from the agent's current live
  // configuration for a member not in the profile yet — the common intent is
  // "capture what this member runs now", not "start from blank".
  const [runtimeId, setRuntimeId] = useState(
    entry?.runtime_id ?? (isAgentRuntimeBound(agent) ? agent.runtime_id : ""),
  );
  const [model, setModel] = useState(entry?.model ?? agent.model ?? "");
  // The drawer always states an opinion: "" here is the field's "runtime
  // default" choice, which the entry stores as an explicit clear. A stored
  // null (no opinion, only reachable via the API) seeds the same empty field.
  const [thinking, setThinking] = useState(
    entry?.thinking_level ?? agent.thinking_level ?? "",
  );

  useEffect(() => {
    if (!entry) return;
    setRuntimeId(entry.runtime_id);
    setModel(entry.model);
    setThinking(entry.thinking_level ?? "");
  }, [entry?.runtime_id, entry?.model, entry?.thinking_level]);

  const runtime = runtimes.find((r) => r.id === runtimeId) ?? null;
  const runtimeOnline = runtime?.status === "online";
  const complete = runtimeId !== "" && model !== "";
  const dirty =
    entry === null ||
    entry.runtime_id !== runtimeId ||
    entry.model !== model ||
    // A stored null and a staged "" differ: saving turns "no opinion" into an
    // explicit clear, so that must count as a change worth saving.
    (entry.thinking_level ?? null) !== thinking;

  return (
    <div className="space-y-2 rounded-lg border p-3">
      <div className="flex items-center justify-between gap-2">
        <span className="min-w-0 truncate text-body font-medium">
          {agent.name}
        </span>
        {entry && (
          <Button
            size="sm"
            variant="ghost"
            className="h-7 px-2 text-caption text-muted-foreground"
            disabled={busy}
            onClick={() => void onRemove()}
          >
            {t(($) => $.execution_profile.entry_remove)}
          </Button>
        )}
      </div>

      <RuntimePicker
        variant="field"
        showLabel={false}
        value={runtimeId}
        runtimes={runtimes}
        members={members}
        currentUserId={currentUserId}
        onChange={(id) => {
          // Model and thinking level are runtime-native; keeping them across a
          // runtime switch would stage a combination the server rejects.
          setRuntimeId(id);
          setModel("");
          setThinking("");
        }}
      />
      <ModelPicker
        variant="field"
        showLabel={false}
        runtimeId={runtimeId || null}
        runtimeOnline={runtimeOnline}
        value={model}
        onChange={(next) => setModel(next)}
      />
      <ThinkingSettingField
        label={t(($) => $.execution_profile.thinking_label)}
        runtimeId={runtimeId || null}
        runtimeOnline={runtimeOnline}
        provider={runtime?.provider ?? ""}
        model={model}
        value={thinking}
        canEdit
        onChange={(next) => setThinking(next)}
      />

      <div className="flex items-center justify-end gap-2">
        {!complete && dirty && (
          <span className="text-caption text-muted-foreground">
            {t(($) => $.execution_profile.entry_incomplete_hint)}
          </span>
        )}
        <Button
          size="sm"
          disabled={!complete || !dirty || busy}
          onClick={() =>
            void onSave({
              agent_id: agent.id,
              runtime_id: runtimeId,
              model,
              thinking_level: thinking,
            })
          }
        >
          {t(($) => $.execution_profile.entry_save)}
        </Button>
      </div>
    </div>
  );
}
