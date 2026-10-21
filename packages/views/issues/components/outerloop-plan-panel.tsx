"use client";

import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Play, Square } from "lucide-react";
import { api } from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { cn } from "@multica/ui/lib/utils";

type Plan = { state: string; goal: string; max_rounds: number; round: number };
type PlanResponse = { enabled: boolean; has_plan: boolean; plan: Plan };
type OuterLoopApi = { getOuterLoopPlan(id: string): Promise<PlanResponse>; updateOuterLoopPlan(id: string, data: { enabled?: boolean; max_rounds?: number; goal?: string }): Promise<PlanResponse>; fireOuterLoopPlan(id: string): Promise<unknown>; stopOuterLoopPlan(id: string): Promise<unknown> };
const outerLoopApi = api as unknown as OuterLoopApi;

export function OuterLoopPlanPanel({ issueId }: { issueId: string }) {
  const client = useQueryClient();
  const [open, setOpen] = useState(true);
  const [preview, setPreview] = useState(false);
  const [goal, setGoal] = useState("");
  const [maxRounds, setMaxRounds] = useState(20);
  const query = useQuery({ queryKey: ["outerloop-plan", issueId], queryFn: () => outerLoopApi.getOuterLoopPlan(issueId) });
  useEffect(() => { if (query.data?.has_plan) { setGoal(query.data.plan.goal); setMaxRounds(query.data.plan.max_rounds); } }, [query.data]);
  const refresh = () => client.invalidateQueries({ queryKey: ["outerloop-plan", issueId] });
  const save = useMutation({ mutationFn: () => outerLoopApi.updateOuterLoopPlan(issueId, { enabled: true, goal, max_rounds: maxRounds }), onSuccess: () => { setPreview(false); refresh(); } });
  const fire = useMutation({ mutationFn: () => outerLoopApi.fireOuterLoopPlan(issueId), onSuccess: refresh });
  const stop = useMutation({ mutationFn: () => outerLoopApi.stopOuterLoopPlan(issueId), onSuccess: refresh });
  const plan = query.data?.plan;
  return <div><button type="button" onClick={() => setOpen(!open)} className={cn("mb-2 flex w-full items-center gap-1 rounded-md px-2 py-1 text-caption font-medium hover:bg-accent/70", !open && "text-muted-foreground")}>连续续跑计划 <ChevronRight className={cn("!size-3 transition-transform", open && "rotate-90")} /></button>{open ? <div className="space-y-2 pl-2"><p className="text-caption text-muted-foreground">{query.data?.has_plan ? `第 ${plan?.round ?? 0}/${plan?.max_rounds ?? 20} 轮 · ${plan?.state ?? ""}` : "尚未开启"}</p><Input value={goal} onChange={(e) => setGoal(e.target.value)} placeholder="续跑目标" /><Input type="number" min={1} max={1000} value={maxRounds} onChange={(e) => setMaxRounds(Number(e.target.value))} /><div className="flex gap-1"><Button size="sm" onClick={() => setPreview(true)}>预览并保存</Button><Button size="icon-sm" variant="ghost" aria-label="手动续跑" onClick={() => fire.mutate()}><Play /></Button><Button size="icon-sm" variant="ghost" aria-label="停止计划" onClick={() => stop.mutate()}><Square /></Button></div></div> : null}<Dialog open={preview} onOpenChange={setPreview}><DialogContent><DialogHeader><DialogTitle>确认续跑计划</DialogTitle></DialogHeader><p>目标：{goal || "未设置"}<br />最大轮次：{maxRounds}</p><DialogFooter><Button variant="ghost" onClick={() => setPreview(false)}>取消</Button><Button disabled={save.isPending} onClick={() => save.mutate()}>确认保存</Button></DialogFooter></DialogContent></Dialog></div>;
}
