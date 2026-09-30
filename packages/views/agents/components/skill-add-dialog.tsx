"use client";

import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type { Agent, SkillCatalogEntry, SkillSummary } from "@multica/core/types";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  skillCatalogOptions,
  skillListOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { useCatalogSkillImport } from "../../skills/lib/use-catalog-skill-import";
import { SkillPickerList } from "./skill-picker-list";

/**
 * "Attach workspace skills to this agent." Multi-select with explicit
 * Confirm — earlier iterations attached on a single row click, which
 * meant the user couldn't tick several skills at once and the dialog
 * closed before they could review their choice.
 *
 * Already-attached skills are filtered out of the list entirely (vs.
 * showing them disabled). When there are no remaining workspace skills
 * to attach, the empty-state copy explains why, and the Confirm button
 * is naturally disabled because nothing can be selected.
 *
 * RUYI-288: the list is the workspace skill catalog's authored half —
 * every real workspace skill is selectable regardless of origin (patent/
 * pattern packs are ordinary workspace rows; runtime-imported and plugin
 * rows carry a source badge). Discovery sightings that are not imported
 * yet render below as explicitly unattachable, with the import action
 * that brings them in — so the dialog explains why each entry is or
 * isn't selectable instead of silently omitting half the world.
 */
export function SkillAddDialog({
  agent,
  open,
  onOpenChange,
}: {
  agent: Agent;
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const { data: workspaceSkills = [], isLoading } = useQuery(skillListOptions(wsId));
  const catalog = useQuery(skillCatalogOptions(wsId));
  const { importSkill, importingKey } = useCatalogSkillImport(wsId);
  const [saving, setSaving] = useState(false);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

  const attachedIds = useMemo(
    () => new Set(agent.skills.map((s) => s.id)),
    [agent.skills],
  );
  // Hide attached skills outright — the dialog is for adding new ones.
  // If a user wants to see what's already on the agent, the SkillsTab
  // list above shows it.
  const availableSkills = useMemo(
    () => workspaceSkills.filter((s) => !attachedIds.has(s.id)),
    [workspaceSkills, attachedIds],
  );

  const sourceLabel = (source: string) => {
    switch (source) {
      case "runtime": return t(($) => $.tab_body.skills.source_runtime);
      case "plugin": return t(($) => $.tab_body.skills.source_plugin);
      default: return "";
    }
  };
  const sourceById = new Map<string, string>();
  for (const entry of catalog.data ?? []) {
    if (entry.kind !== "skill") continue;
    const label = sourceLabel(entry.source);
    if (label) sourceById.set(entry.id ?? "", label);
  }

  // Sightings that are not in the workspace yet. Entries whose name
  // collides with an authored skill (matching_skill_id) are already
  // represented in the selectable list, so they don't need an import row.
  const discoveries = useMemo(
    () => (catalog.data ?? []).filter(
      (e): e is SkillCatalogEntry & { runtime_id: string; key: string } =>
        e.kind === "discovery" && !e.matching_skill_id && !!e.runtime_id && !!e.key,
    ),
    [catalog.data],
  );

  const handleImport = async (entry: SkillCatalogEntry) => {
    try {
      await importSkill(entry);
      toast.success(t(($) => $.tab_body.skills.add_dialog_discovery_imported_toast));
    } catch {
      toast.error(t(($) => $.tab_body.skills.add_dialog_discovery_import_failed_toast));
    }
  };

  const handleOpenChange = (v: boolean) => {
    if (!v) setSelectedIds(new Set());
    onOpenChange(v);
  };

  const handleToggle = (skill: SkillSummary) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(skill.id)) next.delete(skill.id);
      else next.add(skill.id);
      return next;
    });
  };

  const handleConfirm = async () => {
    if (selectedIds.size === 0) return;
    setSaving(true);
    try {
      const newIds = [
        ...agent.skills.map((s) => s.id),
        ...selectedIds,
      ];
      await api.setAgentSkills(agent.id, { skill_ids: newIds });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      handleOpenChange(false);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.tab_body.skills.add_failed_toast));
    } finally {
      setSaving(false);
    }
  };

  const count = selectedIds.size;

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="text-body">
            {t(($) => $.tab_body.skills.add_dialog_title)}
          </DialogTitle>
          <DialogDescription className="text-caption">
            {t(($) => $.tab_body.skills.add_dialog_description)}
          </DialogDescription>
        </DialogHeader>

        <SkillPickerList
          skills={availableSkills}
          selectedIds={selectedIds}
          onToggle={handleToggle}
          sourceById={sourceById}
          loading={isLoading}
          emptyMessage={
            workspaceSkills.length === 0
              ? t(($) => $.tab_body.skills.add_dialog_empty)
              : t(($) => $.tab_body.skills.add_dialog_empty_partial)
          }
        />

        {discoveries.length > 0 && (
          <div className="space-y-1.5">
            <p className="text-caption font-medium">
              {t(($) => $.tab_body.skills.add_dialog_discoveries_title)}
            </p>
            <p className="text-caption text-muted-foreground">
              {t(($) => $.tab_body.skills.add_dialog_discovery_hint)}
            </p>
            <div className="divide-y rounded-lg border bg-card">
              {discoveries.map((entry) => (
                <div key={`${entry.runtime_id}:${entry.key}`} className="flex items-center gap-2.5 px-2.5 py-2">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-1.5">
                      <span className="truncate text-body font-medium">{entry.name}</span>
                      <Badge variant="outline" className="shrink-0 px-1.5 py-0 text-[10px] font-normal">
                        {t(($) => $.tab_body.skills.source_runtime)}
                      </Badge>
                    </div>
                    <div className="truncate text-caption text-muted-foreground">
                      {entry.description || entry.source_path}
                    </div>
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={importingKey !== null}
                    onClick={() => handleImport(entry)}
                  >
                    {importingKey === entry.key
                      ? t(($) => $.tab_body.skills.add_dialog_discovery_importing)
                      : t(($) => $.tab_body.skills.add_dialog_discovery_import)}
                  </Button>
                </div>
              ))}
            </div>
          </div>
        )}

        <DialogFooter>
          <Button variant="ghost" onClick={() => handleOpenChange(false)}>
            {t(($) => $.tab_body.skills.add_dialog_cancel)}
          </Button>
          <Button
            onClick={handleConfirm}
            disabled={count === 0 || saving}
          >
            {saving
              ? t(($) => $.tab_body.skills.add_dialog_saving)
              : count > 0
                ? t(($) => $.tab_body.skills.add_dialog_confirm, { count })
                : t(($) => $.tab_body.skills.add_dialog_confirm_default)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
