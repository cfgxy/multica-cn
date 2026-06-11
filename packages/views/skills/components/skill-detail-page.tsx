"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import {
  AlertCircle,
  AlertTriangle,
  ArrowLeft,
  Copy,
  HardDrive,
  Loader2,
  Lock,
  MoreHorizontal,
  Plus,
  Save,
  Sparkles,
  Trash2,
  X,
} from "lucide-react";
import type {
  Agent,
  MemberWithUser,
  Skill,
  SkillFile,
  UpdateSkillRequest,
} from "@multica/core/types";
import { toast } from "sonner";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useTimeAgo } from "../../i18n";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  agentListOptions,
  memberListOptions,
  selectSkillAssignments,
  skillDetailOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { runtimeListOptions } from "@multica/core/runtimes";
import { parseFrontmatter } from "@multica/core/skills/frontmatter";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@multica/ui/components/ui/tabs";
import { Textarea } from "@multica/ui/components/ui/textarea";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { AppLink, useNavigation } from "../../navigation";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { Markdown } from "../../common/markdown";
import { useCanEditSkill } from "../hooks/use-can-edit-skill";
import { useSkillPermissions } from "@multica/core/permissions";
import { CapabilityBanner } from "@multica/ui/components/common/capability-banner";
import { readOrigin, totalFileCount } from "../lib/origin";
import { FileTree } from "./file-tree";
import { FileViewer } from "./file-viewer";
import {
  AddToAgentDialog,
  type SkillActionsContext,
} from "./skill-list-actions";
import { useT } from "../../i18n";

const SKILL_MD = "SKILL.md";
const TAB_QUERY_KEY = "tab";

type DraftFile = { id?: string; path: string; content: string };
type DetailTab = "overview" | "files";

// ---------------------------------------------------------------------------
// File path validation + inline add
// ---------------------------------------------------------------------------

function useValidateNewFilePath() {
  const { t } = useT("skills");
  return (path: string, existing: string[]): string => {
    const p = path.trim();
    if (!p) return t(($) => $.detail.add_file.errors.empty);
    if (p.startsWith("/")) return t(($) => $.detail.add_file.errors.absolute);
    if (p.split("/").includes("..")) return t(($) => $.detail.add_file.errors.double_dot);
    if (p === SKILL_MD) return t(($) => $.detail.add_file.errors.reserved);
    if (existing.includes(p)) return t(($) => $.detail.add_file.errors.exists);
    return "";
  };
}

function AddFileInline({
  existingPaths,
  onAdd,
  onCancel,
}: {
  existingPaths: string[];
  onAdd: (path: string) => void;
  onCancel: () => void;
}) {
  const { t } = useT("skills");
  const validate = useValidateNewFilePath();
  const [path, setPath] = useState("");
  const [error, setError] = useState("");

  const submit = () => {
    const err = validate(path, existingPaths);
    if (err) {
      setError(err);
      return;
    }
    onAdd(path.trim());
  };

  return (
    <div className="border-b bg-muted/30 px-2 py-2">
      <Input
        autoFocus
        value={path}
        onChange={(e) => {
          setPath(e.target.value);
          setError("");
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") submit();
          if (e.key === "Escape") onCancel();
        }}
        placeholder={t(($) => $.detail.add_file.placeholder)}
        className="h-7 font-mono text-xs"
      />
      {error && (
        <p role="alert" className="mt-1 text-xs text-destructive">
          {error}
        </p>
      )}
      <div className="mt-1.5 flex items-center gap-1.5">
        <Button type="button" size="xs" onClick={submit}>
          {t(($) => $.detail.add_file.add)}
        </Button>
        <Button type="button" size="xs" variant="ghost" onClick={onCancel}>
          {t(($) => $.detail.add_file.cancel)}
        </Button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Overview tab building blocks
// ---------------------------------------------------------------------------

function SectionHeader({
  title,
  action,
}: {
  title: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="mb-2 flex h-7 items-center justify-between">
      <h3 className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
        {title}
      </h3>
      {action}
    </div>
  );
}

// Stacked label-over-value rows — the card lives in the narrow right rail,
// where side-by-side labels would squeeze the values.
function MetaRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="px-3 py-2">
      <dt className="mb-0.5 text-xs text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-sm">{children}</dd>
    </div>
  );
}

function UsedByPanel({
  skill,
  agents,
  ctx,
}: {
  skill: Skill;
  agents: Agent[];
  ctx: SkillActionsContext;
}) {
  const { t } = useT("skills");
  const qc = useQueryClient();
  const [addOpen, setAddOpen] = useState(false);
  const [unbindingId, setUnbindingId] = useState<string | null>(null);

  // Mirrors the server-side canManageAgent gate (owner or workspace admin).
  const canManage = (a: Agent) =>
    ctx.isAdmin || (a.owner_id !== null && a.owner_id === ctx.currentUserId);

  const handleUnbind = async (agent: Agent) => {
    setUnbindingId(agent.id);
    try {
      const remaining = agent.skills
        .filter((s) => s.id !== skill.id)
        .map((s) => s.id);
      await api.setAgentSkills(agent.id, { skill_ids: remaining });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(ctx.wsId) });
      toast.success(
        t(($) => $.detail.overview.unbound_toast, { name: agent.name }),
      );
    } catch (e) {
      toast.error(
        e instanceof Error && e.message
          ? e.message
          : t(($) => $.detail.overview.unbind_failed_toast),
      );
    } finally {
      setUnbindingId(null);
    }
  };

  return (
    <section>
      <SectionHeader
        title={t(($) => $.detail.overview.used_by, { count: agents.length })}
        action={
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  onClick={() => setAddOpen(true)}
                  className="text-muted-foreground"
                  aria-label={t(($) => $.actions.add_to_agent)}
                >
                  <Plus className="h-3.5 w-3.5" />
                </Button>
              }
            />
            <TooltipContent>{t(($) => $.actions.add_to_agent)}</TooltipContent>
          </Tooltip>
        }
      />
      {agents.length === 0 ? (
        <div className="rounded-md border border-dashed px-3 py-4 text-center text-xs text-muted-foreground">
          {t(($) => $.detail.overview.used_by_empty)}
        </div>
      ) : (
        <ul className="space-y-1.5">
          {agents.map((a) => (
            <li
              key={a.id}
              className="group/agent flex items-center gap-2 rounded-md border bg-card px-2.5 py-1.5"
            >
              <ActorAvatar
                name={a.name}
                initials={a.name.slice(0, 2).toUpperCase()}
                avatarUrl={resolvePublicFileUrl(a.avatar_url)}
                isAgent
                size={22}
              />
              <div className="min-w-0 flex-1">
                <div className="truncate text-xs font-medium">{a.name}</div>
                {a.description && (
                  <div className="truncate text-xs text-muted-foreground">
                    {a.description}
                  </div>
                )}
              </div>
              {canManage(a) && (
                <button
                  type="button"
                  disabled={unbindingId === a.id}
                  onClick={() => handleUnbind(a)}
                  aria-label={t(($) => $.detail.overview.unbind_aria, {
                    name: a.name,
                  })}
                  className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity hover:bg-accent hover:text-accent-foreground focus-visible:opacity-100 group-hover/agent:opacity-100"
                >
                  {unbindingId === a.id ? (
                    <Loader2 className="size-3 animate-spin" />
                  ) : (
                    <X className="size-3" />
                  )}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      <AddToAgentDialog
        skills={[skill]}
        ctx={ctx}
        open={addOpen}
        onOpenChange={setAddOpen}
      />
    </section>
  );
}

// ---------------------------------------------------------------------------
// Main page
// ---------------------------------------------------------------------------

export function SkillDetailPage({ skillId }: { skillId: string }) {
  const { t } = useT("skills");
  const timeAgo = useTimeAgo();
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);

  const {
    data: skill,
    isLoading,
    error,
  } = useQuery(skillDetailOptions(wsId, skillId));
  const { data: agents = [], error: agentsError } = useQuery(
    agentListOptions(wsId),
  );
  const { data: members = [], error: membersError } = useQuery(
    memberListOptions(wsId),
  );
  const { data: runtimes = [], error: runtimesError } = useQuery(
    runtimeListOptions(wsId),
  );

  const assignments = useMemo(
    () => selectSkillAssignments(agents),
    [agents],
  );

  const canEdit = useCanEditSkill(skill, wsId);
  const skillPermissions = useSkillPermissions(skill ?? null, wsId);

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [content, setContent] = useState("");
  const [files, setFiles] = useState<DraftFile[]>([]);
  const [selectedPath, setSelectedPath] = useState(SKILL_MD);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [addingFile, setAddingFile] = useState(false);
  const [conflictPending, setConflictPending] = useState(false);
  const [editingMeta, setEditingMeta] = useState(false);

  const draftRef = useRef({ name, description, content, files });
  draftRef.current = { name, description, content, files };

  const seededKeyRef = useRef<string | null>(null);

  useEffect(() => {
    if (!skill) return;
    const key = `${wsId}:${skill.id}@${skill.updated_at}`;
    if (seededKeyRef.current === key) return;

    const sameSkill =
      seededKeyRef.current !== null &&
      seededKeyRef.current.startsWith(`${wsId}:${skill.id}@`);

    if (sameSkill) {
      const d = draftRef.current;
      const serverFilesJson = JSON.stringify(
        (skill.files ?? []).map((f) => ({ path: f.path, content: f.content })),
      );
      const draftFilesJson = JSON.stringify(
        d.files.map((f) => ({ path: f.path, content: f.content })),
      );
      const hasEdits =
        d.name.trim() !== skill.name ||
        d.description.trim() !== skill.description ||
        d.content !== skill.content ||
        draftFilesJson !== serverFilesJson;
      if (hasEdits) {
        setConflictPending(true);
        return;
      }
    }

    seededKeyRef.current = key;
    setConflictPending(false);
    setName(skill.name);
    setDescription(skill.description);
    setContent(skill.content);
    setFiles(
      (skill.files ?? []).map((f: SkillFile) => ({
        id: f.id,
        path: f.path,
        content: f.content,
      })),
    );
    if (!sameSkill) setSelectedPath(SKILL_MD);
  }, [skill, wsId]);

  const creator = useMemo<MemberWithUser | null>(
    () =>
      skill?.created_by
        ? members.find((m) => m.user_id === skill.created_by) ?? null
        : null,
    [members, skill?.created_by],
  );

  const myRole = useMemo(
    () =>
      members.find((m) => m.user_id === currentUserId)?.role ?? null,
    [members, currentUserId],
  );
  const isAdmin = myRole === "owner" || myRole === "admin";

  const actionsCtx = useMemo<SkillActionsContext>(
    () => ({ wsId, agents, currentUserId, isAdmin }),
    [wsId, agents, currentUserId, isAdmin],
  );

  const origin = useMemo(
    () => (skill ? readOrigin(skill) : null),
    [skill],
  );
  const originRuntime = useMemo(() => {
    if (!origin || origin.type !== "runtime_local" || !origin.runtime_id)
      return null;
    return runtimes.find((r) => r.id === origin.runtime_id) ?? null;
  }, [origin, runtimes]);

  const skillAgents = useMemo(
    () => assignments.get(skillId) ?? [],
    [assignments, skillId],
  );

  const fileMap = useMemo(() => {
    const map = new Map<string, string>();
    map.set(SKILL_MD, content);
    for (const f of files) if (f.path.trim()) map.set(f.path, f.content);
    return map;
  }, [content, files]);
  const filePaths = useMemo(() => Array.from(fileMap.keys()), [fileMap]);
  const selectedContent = fileMap.get(selectedPath) ?? "";

  useEffect(() => {
    if (selectedPath !== SKILL_MD && !fileMap.has(selectedPath)) {
      setSelectedPath(SKILL_MD);
    }
  }, [fileMap, selectedPath]);

  const isDirty = useMemo(() => {
    if (!skill) return false;
    const serverFiles = (skill.files ?? []).map((f: SkillFile) => ({
      path: f.path,
      content: f.content,
    }));
    const draftFiles = files.map((f) => ({ path: f.path, content: f.content }));
    return (
      name.trim() !== skill.name ||
      description.trim() !== skill.description ||
      content !== skill.content ||
      JSON.stringify(draftFiles) !== JSON.stringify(serverFiles)
    );
  }, [skill, name, description, content, files]);

  // Preview renders the draft body so what you see is what will be saved;
  // frontmatter stays a Files-tab concern.
  const previewBody = useMemo(
    () => parseFrontmatter(content).body.trim(),
    [content],
  );

  const seedFromSkill = (s: Skill) => {
    setName(s.name);
    setDescription(s.description);
    setContent(s.content);
    setFiles(
      (s.files ?? []).map((f: SkillFile) => ({
        id: f.id,
        path: f.path,
        content: f.content,
      })),
    );
  };

  const handleSave = async () => {
    if (!skill || !canEdit) return;
    const trimmedName = name.trim();
    const trimmedDesc = description.trim();
    setSaving(true);
    try {
      const payload: UpdateSkillRequest = {
        name: trimmedName,
        description: trimmedDesc,
        content,
        files: files.filter((f) => f.path.trim()),
      };
      const updated = await api.updateSkill(skill.id, payload);
      qc.setQueryData(
        skillDetailOptions(wsId, skill.id).queryKey,
        updated,
      );
      seedFromSkill(updated);
      seededKeyRef.current = `${wsId}:${updated.id}@${updated.updated_at}`;
      setConflictPending(false);
      setEditingMeta(false);
      qc.invalidateQueries({
        queryKey: workspaceKeys.skills(wsId),
        exact: true,
      });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      toast.success(t(($) => $.detail.toast_saved));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t(($) => $.detail.toast_save_failed));
    } finally {
      setSaving(false);
    }
  };

  const handleDiscard = () => {
    if (!skill) return;
    seedFromSkill(skill);
    seededKeyRef.current = `${wsId}:${skill.id}@${skill.updated_at}`;
    setConflictPending(false);
    setEditingMeta(false);
  };

  const handleDelete = async () => {
    if (!skill) return;
    setDeleting(true);
    try {
      await api.deleteSkill(skill.id);
      navigation.replace(paths.skills());
      qc.removeQueries({
        queryKey: skillDetailOptions(wsId, skill.id).queryKey,
      });
      qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      toast.success(t(($) => $.detail.toast_deleted));
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : t(($) => $.detail.toast_delete_failed),
      );
      setDeleting(false);
      setConfirmDelete(false);
    }
  };

  const handleCopyId = async () => {
    if (!skill) return;
    try {
      await navigator.clipboard.writeText(skill.id);
      toast.success(t(($) => $.detail.menu.copied_toast));
    } catch {
      toast.error(t(($) => $.detail.menu.copy_failed_toast));
    }
  };

  const handleAddFile = (path: string) => {
    setFiles((prev) => [...prev, { path, content: "" }]);
    setSelectedPath(path);
    setAddingFile(false);
  };

  const handleDeleteFile = () => {
    if (selectedPath === SKILL_MD) return;
    setFiles((prev) => prev.filter((f) => f.path !== selectedPath));
    setSelectedPath(SKILL_MD);
  };

  const handleFileContentChange = (newContent: string) => {
    if (!canEdit) return;
    if (selectedPath === SKILL_MD) {
      setContent(newContent);
    } else {
      setFiles((prev) =>
        prev.map((f) =>
          f.path === selectedPath ? { ...f, content: newContent } : f,
        ),
      );
    }
  };

  // Tab state lives in the URL (like settings) so links land on a specific
  // tab and switches survive reload; replace keeps history clean.
  const rawTab = navigation.searchParams.get(TAB_QUERY_KEY);
  const activeTab: DetailTab = rawTab === "files" ? "files" : "overview";
  const handleTabChange = (next: string) => {
    const params = new URLSearchParams(navigation.searchParams);
    params.set(TAB_QUERY_KEY, next);
    navigation.replace(`${navigation.pathname}?${params.toString()}`);
  };

  const handleEditContent = () => {
    setSelectedPath(SKILL_MD);
    handleTabChange("files");
  };

  const supportingQueryDown =
    !!agentsError || !!membersError || !!runtimesError;

  if (isLoading) {
    return (
      <div className="flex flex-1 min-h-0 flex-col">
        <div className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
          <Skeleton className="h-4 w-16" />
          <Skeleton className="h-3 w-3 rounded" />
          <Skeleton className="h-4 w-40" />
        </div>
        <div className="space-y-3 p-6">
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-5/6" />
          <Skeleton className="h-4 w-3/4" />
        </div>
      </div>
    );
  }

  if (error || !skill) {
    return (
      <div className="flex flex-1 min-h-0 flex-col">
        <div className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
          <Button
            variant="ghost"
            size="xs"
            render={<AppLink href={paths.skills()} />}
            nativeButton={false}
          >
            <ArrowLeft className="h-3 w-3" />
            {t(($) => $.detail.all_skills)}
          </Button>
        </div>
        <div className="flex flex-1 flex-col items-center justify-center gap-2 text-center">
          <AlertCircle className="h-8 w-8 text-muted-foreground/40" />
          <p className="text-sm font-medium">{t(($) => $.detail.not_found.title)}</p>
          <p className="max-w-xs text-xs text-muted-foreground">
            {error instanceof Error ? error.message : t(($) => $.detail.not_found.fallback)}
          </p>
          <AppLink
            href={paths.skills()}
            className={`${buttonVariants({ variant: "outline", size: "xs" })} mt-2`}
          >
            {t(($) => $.detail.not_found.back)}
          </AppLink>
        </div>
      </div>
    );
  }

  // --- Source row content ---
  const originLabel = (() => {
    if (!origin) return null;
    if (origin.type === "runtime_local") {
      return originRuntime
        ? t(($) => $.detail.subline.origin_runtime_named, { name: originRuntime.name })
        : origin.provider
          ? t(($) => $.detail.subline.origin_runtime_provider, { provider: origin.provider })
          : t(($) => $.detail.subline.origin_runtime_unknown);
    }
    if (origin.type === "clawhub") return t(($) => $.detail.subline.origin_clawhub);
    if (origin.type === "skills_sh") return t(($) => $.detail.subline.origin_skills_sh);
    if (origin.type === "github") return t(($) => $.detail.subline.origin_github);
    return t(($) => $.detail.subline.origin_workspace);
  })();
  const originDetail = origin?.source_url ?? origin?.source_path ?? null;

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <BreadcrumbHeader
        segments={[{ href: paths.skills(), label: t(($) => $.page.title) }]}
        leaf={
          <span className="truncate font-mono text-xs text-foreground">
            {skill.name}
          </span>
        }
        actions={
          <>
            {!canEdit && (
              <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                <Lock className="h-3 w-3" />
                {t(($) => $.detail.read_only)}
              </span>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="text-muted-foreground"
                    aria-label={t(($) => $.detail.menu.aria)}
                  >
                    <MoreHorizontal className="h-3.5 w-3.5" />
                  </Button>
                }
              />
              <DropdownMenuContent align="end" className="w-48">
                <DropdownMenuItem onClick={handleCopyId}>
                  <Copy className="size-3.5" />
                  {t(($) => $.detail.menu.copy_id)}
                </DropdownMenuItem>
                {canEdit && (
                  <>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem
                      variant="destructive"
                      onClick={() => setConfirmDelete(true)}
                    >
                      <Trash2 className="size-3.5" />
                      {t(($) => $.actions.delete)}
                    </DropdownMenuItem>
                  </>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
      />

      {!canEdit && (
        <div className="px-4 pt-3">
          <CapabilityBanner
            reason={skillPermissions.canEdit.reason}
            resource="skill"
            ownerName={creator?.name}
          />
        </div>
      )}

      {supportingQueryDown && (
        <div
          role="status"
          className="flex shrink-0 items-start gap-2 border-b bg-warning/10 px-4 py-2 text-xs text-muted-foreground"
        >
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning" />
          <span>{t(($) => $.detail.supporting_data_warning)}</span>
        </div>
      )}

      {/* Conflict banner (drafts span both tabs, so it sits at page level) */}
      {conflictPending && canEdit && (
        <div
          role="status"
          aria-live="polite"
          className="flex shrink-0 items-start gap-2 border-b border-warning/30 bg-warning/10 px-4 py-2 text-xs"
        >
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning" />
          <div className="flex-1">
            <div className="font-medium text-foreground">
              {t(($) => $.detail.conflict_banner.title)}
            </div>
            <div className="mt-0.5 text-muted-foreground">
              {t(($) => $.detail.conflict_banner.body)}
            </div>
          </div>
        </div>
      )}

      <Tabs
        value={activeTab}
        onValueChange={(value) => handleTabChange(String(value))}
        className="flex flex-1 min-h-0 flex-col gap-0"
      >
        <div className="shrink-0 px-4 pt-2 sm:px-5">
          <TabsList variant="line" className="h-8 gap-5 p-0">
            <TabsTrigger value="overview" className="flex-none px-0 text-xs">
              {t(($) => $.detail.tabs.overview)}
            </TabsTrigger>
            <TabsTrigger value="files" className="flex-none px-0 text-xs">
              {t(($) => $.detail.tabs.files)}
              <span className="text-muted-foreground/70">
                {filePaths.length}
              </span>
            </TabsTrigger>
          </TabsList>
        </div>

        {/* Overview: main column (identity + rendered SKILL.md) with a
            right rail (metadata + used-by), like a marketplace detail page */}
        <TabsContent value="overview" className="min-h-0 overflow-y-auto">
          <div className="mx-auto flex w-full max-w-5xl flex-col gap-6 p-4 sm:p-6 lg:flex-row lg:gap-8">
            <div className="min-w-0 flex-1 space-y-6">
              {/* Identity */}
              <div className="flex items-start gap-3">
                <div className="flex size-10 shrink-0 items-center justify-center rounded-lg border bg-muted/50">
                  <Sparkles className="h-4 w-4 text-muted-foreground" />
                </div>
                <div className="min-w-0 flex-1">
                  <h2 className="truncate text-lg font-semibold leading-tight">
                    {name.trim() || skill.name}
                  </h2>
                  {description.trim() !== "" && (
                    <p className="mt-0.5 text-sm text-muted-foreground">
                      {description}
                    </p>
                  )}
                </div>
              </div>

              {/* Rendered SKILL.md */}
              <section>
                <SectionHeader
                  title={SKILL_MD}
                  action={
                    canEdit ? (
                      <Button
                        type="button"
                        variant="ghost"
                        size="xs"
                        onClick={handleEditContent}
                        className="text-muted-foreground"
                      >
                        {t(($) => $.detail.overview.edit)}
                      </Button>
                    ) : undefined
                  }
                />
                <div className="rounded-lg border bg-card p-4 sm:p-6">
                  {previewBody ? (
                    <Markdown mode="full">{previewBody}</Markdown>
                  ) : (
                    <p className="text-sm text-muted-foreground">
                      {t(($) => $.detail.overview.preview_empty)}
                    </p>
                  )}
                </div>
              </section>
            </div>

            <aside className="w-full shrink-0 space-y-7 lg:w-72">
              {/* Metadata */}
              <section>
                <SectionHeader
                  title={t(($) => $.detail.overview.metadata)}
                  action={
                    canEdit ? (
                      <Button
                        type="button"
                        variant="ghost"
                        size="xs"
                        onClick={() => setEditingMeta((v) => !v)}
                        className="text-muted-foreground"
                      >
                        {editingMeta
                          ? t(($) => $.detail.overview.done)
                          : t(($) => $.detail.overview.edit)}
                      </Button>
                    ) : undefined
                  }
                />
                <div className="rounded-lg border bg-card">
                  {editingMeta && canEdit && (
                    <div className="space-y-3 border-b px-4 py-3">
                      <div className="space-y-1">
                        <Label
                          htmlFor="skill-name"
                          className="text-xs text-muted-foreground"
                        >
                          {t(($) => $.detail.overview.name_label)}
                        </Label>
                        <Input
                          id="skill-name"
                          value={name}
                          onChange={(e) => setName(e.target.value)}
                          placeholder={t(($) => $.detail.name_placeholder)}
                          className="h-8 font-mono text-sm"
                        />
                      </div>
                      <div className="space-y-1">
                        <Label
                          htmlFor="skill-description"
                          className="text-xs text-muted-foreground"
                        >
                          {t(($) => $.detail.description_label)}
                        </Label>
                        <Textarea
                          id="skill-description"
                          value={description}
                          onChange={(e) => setDescription(e.target.value)}
                          placeholder={t(($) => $.detail.description_placeholder)}
                          rows={2}
                          className="resize-none text-sm"
                        />
                      </div>
                    </div>
                  )}
                  <dl className="divide-y">
                    {originLabel && (
                      <MetaRow label={t(($) => $.detail.overview.origin)}>
                        <span className="inline-flex items-center gap-1.5">
                          {origin?.type === "runtime_local" ? (
                            <HardDrive className="h-3 w-3 shrink-0 text-muted-foreground" />
                          ) : (
                            <Sparkles className="h-3 w-3 shrink-0 text-muted-foreground" />
                          )}
                          {originLabel}
                        </span>
                        {originDetail && (
                          <div className="mt-0.5 break-all font-mono text-xs text-muted-foreground">
                            {originDetail}
                          </div>
                        )}
                      </MetaRow>
                    )}
                    {creator && (
                      <MetaRow label={t(($) => $.detail.overview.creator)}>
                        <span className="inline-flex items-center gap-1.5">
                          <ActorAvatar
                            name={creator.name}
                            initials={creator.name.slice(0, 2).toUpperCase()}
                            avatarUrl={resolvePublicFileUrl(creator.avatar_url)}
                            size={18}
                          />
                          {creator.name}
                        </span>
                      </MetaRow>
                    )}
                    <MetaRow label={t(($) => $.detail.overview.updated)}>
                      {timeAgo(skill.updated_at)}
                    </MetaRow>
                  </dl>
                </div>
              </section>

              {/* Used by */}
              <UsedByPanel skill={skill} agents={skillAgents} ctx={actionsCtx} />
            </aside>
          </div>
        </TabsContent>

        {/* Files: tree + editor */}
        <TabsContent
          value="files"
          className="mt-2 flex min-h-0 flex-col overflow-y-auto border-t md:flex-row md:overflow-hidden"
        >
          <aside className="flex max-h-44 w-full shrink-0 flex-col border-b md:max-h-none md:w-56 md:border-b-0 md:border-r">
            <div className="flex h-10 shrink-0 items-center justify-between border-b px-3">
              <span className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
                {t(($) => $.detail.files_label, { count: totalFileCount(skill) })}
              </span>
              {canEdit && (
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => setAddingFile(true)}
                        className="text-muted-foreground"
                        aria-label={t(($) => $.detail.add_file_aria)}
                      >
                        <Plus className="h-3.5 w-3.5" />
                      </Button>
                    }
                  />
                  <TooltipContent>{t(($) => $.detail.add_file_tooltip)}</TooltipContent>
                </Tooltip>
              )}
            </div>
            {addingFile && (
              <AddFileInline
                existingPaths={filePaths}
                onAdd={handleAddFile}
                onCancel={() => setAddingFile(false)}
              />
            )}
            <div className="flex-1 overflow-y-auto">
              <FileTree
                filePaths={filePaths}
                selectedPath={selectedPath}
                onSelect={setSelectedPath}
              />
            </div>
            {selectedPath !== SKILL_MD && canEdit && (
              <div className="border-t px-3 py-2">
                <Button
                  type="button"
                  variant="ghost"
                  size="xs"
                  onClick={handleDeleteFile}
                  className="text-muted-foreground hover:text-destructive"
                >
                  <Trash2 className="h-3 w-3" />
                  {t(($) => $.detail.delete_file)}
                </Button>
              </div>
            )}
          </aside>

          <section className="flex min-h-[28rem] min-w-0 shrink-0 flex-col md:min-h-0 md:flex-1 md:shrink">
            <div className="flex-1 min-h-0">
              <FileViewer
                key={selectedPath}
                path={selectedPath}
                content={selectedContent}
                onChange={handleFileContentChange}
              />
            </div>
          </section>
        </TabsContent>
      </Tabs>

      {/* Save bar: page-level — metadata edits live in the Overview tab and
          file edits in the Files tab, so dirty state must show on both. */}
      {isDirty && canEdit && (
        <div
          role="status"
          aria-live="polite"
          className="flex shrink-0 flex-wrap items-center gap-2 border-t bg-muted/30 px-4 py-2"
        >
          <span className="h-1.5 w-1.5 rounded-full bg-brand" />
          <span className="text-xs text-muted-foreground">
            {t(($) => $.detail.save_bar.unsaved)}
          </span>
          <div className="ml-auto flex items-center gap-1.5">
            <Button
              type="button"
              variant="ghost"
              size="xs"
              onClick={handleDiscard}
            >
              {t(($) => $.detail.save_bar.discard)}
            </Button>
            <Button
              type="button"
              size="xs"
              onClick={handleSave}
              disabled={saving || !name.trim()}
            >
              {saving ? (
                <>
                  <Loader2 className="h-3 w-3 animate-spin" />
                  {t(($) => $.detail.save_bar.saving)}
                </>
              ) : (
                <>
                  <Save className="h-3 w-3" />
                  {t(($) => $.detail.save_bar.save)}
                </>
              )}
            </Button>
          </div>
        </div>
      )}

      {/* Delete confirmation */}
      <Dialog
        open={confirmDelete}
        onOpenChange={(open) => {
          if (!deleting) setConfirmDelete(open);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t(($) => $.detail.delete_dialog.title)}</DialogTitle>
            <DialogDescription>
              {skillAgents.length > 0
                ? t(($) => $.detail.delete_dialog.description_with_agents, {
                    name: skill.name,
                    count: skillAgents.length,
                  })
                : t(($) => $.detail.delete_dialog.description_no_agents, {
                    name: skill.name,
                  })}
            </DialogDescription>
          </DialogHeader>
          <div className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
            {t(($) => $.detail.delete_dialog.warning)}
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={() => setConfirmDelete(false)}
              disabled={deleting}
            >
              {t(($) => $.detail.delete_dialog.cancel)}
            </Button>
            <Button
              type="button"
              variant="destructive"
              onClick={handleDelete}
              disabled={deleting}
            >
              {deleting ? (
                <>
                  <Loader2 className="h-3 w-3 animate-spin" />
                  {t(($) => $.detail.delete_dialog.deleting)}
                </>
              ) : (
                <>
                  <Trash2 className="h-3 w-3" />
                  {t(($) => $.detail.delete_dialog.confirm)}
                </>
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
