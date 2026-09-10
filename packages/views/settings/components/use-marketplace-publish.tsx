"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { ApiError } from "@multica/core/api";
import { useFeatureEnabled } from "@multica/core/config";
import { MARKETPLACE_PUBLISH_V1_FLAG } from "@multica/core/feature-flags";
import { useCurrentMember } from "@multica/core/permissions";
import { marketplaceListingsOptions } from "@multica/core/workspace/queries";
import {
  usePublishMarketplaceListing,
  useUpdateMarketplaceListing,
} from "@multica/core/workspace/mutations";
import type {
  MarketplaceListing,
  MarketplaceScanFinding,
} from "@multica/core/types";
import { useT } from "../../i18n";
import type { MarketplacePublishSubmit } from "./marketplace-publish-dialog";

/**
 * The publish/update half of the marketplace, usable from anywhere a skill or
 * MCP entry is listed.
 *
 * It exists because publishing started life inside the marketplace tab, which
 * left the only way to publish an EXISTING skill or MCP server being to retype
 * it into a blank form. The state here — which dialog is open, what the last
 * submit was rejected for — is the same wherever the flow is started from, so
 * the tab and the entity rows share it rather than each growing a copy.
 *
 * What it deliberately does NOT do is read the entity being published. An MCP
 * server's config column is write-only; a publish entry may pass the name and
 * transport the server already reports, and the template stays something the
 * publisher writes by hand.
 */
export interface MarketplacePublishFlow {
  /** Whether this member may publish at all — admin/owner plus the write flag. */
  canPublish: boolean;
  /** The listing this workspace already has for a kind/name, if any. */
  listingFor: (kind: string, name: string) => MarketplaceListing | null;
  open: (kind: string, listing: MarketplaceListing | null) => void;
  close: () => void;
  submit: (input: MarketplacePublishSubmit) => Promise<void>;
  /** Spread onto MarketplacePublishDialog; `open`/`onSubmit` included. */
  dialogProps: {
    open: boolean;
    kind: string;
    listing: MarketplaceListing | null;
    submitting: boolean;
    findings: MarketplaceScanFinding[];
    scannerRevision: string;
    errorMessage: string;
    onOpenChange: (open: boolean) => void;
    onSubmit: (input: MarketplacePublishSubmit) => void;
  };
}

export function useMarketplacePublishFlow(wsId: string): MarketplacePublishFlow {
  const { t } = useT("settings");
  const currentMember = useCurrentMember(wsId);
  const publishEnabled = useFeatureEnabled(MARKETPLACE_PUBLISH_V1_FLAG, false);
  const canPublish =
    (currentMember.role === "owner" || currentMember.role === "admin") &&
    publishEnabled;

  const listingsQuery = useQuery({
    ...marketplaceListingsOptions(wsId),
    enabled: wsId !== "" && canPublish,
  });
  const listings = useMemo(() => listingsQuery.data ?? [], [listingsQuery.data]);

  const publish = usePublishMarketplaceListing(wsId);
  const update = useUpdateMarketplaceListing(wsId);

  // `kind` doubles as the open flag: a dialog is open exactly when a kind has
  // been chosen, and editing carries the listing alongside it.
  const [kind, setKind] = useState("");
  const [editing, setEditing] = useState<MarketplaceListing | null>(null);
  const [findings, setFindings] = useState<MarketplaceScanFinding[]>([]);
  const [scannerRevision, setScannerRevision] = useState("");
  const [publishError, setPublishError] = useState("");

  const clearErrors = () => {
    setFindings([]);
    setScannerRevision("");
    setPublishError("");
  };

  const close = () => {
    setKind("");
    setEditing(null);
    clearErrors();
  };

  const open = (nextKind: string, listing: MarketplaceListing | null) => {
    clearErrors();
    setEditing(listing);
    setKind(nextKind);
  };

  // Names are unique per kind, so the entity a row shows and the listing that
  // published it are matched on exactly that pair. Case-insensitively, because
  // the server reserves the lowercased name.
  const listingFor = (entityKind: string, name: string) => {
    const wanted = name.trim().toLowerCase();
    return (
      listings.find(
        (listing) =>
          listing.kind === entityKind &&
          listing.name.toLowerCase() === wanted,
      ) ?? null
    );
  };

  const submit = async (input: MarketplacePublishSubmit) => {
    clearErrors();
    try {
      // A withdrawn listing cannot be edited — the server refuses a PATCH on a
      // tombstone and says to publish again instead, which is what republish
      // does: the same name goes back through publish and revives the row this
      // workspace still owns.
      if (editing && editing.state !== "withdrawn") {
        // `kind` is fixed at publish time and the update route does not accept
        // it, so it is dropped rather than sent as a field the server ignores.
        const { kind: _kind, ...editable } = input;
        await update.mutateAsync({
          id: editing.id,
          revision: editing.revision,
          ...editable,
        });
        toast.success(t(($) => $.marketplace.updated_toast, { name: input.name }));
      } else {
        await publish.mutateAsync(input);
        toast.success(t(($) => $.marketplace.published_toast, { name: input.name }));
      }
      close();
    } catch (error) {
      // A 422 is the secret scan refusing the content. Its body carries the
      // findings, which are locations only — field, line, rule — so they can be
      // shown without echoing whatever was pasted.
      const scan = scanErrorOf(error);
      if (scan) {
        setFindings(scan.findings);
        setScannerRevision(scan.scannerRevision);
        return;
      }
      setPublishError(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.marketplace.publish_failed_toast),
      );
    }
  };

  return {
    canPublish,
    listingFor,
    open,
    close,
    submit,
    dialogProps: {
      open: kind !== "",
      kind,
      listing: editing,
      submitting: publish.isPending || update.isPending,
      findings,
      scannerRevision,
      errorMessage: publishError,
      onOpenChange: (next: boolean) => {
        if (!next) close();
      },
      onSubmit: (input: MarketplacePublishSubmit) => void submit(input),
    },
  };
}

/**
 * Pulls the secret-scan report out of a rejected publish.
 *
 * Only a 422 carries one, and only the location fields are read: taking the
 * whole body would risk rendering something the server did not intend to be
 * displayed. Anything else — a 409, a 403, a malformed body — returns null and
 * falls through to the plain error message.
 */
export function scanErrorOf(
  error: unknown,
): { findings: MarketplaceScanFinding[]; scannerRevision: string } | null {
  if (!(error instanceof ApiError) || error.status !== 422) return null;
  const body = error.body;
  if (!body || typeof body !== "object") return null;
  const raw = (body as { findings?: unknown }).findings;
  if (!Array.isArray(raw)) return null;
  const findings = raw.flatMap((entry) => {
    if (!entry || typeof entry !== "object") return [];
    const finding = entry as Record<string, unknown>;
    return [
      {
        category: typeof finding.category === "string" ? finding.category : "",
        rule: typeof finding.rule === "string" ? finding.rule : "",
        field: typeof finding.field === "string" ? finding.field : "",
        line: typeof finding.line === "number" ? finding.line : 0,
        mask: typeof finding.mask === "string" ? finding.mask : "",
      },
    ];
  });
  if (findings.length === 0) return null;
  const revision = (body as { scanner_revision?: unknown }).scanner_revision;
  return {
    findings,
    scannerRevision: typeof revision === "string" ? revision : "",
  };
}
