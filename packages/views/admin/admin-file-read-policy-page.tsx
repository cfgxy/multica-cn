"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  adminFileReadPolicyOptions,
  useAdminRestoreFileReadPolicy,
  useAdminUpdateFileReadPolicy,
  type FileReadDenyRule,
} from "@multica/core/admin";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { SettingsSection, SettingsTab } from "../settings/components/settings-layout";

export function AdminFileReadPolicyPage() {
  const { t } = useTranslation("admin");
  const query = useQuery(adminFileReadPolicyOptions());
  const update = useAdminUpdateFileReadPolicy();
  const restore = useAdminRestoreFileReadPolicy();
  const [rules, setRules] = useState<FileReadDenyRule[]>([]);
  const [newRule, setNewRule] = useState({ pattern: "", description: "" });

  useEffect(() => {
    if (query.data !== undefined) setRules(query.data.rules);
  }, [query.data]);

  function save() {
    if (query.data === undefined || update.isPending === true) return;
    if (!window.confirm(t(($) => $.file_read_policy.save_confirm))) return;
    update.mutate({ version: query.data.version, rules });
  }

  function addRule(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const pattern = newRule.pattern.trim();
    if (pattern === "") return;
    setRules((current) => [...current, {
      id: `custom-${crypto.randomUUID()}`,
      pattern,
      description: newRule.description.trim(),
      enabled: true,
      builtin: false,
    }]);
    setNewRule({ pattern: "", description: "" });
  }

  if (query.isLoading === true || query.data === undefined) {
    return <div className="p-4 text-caption text-muted-foreground">{t(($) => $.loading)}</div>;
  }

  return (
    <SettingsTab
      title={t(($) => $.file_read_policy.title)}
      description={t(($) => $.file_read_policy.description)}
    >
      <SettingsSection title={t(($) => $.file_read_policy.rules_title)}>
        <div className="divide-y border-y">
          {rules.map((rule, index) => (
            <div key={rule.id} className="grid gap-2 py-3 md:grid-cols-[auto_minmax(12rem,1fr)_minmax(10rem,1fr)_auto] md:items-center">
              <label className="flex items-center gap-2 text-caption">
                <input
                  type="checkbox"
                  checked={rule.enabled === true}
                  onChange={(event) => {
                    const enabled = event.currentTarget.checked;
                    setRules((current) => current.map((item, i) =>
                      i === index ? { ...item, enabled } : item));
                  }}
                />
                {t(($) => $.file_read_policy.enabled)}
              </label>
              <Input
                aria-label={t(($) => $.file_read_policy.pattern)}
                value={rule.pattern}
                onChange={(event) => {
                  const pattern = event.currentTarget.value;
                  setRules((current) => current.map((item, i) =>
                    i === index ? { ...item, pattern } : item));
                }}
              />
              <Input
                aria-label={t(($) => $.file_read_policy.description_label)}
                value={rule.description}
                onChange={(event) => {
                  const description = event.currentTarget.value;
                  setRules((current) => current.map((item, i) =>
                    i === index ? { ...item, description } : item));
                }}
              />
              <Button
                type="button"
                variant="ghost"
                onClick={() => setRules((current) => current.filter((_, i) => i !== index))}
              >
                {t(($) => $.file_read_policy.remove)}
              </Button>
            </div>
          ))}
        </div>

        <form onSubmit={addRule} className="grid gap-2 py-3 md:grid-cols-[minmax(12rem,1fr)_minmax(10rem,1fr)_auto]">
          <Input
            aria-label={t(($) => $.file_read_policy.pattern)}
            placeholder={t(($) => $.file_read_policy.pattern_placeholder)}
            value={newRule.pattern}
            onChange={(event) => {
              const pattern = event.currentTarget.value;
              setNewRule((current) => ({ ...current, pattern }));
            }}
          />
          <Input
            aria-label={t(($) => $.file_read_policy.description_label)}
            placeholder={t(($) => $.file_read_policy.description_placeholder)}
            value={newRule.description}
            onChange={(event) => {
              const description = event.currentTarget.value;
              setNewRule((current) => ({ ...current, description }));
            }}
          />
          <Button type="submit" variant="outline">{t(($) => $.file_read_policy.add)}</Button>
        </form>
      </SettingsSection>

      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" onClick={save} disabled={update.isPending === true}>
          {t(($) => $.file_read_policy.save)}
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={restore.isPending === true}
          onClick={() => {
            if (window.confirm(t(($) => $.file_read_policy.restore_confirm))) restore.mutate(query.data.version);
          }}
        >
          {t(($) => $.file_read_policy.restore_defaults)}
        </Button>
        {(update.isError === true || restore.isError === true || query.isError === true) && (
          <span role="alert" className="text-caption text-destructive">{t(($) => $.file_read_policy.save_failed)}</span>
        )}
      </div>
    </SettingsTab>
  );
}
