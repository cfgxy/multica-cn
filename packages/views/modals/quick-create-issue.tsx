"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronRight, Sparkles, X as XIcon } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { cn } from "@multica/ui/lib/utils";
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@multica/ui/components/ui/tooltip";
import { Button } from "@multica/ui/components/ui/button";
import { api, ApiError } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentWorkspace } from "@multica/core/paths";
import { agentListOptions } from "@multica/core/workspace/queries";
import { useQuickCreateStore } from "@multica/core/issues/stores/quick-create-store";
import { useIssueDraftStore } from "@multica/core/issues/stores/draft-store";
import { useModalStore } from "@multica/core/modals";
import type { Agent } from "@multica/core/types";
import { ActorAvatar } from "../common/actor-avatar";
import { canAssignAgent } from "../issues/components/pickers/assignee-picker";
import { useAuthStore } from "@multica/core/auth";
import { memberListOptions } from "@multica/core/workspace/queries";

// QuickCreateIssueModal — a streamlined create-issue UI: pick an agent, type
// one line, submit. The agent translates the line into a `multica issue
// create` call asynchronously; the modal closes immediately and the user is
// notified via inbox when the agent finishes.
export function QuickCreateIssueModal({
  onClose,
  data,
}: {
  onClose: () => void;
  data?: Record<string, unknown> | null;
}) {
  const workspaceName = useCurrentWorkspace()?.name;
  const wsId = useWorkspaceId();
  const userId = useAuthStore((s) => s.user?.id);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));

  const memberRole = useMemo(
    () => members.find((m) => m.user_id === userId)?.role,
    [members, userId],
  );

  // Visible = not archived AND assignable by this user.
  const visibleAgents = useMemo(
    () =>
      agents.filter(
        (a) => !a.archived_at && canAssignAgent(a, userId, memberRole),
      ),
    [agents, userId, memberRole],
  );

  const lastAgentId = useQuickCreateStore((s) => s.lastAgentId);
  const setLastAgentId = useQuickCreateStore((s) => s.setLastAgentId);

  const [agentId, setAgentId] = useState<string | undefined>(() => {
    const seed = (data?.agent_id as string) || lastAgentId || undefined;
    if (seed && visibleAgents.some((a) => a.id === seed)) return seed;
    return visibleAgents[0]?.id;
  });

  // Re-seed once visible list resolves (queries may be empty on first render).
  useEffect(() => {
    if (agentId && visibleAgents.some((a) => a.id === agentId)) return;
    const seed = (data?.agent_id as string) || lastAgentId || undefined;
    if (seed && visibleAgents.some((a) => a.id === seed)) {
      setAgentId(seed);
      return;
    }
    const first = visibleAgents[0];
    if (first) setAgentId(first.id);
  }, [visibleAgents, agentId, data?.agent_id, lastAgentId]);

  const selectedAgent = useMemo(
    () => visibleAgents.find((a) => a.id === agentId),
    [visibleAgents, agentId],
  );

  const initialPrompt = (data?.prompt as string) || "";
  const [prompt, setPrompt] = useState(initialPrompt);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    textareaRef.current?.focus();
  }, []);

  const submit = async () => {
    if (!prompt.trim() || !agentId || submitting) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.quickCreateIssue({ agent_id: agentId, prompt: prompt.trim() });
      setLastAgentId(agentId);
      toast.success("Sent to agent — you'll get an inbox notification when it's done", {
        duration: 4000,
      });
      onClose();
    } catch (e) {
      // Server returns 422 with { code: "agent_unavailable", reason } when the
      // picked agent's runtime is offline. Surface the reason in-modal so the
      // user can switch to a live agent without leaving the flow.
      if (e instanceof ApiError && e.body && typeof e.body === "object") {
        const body = e.body as { code?: string; reason?: string };
        if (body.code === "agent_unavailable") {
          setError(body.reason || "Agent is unavailable. Pick another agent.");
          setSubmitting(false);
          return;
        }
      }
      setError("Failed to submit. Try again.");
    } finally {
      setSubmitting(false);
    }
  };

  // Switch to the legacy advanced form, carrying the prompt over as the
  // description so the user doesn't lose what they typed. The picked agent
  // becomes the default assignee candidate (still editable). We seed the
  // shared issue-draft store directly because the legacy modal reads its
  // initial values from there rather than from `data`.
  const switchToAdvanced = () => {
    useIssueDraftStore.getState().setDraft({
      description: prompt,
      ...(agentId
        ? { assigneeType: "agent" as const, assigneeId: agentId }
        : {}),
    });
    useModalStore.getState().open("create-issue");
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      submit();
    }
  };

  return (
    <Dialog open onOpenChange={(v) => { if (!v) onClose(); }}>
      <DialogContent
        finalFocus={false}
        showCloseButton={false}
        className={cn(
          "p-0 gap-0 flex flex-col overflow-hidden",
          "!top-1/2 !left-1/2 !-translate-x-1/2 !-translate-y-1/2",
          "!max-w-xl !w-full",
        )}
      >
        <DialogTitle className="sr-only">Quick create issue</DialogTitle>

        {/* Header */}
        <div className="flex items-center justify-between px-5 pt-3 pb-2 shrink-0">
          <div className="flex items-center gap-1.5 text-xs">
            <span className="text-muted-foreground">{workspaceName}</span>
            <ChevronRight className="size-3 text-muted-foreground/50" />
            <span className="font-medium">Quick create</span>
          </div>
          <div className="flex items-center gap-1">
            <Tooltip>
              <TooltipTrigger
                render={
                  <button
                    onClick={switchToAdvanced}
                    className="text-xs px-2 py-1 rounded-sm opacity-70 hover:opacity-100 hover:bg-accent/60 transition-all cursor-pointer"
                  >
                    Advanced
                  </button>
                }
              />
              <TooltipContent side="bottom">
                Open the full form with all fields
              </TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger
                render={
                  <button
                    onClick={onClose}
                    className="rounded-sm p-1.5 opacity-70 hover:opacity-100 hover:bg-accent/60 transition-all cursor-pointer"
                  >
                    <XIcon className="size-4" />
                  </button>
                }
              />
              <TooltipContent side="bottom">Close</TooltipContent>
            </Tooltip>
          </div>
        </div>

        {/* Agent picker */}
        <div className="px-5 pt-1 pb-2 shrink-0">
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <button
                  type="button"
                  className="flex items-center gap-2 text-xs text-muted-foreground hover:text-foreground transition-colors cursor-pointer rounded-sm px-1.5 py-1 -ml-1.5 hover:bg-accent/60"
                >
                  <Sparkles className="size-3.5" />
                  <span>Created by</span>
                  {selectedAgent ? (
                    <span className="flex items-center gap-1.5 text-foreground">
                      <ActorAvatar
                        actorType="agent"
                        actorId={selectedAgent.id}
                        size={16}
                        disableHoverCard
                      />
                      {selectedAgent.name}
                    </span>
                  ) : (
                    <span>Pick an agent…</span>
                  )}
                </button>
              }
            />
            <DropdownMenuContent align="start" className="w-64 max-h-72 overflow-y-auto">
              {visibleAgents.length === 0 ? (
                <div className="px-2 py-1.5 text-xs text-muted-foreground">
                  No agents available.
                </div>
              ) : (
                visibleAgents.map((a: Agent) => (
                  <DropdownMenuItem
                    key={a.id}
                    onClick={() => {
                      setAgentId(a.id);
                      setError(null);
                    }}
                    className="flex items-center gap-2"
                  >
                    <ActorAvatar
                      actorType="agent"
                      actorId={a.id}
                      size={16}
                      disableHoverCard
                    />
                    <span className="flex-1 truncate">{a.name}</span>
                  </DropdownMenuItem>
                ))
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        {/* Prompt textarea */}
        <div className="px-5 pb-3">
          <textarea
            ref={textareaRef}
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder='Describe the issue, e.g. "fix inbox loading slowness, assign to naiyuan, P1"'
            rows={5}
            className="w-full resize-none bg-transparent text-sm placeholder:text-muted-foreground focus:outline-none"
          />
        </div>

        {error && (
          <div className="px-5 pb-2 text-xs text-destructive">{error}</div>
        )}

        {/* Footer */}
        <div className="flex items-center justify-between px-4 py-3 border-t shrink-0">
          <span className="text-xs text-muted-foreground">⌘↵ to submit</span>
          <Button
            size="sm"
            onClick={submit}
            disabled={!prompt.trim() || !agentId || submitting}
          >
            {submitting ? "Sending…" : "Create"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
