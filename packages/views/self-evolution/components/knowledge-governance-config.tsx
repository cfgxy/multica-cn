"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Label } from "@multica/ui/components/ui/label";
import { Input } from "@multica/ui/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { clientErrorMessage } from "@multica/core/api";
import { useCurrentMember } from "@multica/core/permissions";
import { agentListOptions } from "@multica/core/workspace/queries";
import { knowledgeGovernanceConfigOptions, useUpdateKnowledgeGovernanceConfig } from "@multica/core/self-evolution";

/** Workspace-scoped controls for system-initiated knowledge governance. */
export function KnowledgeGovernanceConfig({ wsId }: { wsId: string }) {
  const member = useCurrentMember(wsId);
  const canManage = member.role === "owner" || member.role === "admin";
  const config = useQuery(knowledgeGovernanceConfigOptions(wsId));
  const agents = useQuery(agentListOptions(wsId));
  const save = useUpdateKnowledgeGovernanceConfig(wsId);
  const [enabled, setEnabled] = useState(false);
  const [agentId, setAgentId] = useState("");
  const [cadence, setCadence] = useState<"daily" | "weekly" | "monthly">("daily");
  const [timezone, setTimezone] = useState("UTC");

  useEffect(() => {
    if (!config.data) return;
    setEnabled(config.data.enabled);
    setAgentId(config.data.agent_id);
    setCadence(config.data.cadence);
    setTimezone(config.data.timezone);
  }, [config.data]);

  const agentChoices = (agents.data ?? []).filter((agent) => !agent.archived_at && agent.runtime_id);

  return (
    <section className="flex flex-col gap-3 rounded-md border p-4" data-testid="knowledge-governance-config">
      <div>
        <h2 className="text-title">知识治理调度</h2>
        <p className="text-muted-foreground text-caption">系统按独立配置创建无 Issue 的治理运行；不会跟随每日复盘设置。</p>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-2">
          <Checkbox id="knowledge-governance-enabled" checked={enabled} disabled={!canManage} onCheckedChange={(value) => setEnabled(value === true)} />
          <Label htmlFor="knowledge-governance-enabled">启用</Label>
        </div>
        <Select items={[{ value: "none", label: "未选择" }, ...agentChoices.map((agent) => ({ value: agent.id, label: agent.name }))]} value={agentId || "none"} onValueChange={(value) => setAgentId(!value || value === "none" ? "" : value)} disabled={!canManage}>
          <SelectTrigger className="w-48" data-testid="knowledge-governance-agent"><SelectValue placeholder="选择 Agent" /></SelectTrigger>
          <SelectContent>
            <SelectItem value="none">未选择</SelectItem>
            {agentChoices.map((agent) => <SelectItem key={agent.id} value={agent.id}>{agent.name}</SelectItem>)}
          </SelectContent>
        </Select>
        <Select items={[{ value: "daily", label: "每日" }, { value: "weekly", label: "每周" }, { value: "monthly", label: "每月" }]} value={cadence} onValueChange={(value) => setCadence((value ?? "daily") as typeof cadence)} disabled={!canManage}>
          <SelectTrigger className="w-32" data-testid="knowledge-governance-cadence"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="daily">每日</SelectItem>
            <SelectItem value="weekly">每周</SelectItem>
            <SelectItem value="monthly">每月</SelectItem>
          </SelectContent>
        </Select>
        <Input className="w-44" aria-label="IANA timezone" value={timezone} onChange={(event) => setTimezone(event.target.value)} disabled={!canManage} />
        {canManage ? <Button size="sm" disabled={save.isPending} onClick={() => save.mutate({ enabled, agent_id: agentId, cadence, timezone }, { onError: (error) => toast.error(clientErrorMessage(error) ?? "保存治理配置失败") })}>保存</Button> : null}
      </div>
      <p className="text-muted-foreground text-caption" data-testid="knowledge-governance-config-source">来源：{config.data?.source === "explicit" ? "工作区配置" : "默认关闭"} · 版本：{config.data?.version ?? 0}</p>
      {config.data?.executable && config.data.next_scheduled_at ? <p className="text-muted-foreground text-caption" data-testid="knowledge-governance-next-schedule">下一次计划：{config.data.next_scheduled_at}</p> : <p className="text-muted-foreground text-caption" data-testid="knowledge-governance-not-executable">不可执行：{config.data?.block_reason === "disabled" ? "已关闭" : "未配置可运行 Agent"}</p>}
      {config.data?.audit[0] ? <p className="text-muted-foreground text-caption">最近配置审计：版本 {config.data.audit[0].version}，{config.data.audit[0].created_at}</p> : null}
    </section>
  );
}
