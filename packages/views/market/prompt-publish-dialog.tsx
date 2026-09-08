"use client";

import { useEffect, useState } from "react";
import { AlertTriangle, Check, Loader2, ShieldAlert } from "lucide-react";
import { toast } from "sonner";
import { Alert, AlertDescription, AlertTitle } from "@multica/ui/components/ui/alert";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { ApiError } from "@multica/core/api";
import {
  useCreatePromptVersion,
  usePublishPromptVersion,
  useUpdatePromptVersion,
} from "@multica/core/workspace/mutations";
import type { PromptSecretScanBlocked, PromptVersion } from "@multica/core/types";
import { useT } from "../i18n";
import { ChoiceList } from "./choice-list";
import { LICENSE_CODES, licenseLabel } from "./prompt-market-labels";

type Step = "content" | "metadata" | "scan" | "visibility";

type ScanState =
  | { phase: "idle" }
  | { phase: "running" }
  | { phase: "passed"; version: PromptVersion }
  | { phase: "blocked"; blocked: PromptSecretScanBlocked };

/**
 * The publish wizard on an agent or squad prompt tab.
 *
 * The security check is not a preflight the user can walk past. There is no
 * scan endpoint separate from publishing: step 3 attempts the real publish,
 * and a hit comes back as a 422 that leaves the draft unpublished. That is why
 * the blocked panel offers no "publish anyway" — the only exits are editing
 * the prompt at the source and re-snapshotting, or cancelling.
 *
 * Findings are rendered from `category`, `rule`, `line` and the server's
 * fixed-width `mask`. The matched text never leaves the server, so a
 * screenshot of this panel cannot leak the credential it is reporting.
 */
export function PromptPublishDialog({
  open,
  wsId,
  sourceType,
  sourceId,
  /** Set to append a new version onto an existing line. */
  seriesId,
  defaultName,
  onOpenChange,
}: {
  open: boolean;
  wsId: string;
  sourceType: "agent" | "squad";
  sourceId: string;
  seriesId?: string;
  defaultName: string;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("prompt-market");

  const [step, setStep] = useState<Step>("content");
  const [name, setName] = useState(defaultName);
  const [summary, setSummary] = useState("");
  const [audience, setAudience] = useState("");
  const [categories, setCategories] = useState("");
  const [licenseCode, setLicenseCode] = useState("");
  const [usageNotes, setUsageNotes] = useState("");
  const [companions, setCompanions] = useState("");
  const [visibility, setVisibility] = useState<"private" | "public">("private");
  const [draft, setDraft] = useState<PromptVersion | null>(null);
  const [scan, setScan] = useState<ScanState>({ phase: "idle" });
  const [failure, setFailure] = useState<string | null>(null);

  const createDraft = useCreatePromptVersion(wsId);
  const updateDraft = useUpdatePromptVersion(wsId);
  const publish = usePublishPromptVersion(wsId);

  useEffect(() => {
    if (open) {
      setStep("content");
      setName(defaultName);
      setSummary("");
      setAudience("");
      setCategories("");
      setLicenseCode("");
      setUsageNotes("");
      setCompanions("");
      setVisibility("private");
      setDraft(null);
      setScan({ phase: "idle" });
      setFailure(null);
    }
  }, [open, defaultName, sourceId]);

  const metadataComplete =
    name.trim() !== "" && summary.trim() !== "" && licenseCode !== "";

  const parsedCategories = categories
    .split(",")
    .map((entry) => entry.trim())
    .filter(Boolean);

  /**
   * Step 2 → 3. Opens the draft (or updates it on a second pass) and then
   * attempts the publish that carries the scan.
   *
   * `visibility` is deliberately not consulted here: the private/public choice
   * is step 4, and the scan must clear before it is even offered.
   */
  const runScan = async () => {
    setFailure(null);
    setScan({ phase: "running" });
    try {
      const version = draft
        ? await updateDraft.mutateAsync({
            versionId: draft.id,
            name: name.trim(),
            summary: summary.trim(),
            audience: audience.trim(),
            categories: parsedCategories,
            license_code: licenseCode,
            usage_notes: usageNotes.trim(),
            companions: companions.trim(),
            // A second pass re-snapshots, so a publisher who fixed a flagged
            // line at the source is scanning the corrected text.
            refresh_content: true,
          })
        : await createDraft.mutateAsync({
            source_type: sourceType,
            source_id: sourceId,
            series_id: seriesId,
            name: name.trim(),
            summary: summary.trim(),
            audience: audience.trim(),
            categories: parsedCategories,
            license_code: licenseCode,
            usage_notes: usageNotes.trim(),
            companions: companions.trim(),
          });
      setDraft(version);
      setStep("scan");

      // The private publish is the scan: it is the only call that runs the
      // scanner, and it leaves the version private, so step 4 still decides
      // whether the world sees it.
      const published = await publish.mutateAsync({
        versionId: version.id,
        public: false,
      });
      setScan({ phase: "passed", version: published });
    } catch (error) {
      const blocked = secretScanBody(error);
      if (blocked) {
        setScan({ phase: "blocked", blocked });
        setStep("scan");
        return;
      }
      setScan({ phase: "idle" });
      setFailure(errorMessage(error, t(($) => $.publish.failed_toast)));
      setStep("metadata");
    }
  };

  /** Step 4. Private is already the stored state; only public needs a write. */
  const finish = async () => {
    if (scan.phase !== "passed") return;
    setFailure(null);
    try {
      const version =
        visibility === "public"
          ? await publish.mutateAsync({ versionId: scan.version.id, public: true })
          : scan.version;
      toast.success(
        t(($) => $.publish.published_toast, { version: version.version ?? 1 }),
      );
      onOpenChange(false);
    } catch (error) {
      setFailure(errorMessage(error, t(($) => $.publish.failed_toast)));
    }
  };

  const busy =
    createDraft.isPending || updateDraft.isPending || publish.isPending;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t(($) => $.publish.title)}</DialogTitle>
          <DialogDescription>
            {t(($) => $.publish.step_of_label, {
              step: stepLabel(t, step),
            })}
          </DialogDescription>
        </DialogHeader>

        <div className="max-h-[60vh] space-y-4 overflow-y-auto">
          {step === "content" ? (
            <p className="text-caption leading-5 text-muted-foreground">
              {t(($) => $.publish.content_note)}
            </p>
          ) : null}

          {step === "metadata" ? (
            <>
              <Field
                id="prompt-publish-name"
                label={t(($) => $.publish.name)}
                value={name}
                onChange={setName}
              />
              <Field
                id="prompt-publish-summary"
                label={t(($) => $.publish.summary)}
                value={summary}
                onChange={setSummary}
                multiline
              />
              <Field
                id="prompt-publish-audience"
                label={t(($) => $.publish.audience)}
                optionalLabel={t(($) => $.publish.optional)}
                value={audience}
                onChange={setAudience}
              />
              <Field
                id="prompt-publish-categories"
                label={t(($) => $.publish.categories)}
                optionalLabel={t(($) => $.publish.optional)}
                placeholder={t(($) => $.publish.categories_placeholder)}
                value={categories}
                onChange={setCategories}
              />
              <div className="space-y-1.5">
                <Label htmlFor="prompt-publish-license">
                  {t(($) => $.publish.license)}
                </Label>
                <Select
                  items={LICENSE_CODES.map((code) => ({
                    value: code,
                    label: licenseLabel(t, code),
                  }))}
                  value={licenseCode}
                  // The primitive can emit null on a clear; an empty string
                  // keeps "no licence chosen" as one representable state
                  // rather than two the required-field check has to know about.
                  onValueChange={(value) => setLicenseCode(value ?? "")}
                >
                  <SelectTrigger id="prompt-publish-license">
                    <SelectValue
                      placeholder={t(($) => $.publish.license_placeholder)}
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {LICENSE_CODES.map((code) => (
                      <SelectItem key={code} value={code}>
                        {licenseLabel(t, code)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <Field
                id="prompt-publish-usage-notes"
                label={t(($) => $.publish.usage_notes)}
                optionalLabel={t(($) => $.publish.optional)}
                value={usageNotes}
                onChange={setUsageNotes}
                multiline
              />
              <Field
                id="prompt-publish-companions"
                label={t(($) => $.publish.companions)}
                optionalLabel={t(($) => $.publish.optional)}
                value={companions}
                onChange={setCompanions}
                description={t(($) => $.publish.companions_note)}
              />
              <p className="text-caption text-muted-foreground">
                {t(($) => $.publish.required_note)}
              </p>
              <p className="text-caption text-muted-foreground">
                {t(($) => $.publish.publisher_note)}
              </p>
            </>
          ) : null}

          {step === "scan" ? <ScanPanel state={scan} /> : null}

          {step === "visibility" ? (
            <>
              <ChoiceList
                name="prompt-publish-visibility"
                value={visibility}
                onChange={setVisibility}
                options={[
                  {
                    value: "private",
                    label: t(($) => $.publish.visibility_private),
                  },
                  {
                    value: "public",
                    label: t(($) => $.publish.visibility_public),
                  },
                ]}
              />
              {visibility === "public" ? (
                <Alert>
                  <AlertTriangle />
                  <AlertDescription>
                    {t(($) => $.publish.public_warning)}
                  </AlertDescription>
                </Alert>
              ) : null}
            </>
          ) : null}

          {failure ? (
            <Alert variant="destructive">
              <AlertTriangle />
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          ) : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            {t(($) => $.publish.cancel)}
          </Button>

          {step === "content" ? (
            <Button onClick={() => setStep("metadata")}>
              {t(($) => $.apply.next)}
            </Button>
          ) : null}

          {step === "metadata" ? (
            <Button disabled={!metadataComplete || busy} onClick={() => void runScan()}>
              {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
              {t(($) => $.apply.next)}
            </Button>
          ) : null}

          {step === "scan" ? (
            scan.phase === "blocked" ? (
              // No forward action exists in this state on purpose.
              <Button variant="outline" onClick={() => setStep("metadata")}>
                {t(($) => $.apply.back)}
              </Button>
            ) : (
              <Button
                disabled={scan.phase !== "passed"}
                onClick={() => setStep("visibility")}
              >
                {t(($) => $.apply.next)}
              </Button>
            )
          ) : null}

          {step === "visibility" ? (
            <Button
              variant={visibility === "public" ? "destructive" : "default"}
              disabled={busy}
              onClick={() => void finish()}
            >
              {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
              {visibility === "public"
                ? t(($) => $.publish.publish)
                : t(($) => $.publish.save_draft)}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ScanPanel({ state }: { state: ScanState }) {
  const { t } = useT("prompt-market");

  if (state.phase === "running" || state.phase === "idle") {
    return (
      <div className="flex items-center justify-center gap-2 py-8 text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
        <span className="text-caption">{t(($) => $.publish.scan_running)}</span>
      </div>
    );
  }

  if (state.phase === "passed") {
    return (
      <Alert>
        <Check />
        <AlertTitle>{t(($) => $.publish.scan_pass_title)}</AlertTitle>
        {/* A pass is stated as "no rule matched", never as "this is safe" —
            the publisher stays responsible for reading their own prompt. */}
        <AlertDescription>
          {t(($) => $.publish.scan_pass_description, {
            revision: state.version.scanner_revision,
          })}
        </AlertDescription>
      </Alert>
    );
  }

  return (
    <div className="space-y-3">
      <Alert variant="destructive">
        <ShieldAlert />
        <AlertTitle>{t(($) => $.publish.scan_blocked_title)}</AlertTitle>
        <AlertDescription>
          {t(($) => $.publish.scan_blocked_description)}
        </AlertDescription>
      </Alert>
      {state.blocked.truncated ? (
        <p className="text-caption text-muted-foreground">
          {t(($) => $.publish.scan_truncated)}
        </p>
      ) : null}
      <ul className="divide-y divide-surface-border rounded-lg border">
        {state.blocked.findings.map((finding, index) => (
          <li
            key={`${finding.rule}-${finding.line}-${index}`}
            className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 px-3 py-2"
          >
            <span className="text-body font-medium">{finding.category}</span>
            <span className="text-caption text-muted-foreground">{finding.rule}</span>
            <span className="text-caption text-muted-foreground">
              {t(($) => $.publish.finding_line, { line: finding.line })}
            </span>
            {/* `mask` is a fixed-width mask minted server-side, not a prefix
                of the matched value. */}
            <span className="font-mono text-caption text-faint-foreground">
              {finding.mask}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function Field({
  id,
  label,
  value,
  onChange,
  optionalLabel,
  placeholder,
  description,
  multiline,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  optionalLabel?: string;
  placeholder?: string;
  description?: string;
  multiline?: boolean;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>
        {label}
        {optionalLabel ? (
          <span className="ml-1 text-caption font-normal text-muted-foreground">
            {optionalLabel}
          </span>
        ) : null}
      </Label>
      {multiline ? (
        <Textarea
          id={id}
          value={value}
          placeholder={placeholder}
          onChange={(event) => onChange(event.target.value)}
        />
      ) : (
        <Input
          id={id}
          value={value}
          placeholder={placeholder}
          autoComplete="off"
          onChange={(event) => onChange(event.target.value)}
        />
      )}
      {description ? (
        <p className="text-caption leading-5 text-muted-foreground">{description}</p>
      ) : null}
    </div>
  );
}

function stepLabel(t: ReturnType<typeof useT<"prompt-market">>["t"], step: Step): string {
  switch (step) {
    case "content":
      return t(($) => $.publish.step_content);
    case "metadata":
      return t(($) => $.publish.step_metadata);
    case "scan":
      return t(($) => $.publish.step_scan);
    case "visibility":
      return t(($) => $.publish.step_visibility);
  }
}

/** The 422 body a secret-scan block returns, or null for any other failure. */
function secretScanBody(error: unknown): PromptSecretScanBlocked | null {
  if (!(error instanceof ApiError) || error.status !== 422) return null;
  const body = error.body;
  if (!body || typeof body !== "object") return null;
  const candidate = body as Partial<PromptSecretScanBlocked>;
  if (candidate.code !== "prompt_secret_detected") return null;
  return {
    code: candidate.code,
    error: candidate.error ?? "",
    scanner_revision: candidate.scanner_revision ?? "",
    findings: Array.isArray(candidate.findings) ? candidate.findings : [],
    truncated: candidate.truncated === true,
  };
}

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}
