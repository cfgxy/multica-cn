"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Loader2, Play, Square } from "lucide-react";
import { useFeatureEnabled } from "@multica/core/config";
import { OUTER_LOOP_FLAG } from "@multica/core/feature-flags";
import {
  outerLoopPlanOptions,
  useFireOuterLoopPlan,
  useStopOuterLoopPlan,
  useUpdateOuterLoopPlan,
} from "@multica/core/issues/outerloop-queries";
import { useOuterLoopPlanDraftStore } from "@multica/core/issues/stores/outerloop-plan-draft-store";
import { useCurrentWorkspace } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { cn } from "@multica/ui/lib/utils";

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "操作未完成，请重试";
}

export function OuterLoopPlanPanel({ issueId }: { issueId: string }) {
  const wsId = useCurrentWorkspace()?.id ?? "";
	const featureEnabled = useFeatureEnabled(OUTER_LOOP_FLAG, false);
  const [open, setOpen] = useState(true);
  const [preview, setPreview] = useState(false);
  const draft = useOuterLoopPlanDraftStore((state) => state.drafts[issueId]);
  const setDraft = useOuterLoopPlanDraftStore((state) => state.setDraft);
  const clearDraft = useOuterLoopPlanDraftStore((state) => state.clearDraft);
  const query = useQuery({ ...outerLoopPlanOptions(wsId, issueId), enabled: featureEnabled && wsId !== "" });
  const save = useUpdateOuterLoopPlan(wsId, issueId);
  const fire = useFireOuterLoopPlan(wsId, issueId);
  const stop = useStopOuterLoopPlan(wsId, issueId);

  useEffect(() => {
    if (!draft && query.data) {
      setDraft(issueId, { goal: query.data.plan.goal, maxRounds: query.data.plan.max_rounds });
    }
  }, [draft, issueId, query.data, setDraft]);

  if (!featureEnabled) return null;

  const plan = query.data?.plan;
  const goal = draft?.goal ?? "";
  const maxRounds = draft?.maxRounds ?? plan?.max_rounds ?? 20;
  const canFire = query.data?.has_plan === true && plan?.state === "running" && !fire.isPending;
  const canStop = query.data?.has_plan === true && plan?.state !== "stopped" && !stop.isPending;
  const errors = [save.error, fire.error, stop.error].filter(Boolean).map(errorMessage);

  const updateDraft = (next: Partial<{ goal: string; maxRounds: number }>) => {
    setDraft(issueId, { goal, maxRounds, ...next });
  };

  const confirmSave = async () => {
    await save.mutateAsync({ enabled: true, goal, max_rounds: maxRounds });
    clearDraft(issueId);
    setPreview(false);
  };

  return (
    <div>
      <button type="button" onClick={() => setOpen(!open)} className={cn("mb-2 flex w-full items-center gap-1 rounded-md px-2 py-1 text-caption font-medium transition-colors hover:bg-accent/70", !open && "text-muted-foreground")}>
        连续续跑计划 <ChevronRight className={cn("!size-3 shrink-0 transition-transform", open && "rotate-90")} />
      </button>
      {open ? <div className="space-y-2 pl-2">
        {query.isLoading ? <p className="flex items-center gap-1 text-caption text-muted-foreground"><Loader2 className="size-3 animate-spin" />读取计划中</p> : null}
        {!query.isLoading ? <p className="text-caption text-muted-foreground">{query.data?.has_plan ? `第 ${plan?.round ?? 0}/${plan?.max_rounds ?? 20} 轮 · ${plan?.state ?? ""}` : "尚未开启"}</p> : null}
        {plan?.state === "waiting_human" && plan.wait_reason ? <p className="text-caption text-muted-foreground">等待原因：{plan.wait_reason}</p> : null}
        <label className="grid gap-1 text-caption" htmlFor={`outerloop-goal-${issueId}`}>续跑目标
          <Input id={`outerloop-goal-${issueId}`} value={goal} onChange={(event) => updateDraft({ goal: event.target.value })} placeholder="续跑目标" />
        </label>
        <label className="grid gap-1 text-caption" htmlFor={`outerloop-rounds-${issueId}`}>最大轮次
          <Input id={`outerloop-rounds-${issueId}`} type="number" min={1} max={1000} value={maxRounds} onChange={(event) => {
            const value = Number(event.target.value);
            if (Number.isInteger(value) && value >= 1 && value <= 1000) updateDraft({ maxRounds: value });
          }} />
        </label>
        <div className="flex gap-1">
          <Button size="sm" disabled={save.isPending} onClick={() => setPreview(true)}>预览并保存</Button>
          <Button size="icon-sm" variant="ghost" aria-label="手动续跑" disabled={!canFire} onClick={() => fire.mutate()}><Play /></Button>
          <Button size="icon-sm" variant="ghost" aria-label="停止计划" disabled={!canStop} onClick={() => stop.mutate()}><Square /></Button>
        </div>
        {fire.data?.dispatched === false && fire.data.reason === "pending_task" ? <p className="text-caption text-muted-foreground">已有待处理续跑任务</p> : null}
        {errors.map((message) => <p key={message} role="alert" className="text-caption text-destructive">{message}</p>)}
      </div> : null}
      <Dialog open={preview} onOpenChange={setPreview}>
        <DialogContent>
          <DialogHeader><DialogTitle>确认续跑计划</DialogTitle></DialogHeader>
          <p>目标：{goal || "未设置"}<br />最大轮次：{maxRounds}</p>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setPreview(false)}>取消</Button>
            <Button disabled={save.isPending} onClick={() => void confirmSave()}>{save.isPending ? "保存中" : "确认保存"}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
