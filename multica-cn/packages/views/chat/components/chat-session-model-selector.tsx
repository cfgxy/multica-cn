"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Cpu, Loader2, RotateCw } from "lucide-react";
import { api, ApiError } from "@multica/core/api";
import { runtimeModelsOptions } from "@multica/core/runtimes";
import { chatKeys } from "@multica/core/chat/queries";
import type { ChatSession } from "@multica/core/types";
import { DropdownMenuItem, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger } from "@multica/ui/components/ui/dropdown-menu";
import { toast } from "sonner";
import { useT } from "../../i18n";

export function ChatSessionModelSubmenu({
  session,
  wsId,
  runtimeId,
  agentModel,
  disabled,
}: {
  session: ChatSession;
  wsId: string;
  runtimeId: string | null;
  agentModel: string | null;
  disabled: boolean;
}) {
  const { t } = useT("chat");
  const { t: tIssues } = useT("issues");
  const qc = useQueryClient();
  const catalog = useQuery(runtimeModelsOptions(runtimeId));
  const save = useMutation({
    mutationFn: (model: string | null) => api.setChatSessionModel(session.id, model),
    onSuccess: (updated) => {
      qc.setQueryData<ChatSession[]>(chatKeys.sessions(wsId), (old) =>
        old?.map((item) => item.id === updated.id ? { ...item, model_override: updated.model_override ?? null } : item),
      );
      qc.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
    },
    onError: (error) => {
      toast.error(error instanceof ApiError && error.status === 409
        ? t(($) => $.mobile.model.unavailable)
        : t(($) => $.mobile.model.save_failed));
    },
  });
  const selected = session.model_override ?? null;
  const label = selected ?? agentModel ?? t(($) => $.mobile.model.agent_default);

  return (
      <DropdownMenuSub>
        <DropdownMenuSubTrigger disabled={disabled} className="h-16 flex-col justify-center gap-1 rounded-md px-1 py-2 text-caption text-muted-foreground data-popup-open:bg-accent data-popup-open:text-accent-foreground [&>svg:last-child]:hidden">
          {save.isPending ? <Loader2 className="size-5 animate-spin" aria-hidden="true" /> : <Cpu className="size-5" aria-hidden="true" />}
          <span className="max-w-full truncate" title={label}>{t(($) => $.mobile.model.current, { model: label })}</span>
        </DropdownMenuSubTrigger>
        <DropdownMenuSubContent className="max-h-[min(60vh,var(--available-height))] min-w-56 max-w-[min(24rem,var(--available-width))] overflow-y-auto rounded-lg border border-surface-border bg-surface-raised p-1 shadow-[var(--menu-shadow)] ring-1 ring-surface-border origin-bottom data-[side=top]:slide-in-from-bottom-1 data-open:zoom-in-100">
        <DropdownMenuItem disabled={save.isPending} onClick={() => save.mutate(null)} className="min-h-9 rounded-md px-2">
          <span className="min-w-0 flex-1">{t(($) => $.mobile.model.agent_default)}</span>
          {selected === null ? <Check aria-hidden="true" /> : null}
        </DropdownMenuItem>
        {catalog.data?.supported ? catalog.data.models.map((model) => (
          <DropdownMenuItem key={model.id} disabled={save.isPending} onClick={() => save.mutate(model.id)} className="min-h-9 rounded-md px-2 data-[highlighted]:bg-accent data-[highlighted]:text-accent-foreground">
            <span className="min-w-0 flex-1 truncate">{model.label || model.id}</span>
            {selected === model.id ? <Check aria-hidden="true" /> : null}
          </DropdownMenuItem>
        )) : null}
        {catalog.isLoading ? <DropdownMenuItem disabled>{t(($) => $.mobile.model.loading)}</DropdownMenuItem> : null}
        {catalog.isError ? <DropdownMenuItem disabled>{t(($) => $.mobile.model.load_failed)}</DropdownMenuItem> : null}
        {catalog.isError ? <DropdownMenuItem onClick={() => void catalog.refetch()}><RotateCw aria-hidden="true" />{tIssues(($) => $.status_catalog_error.retry)}</DropdownMenuItem> : null}
        {catalog.data?.supported === false ? <DropdownMenuItem disabled>{t(($) => $.mobile.model.unsupported)}</DropdownMenuItem> : null}
        {catalog.data?.supported && catalog.data.models.length === 0 ? <DropdownMenuItem disabled>{t(($) => $.mobile.model.unsupported)}</DropdownMenuItem> : null}
        </DropdownMenuSubContent>
      </DropdownMenuSub>
  );
}
