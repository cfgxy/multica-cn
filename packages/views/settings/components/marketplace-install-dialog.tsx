"use client";

import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import type { MarketplaceItem } from "@multica/core/types";
import { useT } from "../../i18n";

/**
 * Collects what an install needs and hands it to the caller.
 *
 * Secret placeholders are typed into password inputs and go up with the
 * install request. They are never read back: the workspace MCP library stores
 * the entry write-only, so there is no later screen that could show them
 * again, and this dialog says so rather than implying the value is editable.
 */
export function MarketplaceInstallDialog({
  open,
  item,
  installing,
  existingNames,
  onOpenChange,
  onInstall,
}: {
  open: boolean;
  item: MarketplaceItem | null;
  installing: boolean;
  existingNames: Set<string>;
  onOpenChange: (open: boolean) => void;
  onInstall: (input: { name: string; values: Record<string, string> }) => void;
}) {
  const { t } = useT("settings");
  const [name, setName] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});

  // Reopening on a different entry must not carry the previous entry's
  // values across — that would submit one server's token to another.
  useEffect(() => {
    if (open && item) {
      setName(item.name);
      setValues({});
    }
  }, [open, item]);

  if (!item) return null;

  const placeholders = item.placeholders ?? [];
  const isMcp = item.kind === "mcp";
  const trimmedName = name.trim();
  const nameTaken = isMcp && trimmedName !== "" && existingNames.has(trimmedName);
  const missingRequired = placeholders.some(
    (placeholder) => placeholder.required && !(values[placeholder.key] ?? "").trim(),
  );
  const canSubmit =
    !installing && !missingRequired && (!isMcp || (trimmedName !== "" && !nameTaken));

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.marketplace.install_title, { name: item.name })}</DialogTitle>
          <DialogDescription>{item.summary}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          {isMcp ? (
            <div className="space-y-1.5">
              <Label htmlFor="marketplace-install-name">
                {t(($) => $.marketplace.server_name)}
              </Label>
              <Input
                id="marketplace-install-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                autoComplete="off"
              />
              {nameTaken ? (
                <p className="text-caption text-destructive">
                  {t(($) => $.marketplace.name_taken)}
                </p>
              ) : null}
            </div>
          ) : (
            <p className="text-caption text-muted-foreground">
              {t(($) => $.marketplace.skill_source_note, { url: item.source_url ?? "" })}
            </p>
          )}

          {placeholders.map((placeholder) => (
            <div key={placeholder.key} className="space-y-1.5">
              <Label htmlFor={`marketplace-value-${placeholder.key}`}>
                {placeholder.label || placeholder.key}
                {placeholder.required ? null : (
                  <span className="ml-1 text-caption font-normal text-muted-foreground">
                    {t(($) => $.marketplace.optional)}
                  </span>
                )}
              </Label>
              <Input
                id={`marketplace-value-${placeholder.key}`}
                type={placeholder.secret ? "password" : "text"}
                // A credential must not be offered back by the browser's
                // autofill on an unrelated form.
                autoComplete={placeholder.secret ? "new-password" : "off"}
                value={values[placeholder.key] ?? ""}
                onChange={(event) =>
                  setValues((prev) => ({ ...prev, [placeholder.key]: event.target.value }))
                }
              />
              {placeholder.description ? (
                <p className="text-caption text-muted-foreground">
                  {placeholder.description}
                </p>
              ) : null}
            </div>
          ))}

          {placeholders.some((placeholder) => placeholder.secret) ? (
            <p className="text-caption text-muted-foreground">
              {t(($) => $.marketplace.secret_note)}
            </p>
          ) : null}
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={installing}
          >
            {t(($) => $.marketplace.cancel)}
          </Button>
          <Button
            onClick={() => onInstall({ name: trimmedName, values })}
            disabled={!canSubmit}
          >
            {installing ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
            {t(($) => $.marketplace.install)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
