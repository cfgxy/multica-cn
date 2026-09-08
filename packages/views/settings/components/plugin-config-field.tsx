"use client";

import { Lock } from "lucide-react";
import type { PluginConfigField } from "@multica/core/types";
import { Input } from "@multica/ui/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Switch } from "@multica/ui/components/ui/switch";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../../i18n";

/**
 * One generated configuration input.
 *
 * The form is generated from the manifest, not supplied by the plugin:
 * rendering plugin-authored form markup in the host would put plugin code on
 * our origin. It is shared by the consent screen and the installed plugin's
 * settings form because those must agree on what a field looks like — a secret
 * that renders as a plain text input in one of them is the same leak wherever
 * it happens.
 */
export function ConfigFieldRow({
  field,
  value,
  secretValue,
  secretConfigured,
  disabled,
  onValueChange,
  onSecretChange,
}: {
  field: PluginConfigField;
  value: unknown;
  secretValue: string;
  /** A secret already stored under this name. Never its value. */
  secretConfigured: boolean;
  disabled: boolean;
  onValueChange: (value: unknown) => void;
  onSecretChange: (value: string) => void;
}) {
  const { t } = useT("settings");
  const isSecret = field.type === "secret";

  return (
    <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0">
        <div className="flex items-center gap-1.5 text-caption font-medium">
          {isSecret ? <Lock className="size-3 shrink-0 text-muted-foreground" /> : null}
          <span>{field.label}</span>
          {field.required ? <span className="text-destructive">*</span> : null}
        </div>
        {field.description ? (
          <p className="mt-0.5 text-caption text-muted-foreground">{field.description}</p>
        ) : null}
        {isSecret ? (
          <p className="mt-0.5 text-caption text-muted-foreground">
            {t(($) => $.plugins.config.secret_write_only)}
          </p>
        ) : null}
      </div>
      <div className="w-full sm:w-96">
        {isSecret ? (
          <Input
            type="password"
            autoComplete="off"
            disabled={disabled}
            value={secretValue}
            placeholder={secretConfigured
              ? t(($) => $.plugins.config.secret_set)
              : field.placeholder ?? ""}
            onChange={(event) => onSecretChange(event.target.value)}
          />
        ) : field.type === "bool" ? (
          <Switch
            disabled={disabled}
            checked={value === true}
            onCheckedChange={(checked) => onValueChange(checked === true)}
          />
        ) : field.type === "enum" ? (
          <Select
            items={(field.options ?? []).map((option) => ({ value: option, label: option }))}
            value={typeof value === "string" ? value : ""}
            onValueChange={(next) => next && onValueChange(next)}
          >
            <SelectTrigger disabled={disabled}><SelectValue /></SelectTrigger>
            <SelectContent>
              {(field.options ?? []).map((option) => (
                <SelectItem key={option} value={option}>{option}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : field.type === "number" ? (
          <Input
            type="number"
            disabled={disabled}
            value={typeof value === "number" ? String(value) : ""}
            placeholder={field.placeholder ?? ""}
            onChange={(event) => {
              const parsed = Number(event.target.value);
              onValueChange(event.target.value === "" || Number.isNaN(parsed) ? undefined : parsed);
            }}
          />
        ) : field.multiline === true ? (
          // A field whose value is a list of lines is unreadable in a
          // single-line input — and the generated form is the one piece of
          // plugin UI the host owns, so getting it wrong is our bug.
          <Textarea
            rows={4}
            disabled={disabled}
            value={typeof value === "string" ? value : ""}
            placeholder={field.placeholder ?? ""}
            onChange={(event) => onValueChange(event.target.value)}
          />
        ) : (
          <Input
            disabled={disabled}
            value={typeof value === "string" ? value : ""}
            placeholder={field.placeholder ?? ""}
            onChange={(event) => onValueChange(event.target.value)}
          />
        )}
      </div>
    </div>
  );
}

/**
 * Which required fields still have nothing in them.
 *
 * Secrets count as filled once one is stored, because the value is write-only
 * and the form cannot read it back to check. A field the manifest declares
 * required is what makes the install button wait: mounting a plugin whose
 * required credential is empty produces a plugin that fails on first use with
 * nothing on screen explaining why.
 */
export function missingRequiredFields(
  schema: PluginConfigField[],
  values: Record<string, unknown>,
  secrets: Record<string, string>,
  configuredSecrets: ReadonlySet<string> = new Set<string>(),
): string[] {
  return schema
    .filter((field) => {
      if (!field.required) return false;
      if (field.type === "secret") {
        return (secrets[field.key] ?? "").length === 0 && !configuredSecrets.has(field.key);
      }
      // `false` is a filled boolean, and 0 is a filled number.
      const value = values[field.key];
      if (field.type === "bool") return value === undefined;
      if (field.type === "number") return typeof value !== "number";
      return typeof value !== "string" || value.trim().length === 0;
    })
    .map((field) => field.label);
}

/**
 * The payload a generated form submits.
 *
 * Only a secret the administrator actually typed is included: sending "" would
 * clear a stored secret every time an unrelated field is saved.
 */
export function configPayload(
  values: Record<string, unknown>,
  secrets: Record<string, string>,
): Record<string, unknown> {
  const payload: Record<string, unknown> = { ...values };
  for (const [key, value] of Object.entries(secrets)) {
    if (value.length > 0) payload[key] = value;
  }
  return payload;
}
