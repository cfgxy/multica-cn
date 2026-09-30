"use client";

import { useCallback, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import type { SkillCatalogEntry } from "@multica/core/types";
import { workspaceKeys } from "@multica/core/workspace/queries";

const IMPORT_POLL_INTERVAL_MS = 1500;
const IMPORT_POLL_DEADLINE_MS = 30_000;

/**
 * Brings a runtime-discovered catalog entry into the workspace through the
 * existing runtime-local import flow (RUYI-288): enqueue, poll to a terminal
 * status, then refresh the catalog and the authored skill list. The Skill Tab
 * and the agent skill dialog share this so "import" behaves identically on
 * both surfaces. The imported skill becomes a real authored row with a
 * version trail — the catalog never fabricates one.
 */
export function useCatalogSkillImport(wsId: string) {
  const qc = useQueryClient();
  const [importingKey, setImportingKey] = useState<string | null>(null);

  const importSkill = useCallback(
    async (entry: Pick<SkillCatalogEntry, "key" | "runtime_id">) => {
      if (!entry.runtime_id || !entry.key) return;
      setImportingKey(entry.key);
      try {
        let req = await api.initiateImportLocalSkill(entry.runtime_id, {
          skill_key: entry.key,
        });
        const deadline = Date.now() + IMPORT_POLL_DEADLINE_MS;
        while (req.status !== "completed" && Date.now() < deadline) {
          if (req.status === "failed" || req.status === "timeout") {
            throw new Error(req.error ?? "import failed");
          }
          await new Promise((resolve) => setTimeout(resolve, IMPORT_POLL_INTERVAL_MS));
          req = await api.getImportLocalSkillResult(entry.runtime_id, req.id);
        }
        if (req.status !== "completed") throw new Error("import timed out");
        qc.invalidateQueries({ queryKey: workspaceKeys.skillCatalog(wsId) });
        qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
      } finally {
        setImportingKey(null);
      }
    },
    [qc, wsId],
  );

  return { importSkill, importingKey };
}
