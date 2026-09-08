"use client";

import { useEffect, useMemo, useState } from "react";
import { Loader2, Plus, Trash2 } from "lucide-react";
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
import { Textarea } from "@multica/ui/components/ui/textarea";
import type {
  MarketplaceListing,
  MarketplacePlaceholder,
  MarketplaceScanFinding,
} from "@multica/core/types";
import { useT } from "../../i18n";

/**
 * Publishes a skill or MCP entry to the marketplace, or edits one already
 * published.
 *
 * Two rules shape this form and are worth stating up front:
 *
 *  - For an MCP listing the publisher authors a TEMPLATE. Nothing is read out
 *    of the workspace MCP library: that config column is write-only, and this
 *    dialog never asks the server for it. What goes up is a template whose
 *    credential-bearing fields are `${placeholder}` tokens, plus the list of
 *    placeholders an installer will be prompted for.
 *  - Consequently there is no "value" input anywhere on this form. A real token
 *    typed into a header is rejected server-side by the secret scan, and the
 *    findings are rendered by field and line — never by content.
 */

/** The transports a published MCP template may declare. */
type PublishTransport = "stdio" | "http" | "sse";

const TRANSPORTS: PublishTransport[] = ["stdio", "http", "sse"];

type PlaceholderDraft = MarketplacePlaceholder;

type KeyValueDraft = { key: string; value: string };

export type MarketplacePublishSubmit = {
  kind: string;
  name: string;
  summary: string;
  description: string;
  homepage_url: string;
  categories: string[];
  source_url?: string;
  config_template?: unknown;
  placeholders?: MarketplacePlaceholder[];
};

function emptyPlaceholder(): PlaceholderDraft {
  return { key: "", label: "", description: "", secret: false, required: true };
}

function pairsToRecord(pairs: KeyValueDraft[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const pair of pairs) {
    const key = pair.key.trim();
    if (key !== "") out[key] = pair.value;
  }
  return out;
}

function recordToPairs(value: unknown): KeyValueDraft[] {
  if (!value || typeof value !== "object" || Array.isArray(value)) return [];
  return Object.entries(value as Record<string, unknown>).flatMap(([key, item]) =>
    typeof item === "string" ? [{ key, value: item }] : [],
  );
}

/**
 * Classifies an existing template the way the server does, so reopening a
 * published listing lands on the transport it was published under. `sse` is
 * preserved rather than folded into `http`: they are different wire protocols
 * and the server keeps them distinct.
 */
function transportOfTemplate(template: unknown): PublishTransport {
  if (!template || typeof template !== "object" || Array.isArray(template)) {
    return "stdio";
  }
  const entry = template as Record<string, unknown>;
  const declared =
    typeof entry.type === "string" ? entry.type.trim().toLowerCase() : "";
  if (declared === "local" || declared === "stdio") return "stdio";
  if (declared === "sse") return "sse";
  if (
    declared === "remote" ||
    declared === "http" ||
    declared === "streamable-http"
  ) {
    return "http";
  }
  if (entry.command) return "stdio";
  if (entry.url) return "http";
  return "stdio";
}

function stringArray(value: unknown): string[] {
  return Array.isArray(value)
    ? value.flatMap((item) => (typeof item === "string" ? [item] : []))
    : [];
}

function stringField(value: unknown): string {
  return typeof value === "string" ? value : "";
}

export function MarketplacePublishDialog({
  open,
  kind,
  listing,
  submitting,
  findings,
  scannerRevision,
  errorMessage,
  onOpenChange,
  onSubmit,
}: {
  open: boolean;
  /** "skill" or "mcp". Fixed by the row the publish was started from. */
  kind: string;
  /** The listing being edited, or null when publishing something new. */
  listing: MarketplaceListing | null;
  submitting: boolean;
  /** Secret-scan findings from a rejected submit, location-only. */
  findings: MarketplaceScanFinding[];
  scannerRevision: string;
  /** A non-scan failure (conflict, permission, validation) to show inline. */
  errorMessage: string;
  onOpenChange: (open: boolean) => void;
  onSubmit: (input: MarketplacePublishSubmit) => void;
}) {
  const { t } = useT("settings");
  const isMcp = kind === "mcp";
  // A withdrawn listing is reopened to be published again, not edited: the
  // server refuses a PATCH on a tombstone. Its fields still prefill, because
  // republishing the same thing under the same reserved name is the point.
  const editingLive = listing !== null && listing.state !== "withdrawn";

  const [name, setName] = useState("");
  const [summary, setSummary] = useState("");
  const [description, setDescription] = useState("");
  const [homepageUrl, setHomepageUrl] = useState("");
  const [categories, setCategories] = useState("");
  const [sourceUrl, setSourceUrl] = useState("");

  const [transport, setTransport] = useState<PublishTransport>("stdio");
  const [command, setCommand] = useState("");
  const [args, setArgs] = useState("");
  const [url, setUrl] = useState("");
  const [headers, setHeaders] = useState<KeyValueDraft[]>([]);
  const [env, setEnv] = useState<KeyValueDraft[]>([]);
  const [placeholders, setPlaceholders] = useState<PlaceholderDraft[]>([]);

  // Reopening on a different listing must not carry the previous one's fields
  // across — publishing one server's template under another's name is exactly
  // the mistake this reset prevents.
  useEffect(() => {
    if (!open) return;
    const template =
      listing?.config_template && typeof listing.config_template === "object"
        ? (listing.config_template as Record<string, unknown>)
        : {};
    setName(listing?.name ?? "");
    setSummary(listing?.summary ?? "");
    setDescription(listing?.description ?? "");
    setHomepageUrl(listing?.homepage_url ?? "");
    setCategories((listing?.categories ?? []).join(", "));
    setSourceUrl(listing?.source_url ?? "");
    setTransport(listing ? transportOfTemplate(listing.config_template) : "stdio");
    setCommand(stringField(template.command));
    setArgs(stringArray(template.args).join("\n"));
    setUrl(stringField(template.url));
    setHeaders(recordToPairs(template.headers));
    setEnv(recordToPairs(template.env));
    setPlaceholders(listing?.placeholders ? [...listing.placeholders] : []);
  }, [open, listing]);

  const template = useMemo(() => {
    if (!isMcp) return undefined;
    const entry: Record<string, unknown> = { type: transport };
    if (transport === "stdio") {
      entry.command = command.trim();
      const argList = args
        .split("\n")
        .map((line) => line.trim())
        .filter((line) => line !== "");
      if (argList.length > 0) entry.args = argList;
      const envRecord = pairsToRecord(env);
      if (Object.keys(envRecord).length > 0) entry.env = envRecord;
    } else {
      entry.url = url.trim();
      const headerRecord = pairsToRecord(headers);
      if (Object.keys(headerRecord).length > 0) entry.headers = headerRecord;
    }
    return entry;
  }, [isMcp, transport, command, args, env, url, headers]);

  const declaredPlaceholders = useMemo(
    () =>
      placeholders
        .map((placeholder) => ({ ...placeholder, key: placeholder.key.trim() }))
        .filter((placeholder) => placeholder.key !== ""),
    [placeholders],
  );

  // The same rule the server enforces, checked here so the publisher sees it
  // before a round trip: a declared placeholder the template never references
  // would show every installer an input that goes nowhere.
  const unusedPlaceholders = useMemo(() => {
    if (!isMcp || !template) return [];
    const serialized = JSON.stringify(template);
    return declaredPlaceholders
      .filter((placeholder) => !serialized.includes("${" + placeholder.key + "}"))
      .map((placeholder) => placeholder.key);
  }, [isMcp, template, declaredPlaceholders]);

  const trimmedName = name.trim();
  const nameValid = /^[A-Za-z0-9_-]+$/.test(trimmedName);
  const transportComplete = !isMcp
    ? sourceUrl.trim() !== ""
    : transport === "stdio"
      ? command.trim() !== ""
      : url.trim() !== "";
  const canSubmit =
    !submitting &&
    trimmedName !== "" &&
    nameValid &&
    transportComplete &&
    unusedPlaceholders.length === 0;

  const handleSubmit = () => {
    onSubmit({
      kind,
      name: trimmedName,
      summary: summary.trim(),
      description: description.trim(),
      homepage_url: homepageUrl.trim(),
      categories: categories
        .split(",")
        .map((entry) => entry.trim())
        .filter((entry) => entry !== ""),
      ...(isMcp
        ? { config_template: template, placeholders: declaredPlaceholders }
        : { source_url: sourceUrl.trim() }),
    });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {editingLive
              ? t(($) => $.marketplace.publish.edit_title, { name: listing.name })
              : t(($) => $.marketplace.publish.title)}
          </DialogTitle>
          <DialogDescription>
            {isMcp
              ? t(($) => $.marketplace.publish.template_note)
              : t(($) => $.marketplace.publish.skill_note)}
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="marketplace-publish-name">
              {t(($) => $.marketplace.publish.name)}
            </Label>
            <Input
              id="marketplace-publish-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              autoComplete="off"
            />
            {trimmedName !== "" && !nameValid ? (
              <p className="text-caption text-destructive">
                {t(($) => $.marketplace.publish.name_charset)}
              </p>
            ) : (
              <p className="text-caption text-muted-foreground">
                {t(($) => $.marketplace.publish.name_note)}
              </p>
            )}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="marketplace-publish-summary">
              {t(($) => $.marketplace.publish.summary)}
            </Label>
            <Input
              id="marketplace-publish-summary"
              value={summary}
              onChange={(event) => setSummary(event.target.value)}
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="marketplace-publish-description">
              {t(($) => $.marketplace.publish.description)}
            </Label>
            <Textarea
              id="marketplace-publish-description"
              rows={3}
              value={description}
              onChange={(event) => setDescription(event.target.value)}
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="marketplace-publish-homepage">
              {t(($) => $.marketplace.publish.homepage_url)}
            </Label>
            <Input
              id="marketplace-publish-homepage"
              value={homepageUrl}
              onChange={(event) => setHomepageUrl(event.target.value)}
              autoComplete="off"
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="marketplace-publish-categories">
              {t(($) => $.marketplace.publish.categories)}
            </Label>
            <Input
              id="marketplace-publish-categories"
              value={categories}
              onChange={(event) => setCategories(event.target.value)}
              autoComplete="off"
            />
          </div>

          {!isMcp ? (
            <div className="space-y-1.5">
              <Label htmlFor="marketplace-publish-source">
                {t(($) => $.marketplace.publish.source_url)}
              </Label>
              <Input
                id="marketplace-publish-source"
                value={sourceUrl}
                onChange={(event) => setSourceUrl(event.target.value)}
                autoComplete="off"
              />
              <p className="text-caption text-muted-foreground">
                {t(($) => $.marketplace.publish.source_url_note)}
              </p>
            </div>
          ) : (
            <>
              <div className="space-y-1.5">
                <Label>{t(($) => $.marketplace.publish.transport)}</Label>
                <div className="flex gap-2">
                  {TRANSPORTS.map((candidate) => (
                    <Button
                      key={candidate}
                      type="button"
                      size="sm"
                      variant={transport === candidate ? "secondary" : "outline"}
                      aria-pressed={transport === candidate}
                      onClick={() => setTransport(candidate)}
                    >
                      {candidate === "stdio"
                        ? "stdio"
                        : candidate === "http"
                          ? "HTTP"
                          : "SSE"}
                    </Button>
                  ))}
                </div>
              </div>

              {transport === "stdio" ? (
                <>
                  <div className="space-y-1.5">
                    <Label htmlFor="marketplace-publish-command">
                      {t(($) => $.marketplace.publish.command)}
                    </Label>
                    <Input
                      id="marketplace-publish-command"
                      value={command}
                      onChange={(event) => setCommand(event.target.value)}
                      autoComplete="off"
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="marketplace-publish-args">
                      {t(($) => $.marketplace.publish.args)}
                    </Label>
                    <Textarea
                      id="marketplace-publish-args"
                      rows={3}
                      value={args}
                      onChange={(event) => setArgs(event.target.value)}
                    />
                    <p className="text-caption text-muted-foreground">
                      {t(($) => $.marketplace.publish.args_note)}
                    </p>
                  </div>
                  <PairEditor
                    label={t(($) => $.marketplace.publish.env)}
                    idPrefix="marketplace-publish-env"
                    pairs={env}
                    onChange={setEnv}
                    addLabel={t(($) => $.marketplace.publish.add_env)}
                    removeLabel={t(($) => $.marketplace.publish.remove_row)}
                  />
                </>
              ) : (
                <>
                  <div className="space-y-1.5">
                    <Label htmlFor="marketplace-publish-url">
                      {t(($) => $.marketplace.publish.url)}
                    </Label>
                    <Input
                      id="marketplace-publish-url"
                      value={url}
                      onChange={(event) => setUrl(event.target.value)}
                      autoComplete="off"
                    />
                  </div>
                  <PairEditor
                    label={t(($) => $.marketplace.publish.headers)}
                    idPrefix="marketplace-publish-header"
                    pairs={headers}
                    onChange={setHeaders}
                    addLabel={t(($) => $.marketplace.publish.add_header)}
                    removeLabel={t(($) => $.marketplace.publish.remove_row)}
                  />
                </>
              )}

              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <Label>{t(($) => $.marketplace.publish.placeholders)}</Label>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() =>
                      setPlaceholders((prev) => [...prev, emptyPlaceholder()])
                    }
                  >
                    <Plus className="h-4 w-4" />
                    {t(($) => $.marketplace.publish.add_placeholder)}
                  </Button>
                </div>
                <p className="text-caption text-muted-foreground">
                  {t(($) => $.marketplace.publish.placeholders_note)}
                </p>
                {placeholders.map((placeholder, index) => (
                  <div
                    key={index}
                    className="flex flex-col gap-2 rounded-md border border-surface-border p-2 sm:flex-row sm:items-start"
                  >
                    <Input
                      aria-label={t(($) => $.marketplace.publish.placeholder_key)}
                      placeholder={t(($) => $.marketplace.publish.placeholder_key)}
                      value={placeholder.key}
                      onChange={(event) =>
                        setPlaceholders((prev) =>
                          prev.map((item, i) =>
                            i === index ? { ...item, key: event.target.value } : item,
                          ),
                        )
                      }
                      autoComplete="off"
                    />
                    <Input
                      aria-label={t(($) => $.marketplace.publish.placeholder_label)}
                      placeholder={t(($) => $.marketplace.publish.placeholder_label)}
                      value={placeholder.label}
                      onChange={(event) =>
                        setPlaceholders((prev) =>
                          prev.map((item, i) =>
                            i === index ? { ...item, label: event.target.value } : item,
                          ),
                        )
                      }
                      autoComplete="off"
                    />
                    <label className="flex shrink-0 items-center gap-1.5 text-caption text-muted-foreground">
                      <input
                        type="checkbox"
                        checked={placeholder.secret}
                        onChange={(event) =>
                          setPlaceholders((prev) =>
                            prev.map((item, i) =>
                              i === index
                                ? { ...item, secret: event.target.checked }
                                : item,
                            ),
                          )
                        }
                      />
                      {t(($) => $.marketplace.publish.placeholder_secret)}
                    </label>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      aria-label={t(($) => $.marketplace.publish.remove_row)}
                      onClick={() =>
                        setPlaceholders((prev) => prev.filter((_, i) => i !== index))
                      }
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                ))}
                {unusedPlaceholders.length > 0 ? (
                  <p className="text-caption text-destructive">
                    {t(($) => $.marketplace.publish.placeholder_unused, {
                      keys: unusedPlaceholders.join(", "),
                    })}
                  </p>
                ) : null}
              </div>
            </>
          )}

          {findings.length > 0 ? (
            <div className="space-y-1.5 rounded-md border border-destructive/40 p-3">
              <p className="text-caption font-medium text-destructive">
                {t(($) => $.marketplace.publish.scan_blocked)}
              </p>
              <ul className="space-y-0.5">
                {findings.map((finding, index) => (
                  <li
                    key={`${finding.field}-${finding.line}-${index}`}
                    className="text-caption text-muted-foreground"
                  >
                    {t(($) => $.marketplace.publish.scan_finding, {
                      field: finding.field,
                      line: String(finding.line),
                      rule: finding.rule,
                    })}
                  </li>
                ))}
              </ul>
              {scannerRevision ? (
                <p className="text-caption text-muted-foreground">
                  {t(($) => $.marketplace.publish.scan_revision, {
                    revision: scannerRevision,
                  })}
                </p>
              ) : null}
            </div>
          ) : errorMessage ? (
            <p className="text-caption text-destructive">{errorMessage}</p>
          ) : null}
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={submitting}
          >
            {t(($) => $.marketplace.cancel)}
          </Button>
          <Button onClick={handleSubmit} disabled={!canSubmit}>
            {submitting ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
            {editingLive
              ? t(($) => $.marketplace.publish.save)
              : t(($) => $.marketplace.publish.submit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * A key/value list for headers or env.
 *
 * The value input is a plain text field on purpose: what belongs here is a
 * `${placeholder}` token, not a credential. Masking it would suggest a secret
 * is expected, and the server refuses a literal in a credential-named field
 * anyway.
 */
function PairEditor({
  label,
  idPrefix,
  pairs,
  onChange,
  addLabel,
  removeLabel,
}: {
  label: string;
  idPrefix: string;
  pairs: KeyValueDraft[];
  onChange: (pairs: KeyValueDraft[]) => void;
  addLabel: string;
  removeLabel: string;
}) {
  const { t } = useT("settings");
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <Label>{label}</Label>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => onChange([...pairs, { key: "", value: "" }])}
        >
          <Plus className="h-4 w-4" />
          {addLabel}
        </Button>
      </div>
      {pairs.map((pair, index) => (
        <div key={index} className="flex items-center gap-2">
          <Input
            id={`${idPrefix}-key-${index}`}
            aria-label={t(($) => $.marketplace.publish.pair_key, { label })}
            value={pair.key}
            onChange={(event) =>
              onChange(
                pairs.map((item, i) =>
                  i === index ? { ...item, key: event.target.value } : item,
                ),
              )
            }
            autoComplete="off"
          />
          <Input
            id={`${idPrefix}-value-${index}`}
            aria-label={t(($) => $.marketplace.publish.pair_value, { label })}
            value={pair.value}
            onChange={(event) =>
              onChange(
                pairs.map((item, i) =>
                  i === index ? { ...item, value: event.target.value } : item,
                ),
              )
            }
            autoComplete="off"
          />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label={removeLabel}
            onClick={() => onChange(pairs.filter((_, i) => i !== index))}
          >
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      ))}
    </div>
  );
}
