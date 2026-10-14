"use client";

import { useState } from "react";
import { Check, ChevronsUpDown } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import {
  Command,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from "@multica/ui/components/ui/command";
import { copyText } from "@multica/ui/lib/clipboard";
import { useT } from "../i18n";

export function ModelChoice({
  label,
  value,
  options,
  disabled,
  onChange,
}: {
  label: string;
  value: string;
  options: { value: string; label: string }[];
  disabled?: boolean;
  onChange: (value: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const { t } = useT("settings");
  return (
    <div className="min-w-0 space-y-1">
      <span className="text-caption font-medium">{label}</span>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          render={
            <Button
              type="button"
              variant="outline"
              className="w-full justify-between"
              role="combobox"
              aria-label={label}
              aria-expanded={open}
              disabled={disabled}
            />
          }
        >
          <span className="truncate">
            {options.find((o) => o.value === value)?.label || value || "—"}
          </span>
          <ChevronsUpDown aria-hidden="true" />
        </PopoverTrigger>
        <PopoverContent
          className="w-80 max-w-[calc(100vw-2rem)] p-0"
          align="start"
        >
          <Command>
            <CommandInput aria-label={label} placeholder={label} />
            <CommandList>
              <CommandEmpty>
                {t(($) => $.model_configuration.empty)}
              </CommandEmpty>
              {options.map((o) => (
                <CommandItem
                  key={o.value}
                  value={`${o.label} ${o.value}`}
                  onSelect={() => {
                    onChange(o.value);
                    setOpen(false);
                  }}
                >
                  <Check
                    aria-hidden="true"
                    className={value === o.value ? "opacity-100" : "opacity-0"}
                  />
                  <span className="break-all">{o.label}</span>
                </CommandItem>
              ))}
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
    </div>
  );
}
export function ModelStatus({ status }: { status: string }) {
  const { t } = useT("settings");
  const labels: Record<string, string> = t(
    ($) => $.model_configuration.states,
    { returnObjects: true },
  );
  return (
    <span
      className={
        status === "failed" ||
        status === "error" ||
        status === "authorization_invalid"
          ? "text-destructive"
          : status === "succeeded" || status === "ready"
            ? "text-success"
            : "text-muted-foreground"
      }
    >
      {labels[status] ?? labels.unknown}
    </span>
  );
}
export function ModelError({ error }: { error: unknown }) {
  const { t } = useT("settings");
  const [copied, setCopied] = useState(false);
  if (!error) return null;
  const message =
    error instanceof Error
      ? error.message
      : typeof error === "string"
        ? error
        : t(($) => $.model_configuration.failed);
  const body =
    typeof error === "object" && error !== null && "body" in error
      ? error.body
      : null;
  const code =
    typeof body === "object" &&
    body !== null &&
    "code" in body &&
    typeof body.code === "string"
      ? body.code
      : message;
  const errors: Record<string, string> = t(
    ($) => $.model_configuration.errors,
    { returnObjects: true },
  );
  const linuxErrors: Record<string, string> = t(($) => $.linux_user.errors, {
    returnObjects: true,
  });
  const translated = errors[code] ?? linuxErrors[code];
  if (code === "source_not_configured") {
    return (
      <p role="status" className="text-caption text-muted-foreground">
        {translated}
      </p>
    );
  }
  const operationId =
    typeof body === "object" &&
    body !== null &&
    "operation_id" in body &&
    typeof body.operation_id === "string"
      ? body.operation_id
      : "";
  const diagnostics = [
    code,
    message,
    operationId ? `operation_id: ${operationId}` : "",
  ]
    .filter(Boolean)
    .join("\n");
  const conflict =
    typeof error === "object" &&
    error !== null &&
    "status" in error &&
    error.status === 409;
  return (
    <div
      role="alert"
      className="space-y-2 rounded-lg border border-destructive/40 p-3 text-caption"
    >
      <p className="break-words text-destructive">
        {conflict && !translated
          ? t(($) => $.model_configuration.conflict)
          : (translated ?? message)}
      </p>
      <Button
        variant="outline"
        size="sm"
        type="button"
        onClick={() => void copyText(diagnostics).then(setCopied)}
      >
        {copied
          ? t(($) => $.model_configuration.copied)
          : t(($) => $.model_configuration.copy_error)}
      </Button>
    </div>
  );
}
