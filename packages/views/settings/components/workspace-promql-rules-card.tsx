"use client";

import { useEffect, useState } from "react";
import { Activity, Plus, Trash2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Switch } from "@multica/ui/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useQuery } from "@tanstack/react-query";
import {
  usePreviewWorkspacePromQLRule,
  useSaveWorkspacePromQLRules,
  workspacePromQLRulesOptions,
} from "@multica/core/workspace";
import type {
  PromQLRuleComparison,
  PromQLRuleCondition,
  PromQLRuleGrouping,
  PromQLRuleMetric,
  PromQLRulePolicy,
  WorkspacePromQLRule,
  WorkspacePromQLRulePreview,
  WorkspacePromQLRuleSave,
} from "@multica/core/types";
import { toast } from "sonner";
import { useT } from "../../i18n";
import { SettingsSaveState, type SettingsSaveStatus } from "./settings-layout";

type NumericField =
  | "threshold"
  | "recovery_threshold"
  | "lookback_seconds"
  | "evaluation_seconds"
  | "for_seconds";

type RuleDraft = Omit<WorkspacePromQLRuleSave, NumericField> &
  Record<NumericField, string>;

const MAX_RULES = 20;

function toDraft(rule: WorkspacePromQLRule): RuleDraft {
  return {
    id: rule.id,
    name: rule.name,
    enabled: rule.enabled,
    metric: rule.metric,
    grouping: rule.grouping,
    condition: rule.condition,
    comparison: rule.comparison ?? ">=",
    threshold: String(rule.threshold ?? 5),
    recovery_threshold: String(rule.recovery_threshold ?? 3),
    lookback_seconds: String(rule.lookback_seconds ?? 300),
    evaluation_seconds: String(rule.evaluation_seconds ?? 30),
    for_seconds: String(rule.for_seconds ?? 0),
    no_data_policy: rule.no_data_policy,
    query_error_policy: rule.query_error_policy,
  };
}

function toSave(rule: RuleDraft): WorkspacePromQLRuleSave | null {
  const values = {} as Record<NumericField, number>;
  for (const field of [
    "threshold",
    "recovery_threshold",
    "lookback_seconds",
    "evaluation_seconds",
    "for_seconds",
  ] as const) {
    const value = Number(rule[field]);
    if (rule[field].trim() === "" || !Number.isFinite(value)) return null;
    values[field] = value;
  }
  return {
    id: rule.id,
    name: rule.name.trim(),
    enabled: rule.enabled,
    metric: rule.metric,
    grouping: rule.grouping,
    condition: rule.condition,
    comparison: rule.comparison,
    threshold: values.threshold,
    recovery_threshold: values.recovery_threshold,
    lookback_seconds: values.lookback_seconds,
    evaluation_seconds: values.evaluation_seconds,
    for_seconds: values.for_seconds,
    no_data_policy: rule.no_data_policy,
    query_error_policy: rule.query_error_policy,
  };
}

function newRule(): RuleDraft {
  return {
    id: crypto.randomUUID(),
    name: "",
    enabled: true,
    metric: "failures",
    grouping: "workspace",
    condition: "threshold",
    comparison: ">=",
    threshold: "5",
    recovery_threshold: "3",
    lookback_seconds: "300",
    evaluation_seconds: "30",
    for_seconds: "0",
    no_data_policy: "keep",
    query_error_policy: "keep",
  };
}

function ruleStatus(rule: WorkspacePromQLRule): string {
  if (rule.state?.last_error) return "error";
  if (rule.state?.firing === true) return "firing";
  if (rule.state?.pending_since) return "pending";
  return "inactive";
}

export function WorkspacePromQLRulesCard({
  wsId,
  canManage,
}: {
  wsId: string;
  canManage: boolean;
}) {
  const { t } = useT("settings");
  const query = useQuery(workspacePromQLRulesOptions(wsId));
  const save = useSaveWorkspacePromQLRules(wsId);
  const previewMutation = usePreviewWorkspacePromQLRule();
  const [rules, setRules] = useState<RuleDraft[]>([]);
  const [dirty, setDirty] = useState(false);
  const [preview, setPreview] = useState<{
    id: string;
    result: WorkspacePromQLRulePreview;
  } | null>(null);

  useEffect(() => {
    if (!query.data || dirty) return;
    setRules(query.data.rules.map(toDraft));
  }, [dirty, query.data]);

  if (query.isPending) {
    return (
      <div className="space-y-3">
        <Skeleton className="h-20 w-full" />
        <Skeleton className="h-20 w-full" />
      </div>
    );
  }

  if (query.isError) {
    return (
      <p role="alert" className="text-caption text-destructive">
        {t(($) => $.workspace.promql_load_failed)}
      </p>
    );
  }

  const saveRules = rules.map(toSave);
  const names = rules.map((rule) => rule.name.trim().toLocaleLowerCase());
  const invalid =
    saveRules.some((rule) => rule === null) ||
    rules.some((rule) => !rule.name.trim()) ||
    names.some((name, index) => names.indexOf(name) !== index) ||
    rules.some((_rule, index) => {
      const parsed = saveRules[index];
      if (!parsed) return false;
      if (
        !Number.isInteger(parsed.lookback_seconds) ||
        parsed.lookback_seconds < 60 ||
        parsed.lookback_seconds > 604800 ||
        !Number.isInteger(parsed.evaluation_seconds) ||
        parsed.evaluation_seconds < 15 ||
        parsed.evaluation_seconds > 3600 ||
        !Number.isInteger(parsed.for_seconds) ||
        parsed.for_seconds < 0 ||
        parsed.for_seconds > 86400
      ) {
        return true;
      }
      if (parsed.condition !== "threshold") return false;
      const directionUp = parsed.comparison === ">" || parsed.comparison === ">=";
      return (
        parsed.threshold! < 0 ||
        parsed.recovery_threshold! < 0 ||
        (parsed.metric === "failure_rate" &&
          (parsed.threshold! > 100 || parsed.recovery_threshold! > 100)) ||
        (directionUp
          ? parsed.recovery_threshold! >= parsed.threshold!
          : parsed.recovery_threshold! <= parsed.threshold!)
      );
    });
  const saveStatus: SettingsSaveStatus = save.isPending
    ? "saving"
    : dirty && invalid
      ? "error"
      : "idle";

  const updateRule = (index: number, patch: Partial<RuleDraft>) => {
    setRules((current) =>
      current.map((rule, i) => (i === index ? { ...rule, ...patch } : rule)),
    );
    setDirty(true);
    setPreview(null);
  };

  const handleSave = async () => {
    if (invalid || saveRules.some((rule) => rule === null)) return;
    try {
      const result = await save.mutateAsync(saveRules as WorkspacePromQLRuleSave[]);
      setRules(result.rules.map(toDraft));
      setDirty(false);
      setPreview(null);
      toast.success(t(($) => $.workspace.promql_saved));
    } catch (error) {
      toast.error(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.workspace.promql_save_failed),
      );
    }
  };

  const handlePreview = async (rule: RuleDraft) => {
    const parsed = toSave(rule);
    if (!parsed || !query.data?.prometheus_configured) return;
    try {
      const result = await previewMutation.mutateAsync(parsed);
      setPreview({ id: rule.id, result });
    } catch {
      toast.error(t(($) => $.workspace.promql_preview_failed));
    }
  };

  const field = (label: string, control: React.ReactNode) => (
    <label className="grid min-w-0 gap-1.5 text-caption text-muted-foreground">
      <span>{label}</span>
      {control}
    </label>
  );

  const select = (
    label: string,
    value: string,
    options: Array<{ value: string; label: string }>,
    onChange: (value: string) => void,
  ) =>
    field(
      label,
      <Select
        items={options}
        value={value}
        onValueChange={(next) => next && onChange(next)}
        disabled={!canManage}
      >
        <SelectTrigger aria-label={label} className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>,
    );

  const statusLabel = (rule: WorkspacePromQLRule | undefined) => {
    if (!rule) return null;
    const status = ruleStatus(rule);
    const labels = {
      inactive: t(($) => $.workspace.promql_status_inactive),
      pending: t(($) => $.workspace.promql_status_pending),
      firing: t(($) => $.workspace.promql_status_firing),
      error: t(($) => $.workspace.promql_status_error),
    };
    return labels[status as keyof typeof labels] ?? status;
  };

  const existingRules = new Map(query.data.rules.map((rule) => [rule.id, rule]));

  return (
    <div className="space-y-3">
      <p className="text-caption leading-5 text-muted-foreground">
        {t(($) => $.workspace.promql_description)}
      </p>
      {!query.data.prometheus_configured && (
        <p className="text-caption text-muted-foreground">
          {t(($) => $.workspace.promql_unconfigured)}
        </p>
      )}

      {rules.length === 0 && (
        <p className="py-3 text-caption text-muted-foreground">
          {t(($) => $.workspace.promql_empty)}
        </p>
      )}

      <div className="space-y-3">
        {rules.map((rule, index) => {
          const liveRule = existingRules.get(rule.id);
          const activePreview = preview?.id === rule.id ? preview.result : null;
          const numericInput = (key: NumericField, min?: number, max?: number) =>
            field(
              t(($) => $.workspace[`promql_${key}`]),
              <Input
                type="number"
                inputMode="decimal"
                min={min}
                max={max}
                step={key === "threshold" || key === "recovery_threshold" ? "any" : 1}
                value={rule[key]}
                disabled={!canManage}
                onChange={(event) => updateRule(index, { [key]: event.target.value })}
              />,
            );

          return (
            <section
              key={rule.id}
              aria-label={rule.name || t(($) => $.workspace.promql_rule)}
              className="min-w-0 space-y-4 rounded-md border border-border p-3 sm:p-4"
            >
              <div className="flex flex-wrap items-center gap-3">
                <Input
                  aria-label={t(($) => $.workspace.promql_name)}
                  value={rule.name}
                  maxLength={80}
                  disabled={!canManage}
                  placeholder={t(($) => $.workspace.promql_name_placeholder)}
                  className="min-w-40 flex-1"
                  onChange={(event) => updateRule(index, { name: event.target.value })}
                />
                <span className="text-caption text-muted-foreground">
                  {statusLabel(liveRule) ?? t(($) => $.workspace.promql_status_unsaved)}
                </span>
                <Switch
                  aria-label={`${t(($) => $.workspace.promql_enabled)}: ${rule.name.trim() || `${t(($) => $.workspace.promql_rule)} ${index + 1}`}`}
                  checked={rule.enabled}
                  disabled={!canManage}
                  onCheckedChange={(enabled) => updateRule(index, { enabled })}
                />
                {canManage && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t(($) => $.workspace.promql_remove)}
                    title={t(($) => $.workspace.promql_remove)}
                    onClick={() => {
                      setRules((current) => current.filter((_, i) => i !== index));
                      setDirty(true);
                      setPreview(null);
                    }}
                  >
                    <Trash2 aria-hidden="true" />
                  </Button>
                )}
              </div>

              <div className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
                {select(t(($) => $.workspace.promql_metric), rule.metric, [
                  { value: "failures", label: t(($) => $.workspace.promql_metric_failures) },
                  { value: "failure_rate", label: t(($) => $.workspace.promql_metric_failure_rate) },
                ], (value) => updateRule(index, { metric: value as PromQLRuleMetric }))}
                {select(t(($) => $.workspace.promql_grouping), rule.grouping, [
                  { value: "workspace", label: t(($) => $.workspace.promql_group_workspace) },
                  { value: "agent", label: t(($) => $.workspace.promql_group_agent) },
                ], (value) => updateRule(index, { grouping: value as PromQLRuleGrouping }))}
                {select(t(($) => $.workspace.promql_condition), rule.condition, [
                  { value: "threshold", label: t(($) => $.workspace.promql_condition_threshold) },
                  { value: "present", label: t(($) => $.workspace.promql_condition_present) },
                ], (value) => updateRule(index, { condition: value as PromQLRuleCondition }))}
                {rule.condition === "threshold" && (
                  <>
                    {select(t(($) => $.workspace.promql_comparison), rule.comparison ?? ">=", [
                      { value: ">", label: ">" },
                      { value: ">=", label: ">=" },
                      { value: "<", label: "<" },
                      { value: "<=", label: "<=" },
                    ], (value) => updateRule(index, { comparison: value as PromQLRuleComparison }))}
                    {numericInput("threshold", 0, rule.metric === "failure_rate" ? 100 : undefined)}
                    {numericInput("recovery_threshold", 0, rule.metric === "failure_rate" ? 100 : undefined)}
                  </>
                )}
                {numericInput("lookback_seconds", 60, 604800)}
                {numericInput("evaluation_seconds", 15, 3600)}
                {numericInput("for_seconds", 0, 86400)}
                {select(t(($) => $.workspace.promql_no_data_policy), rule.no_data_policy, [
                  { value: "keep", label: t(($) => $.workspace.promql_policy_keep) },
                  { value: "trigger", label: t(($) => $.workspace.promql_policy_trigger) },
                  { value: "resolve", label: t(($) => $.workspace.promql_policy_resolve) },
                ], (value) => updateRule(index, { no_data_policy: value as PromQLRulePolicy }))}
                {select(t(($) => $.workspace.promql_query_error_policy), rule.query_error_policy, [
                  { value: "keep", label: t(($) => $.workspace.promql_policy_keep) },
                  { value: "trigger", label: t(($) => $.workspace.promql_policy_trigger) },
                  { value: "resolve", label: t(($) => $.workspace.promql_policy_resolve) },
                ], (value) => updateRule(index, { query_error_policy: value as PromQLRulePolicy }))}
              </div>

              {canManage && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={
                    !query.data.prometheus_configured ||
                    previewMutation.isPending ||
                    toSave(rule) === null
                  }
                  onClick={() => void handlePreview(rule)}
                >
                  <Activity aria-hidden="true" />
                  {t(($) => $.workspace.promql_preview)}
                </Button>
              )}

              {activePreview && (
                <div className="min-w-0 space-y-2 border-t border-border pt-3">
                  <p className="text-caption font-medium">
                    {activePreview.matches
                      ? t(($) => $.workspace.promql_preview_match)
                      : t(($) => $.workspace.promql_preview_no_match)}
                  </p>
                  <pre className="max-w-full overflow-x-auto whitespace-pre-wrap break-all rounded-sm bg-muted p-2 font-mono text-caption">
                    {activePreview.query}
                  </pre>
                  {activePreview.samples.length > 0 ? (
                    <ul className="space-y-1 text-caption text-muted-foreground">
                      {activePreview.samples.map((sample, sampleIndex) => (
                        <li key={`${sampleIndex}-${sample.value}`} className="break-all">
                          {sample.value}
                          {Object.keys(sample.labels ?? {}).length > 0
                            ? ` · ${JSON.stringify(sample.labels)}`
                            : ""}
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="text-caption text-muted-foreground">
                      {t(($) => $.workspace.promql_no_samples)}
                    </p>
                  )}
                </div>
              )}
            </section>
          );
        })}
      </div>

      {canManage && (
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={rules.length >= MAX_RULES || save.isPending}
            onClick={() => {
              setRules((current) => [...current, newRule()]);
              setDirty(true);
            }}
          >
            <Plus aria-hidden="true" />
            {t(($) => $.workspace.promql_add)}
          </Button>
          <Button
            type="button"
            size="sm"
            disabled={!dirty || invalid || save.isPending}
            onClick={() => void handleSave()}
          >
            {save.isPending
              ? t(($) => $.workspace.promql_saving)
              : t(($) => $.workspace.promql_save)}
          </Button>
          <SettingsSaveState
            status={saveStatus}
            savingLabel={t(($) => $.workspace.promql_saving)}
            savedLabel=""
            errorLabel={t(($) => $.workspace.promql_invalid)}
          />
          {invalid && (
            <p role="alert" className="basis-full text-caption text-destructive">
              {t(($) => $.workspace.promql_invalid)}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
