"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, Cpu, Loader2 } from "lucide-react";
import { api, ApiError } from "@multica/core/api";
import { runtimeModelsOptions } from "@multica/core/runtimes";
import { chatKeys } from "@multica/core/chat/queries";
import type { ChatSession } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@multica/ui/components/ui/dropdown-menu";
import { toast } from "sonner";
import { useT } from "../../i18n";

export function ChatSessionModelSelector({
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
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="outline" size="sm" disabled={disabled || save.isPending} /> }>
        {save.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Cpu aria-hidden="true" />}
        <span className="max-w-44 truncate">{label}</span>
        <ChevronDown aria-hidden="true" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 w-72 overflow-y-auto">
        <DropdownMenuItem disabled={save.isPending} onClick={() => save.mutate(null)}>
          <span className="min-w-0 flex-1">{t(($) => $.mobile.model.agent_default)}</span>
          {selected === null ? <Check aria-hidden="true" /> : null}
        </DropdownMenuItem>
        {catalog.data?.supported ? catalog.data.models.map((model) => (
          <DropdownMenuItem key={model.id} disabled={save.isPending} onClick={() => save.mutate(model.id)}>
            <span className="min-w-0 flex-1 truncate">{model.label || model.id}</span>
            {selected === model.id ? <Check aria-hidden="true" /> : null}
          </DropdownMenuItem>
        )) : null}
        {catalog.isLoading ? <DropdownMenuItem disabled>{t(($) => $.mobile.model.loading)}</DropdownMenuItem> : null}
        {catalog.isError ? <DropdownMenuItem disabled>{t(($) => $.mobile.model.load_failed)}</DropdownMenuItem> : null}
        {catalog.data?.supported === false ? <DropdownMenuItem disabled>{t(($) => $.mobile.model.unsupported)}</DropdownMenuItem> : null}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
