"use client";

import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { IssueReference } from "@multica/core/markdown";
import { IssueReferenceFooter, useIssueReferences } from "./issue-reference-footer";
import { useCurrentWorkspace } from "@multica/core/paths";
import { useCurrentIssueRenderContext } from "../issues/current-issue-render-context";
import { saveTailMeasurement, useIssueReferenceMeasurements } from "./issue-reference-layout";
import { useRichContentScrollRoot } from "./scroll-root";

type ScrollAdjustment = { manual: number; claimed: number };

const scrollAdjustments = new WeakMap<object, ScrollAdjustment>();

function getScrollAdjustment(target: object): ScrollAdjustment {
  let adjustment = scrollAdjustments.get(target);
  if (!adjustment) {
    adjustment = { manual: 0, claimed: 0 };
    scrollAdjustments.set(target, adjustment);
  }
  return adjustment;
}

export function LazyIssueReferenceTail({ content }: { content: string }) {
  const references = useIssueReferences(content);
  if (references.length === 0) return null;
  return <MeasuredIssueReferenceTail content={content} references={references} />;
}

function MeasuredIssueReferenceTail({ content, references }: { content: string; references: IssueReference[] }) {
  const root = useRichContentScrollRoot();
  const workspace = useCurrentWorkspace();
  const issue = useCurrentIssueRenderContext();
  const sharedMeasurements = useIssueReferenceMeasurements();
  const localMeasurements = useRef<NonNullable<ReturnType<typeof useIssueReferenceMeasurements>>>(new Map());
  const measurements = sharedMeasurements ?? localMeasurements.current;
  const cacheKey = JSON.stringify([workspace?.id, workspace?.issue_prefix, issue?.id, content]);
  const [layout, setLayout] = useState("");
  const [measurementRevision, setMeasurementRevision] = useState(0);
  const cached = measurements.get(cacheKey);
  const reservedHeight = cached?.layout === layout ? cached.height : undefined;
  const estimatedRows = references.filter((reference) =>
    !((reference.form === "mention" && reference.ref === issue?.id) ||
      (reference.form === "identifier" && reference.ref === issue?.identifier)),
  ).length;
  const ref = useRef<HTMLDivElement>(null);
  const [nearViewport, setNearViewport] = useState(false);
  const visibility = useRef(false);
  const mountedHeight = useRef(0);
  const pendingScroll = useRef<{
    top: number;
    windowRoot: boolean;
    manualAdjustment: number;
    claimedAdjustment: number;
    selectionRange: Range | null;
    selectionText: string;
    selectionTop: number | null;
  } | null>(null);

  useLayoutEffect(() => {
    const element = ref.current;
    if (!element) return;
    const inspect = () => {
      const style = getComputedStyle(element);
      const signature = JSON.stringify([
        element.clientWidth, style.font, style.letterSpacing,
        style.getPropertyValue("--text-body"),
        style.getPropertyValue("--text-body--line-height"),
        style.getPropertyValue("--text-micro--line-height"),
        getComputedStyle(document.documentElement).fontSize,
      ]);
      setLayout(signature);
      if (nearViewport) {
        const height = element.scrollHeight;
        const previous = measurements.get(cacheKey);
        if (previous?.layout !== signature || previous.height !== height) {
          saveTailMeasurement(measurements, cacheKey, { layout: signature, height });
          setMeasurementRevision((revision) => revision + 1);
        }
      }
    };
    inspect();
    const resize = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(inspect);
    resize?.observe(element);
    const attributes = new MutationObserver(inspect);
    attributes.observe(document.documentElement, { attributes: true });
    attributes.observe(document.body, { attributes: true });
    document.fonts?.addEventListener("loadingdone", inspect);
    return () => {
      resize?.disconnect();
      attributes.disconnect();
      document.fonts?.removeEventListener("loadingdone", inspect);
    };
  }, [nearViewport, cacheKey, measurements]);

  useLayoutEffect(() => {
    const element = ref.current;
    if (!element) return;

    const heightDelta = element.scrollHeight - mountedHeight.current;
    mountedHeight.current = element.scrollHeight;
    const pending = pendingScroll.current;
    pendingScroll.current = null;
    if (!pending || heightDelta === 0) return;

    // Preserve the native find range itself when several tail updates race
    // browser anchoring; scroll deltas cannot identify which sibling moved it.
    const selection = window.getSelection();
    if (
      pending.selectionRange &&
      pending.selectionTop !== null &&
      selection &&
      !selection.isCollapsed &&
      selection.toString() === pending.selectionText &&
      pending.selectionRange.startContainer.isConnected
    ) {
      const selectionTop = pending.selectionRange.getBoundingClientRect().top;
      const correction = selectionTop - pending.selectionTop;
      if (correction !== 0) {
        if (pending.windowRoot) {
          window.scrollBy(0, correction);
        } else if (root) {
          root.scrollTop += correction;
        }
      }
      return;
    }

    if (element.getBoundingClientRect().top > 0) return;

    // Native find can scroll past this shell before its footer mounts. Keep the
    // found content stationary, accounting for native anchoring and sibling
    // tails that share this scroll baseline.
    const currentTop = pending.windowRoot ? window.scrollY : root?.scrollTop ?? 0;
    const scrollTarget = pending.windowRoot ? window : root;
    const adjustment = scrollTarget ? getScrollAdjustment(scrollTarget) : null;
    const manualSinceSnapshot = adjustment
      ? adjustment.manual - pending.manualAdjustment
      : 0;
    const claimedSinceSnapshot = adjustment
      ? adjustment.claimed - pending.claimedAdjustment
      : 0;
    const alreadyAdjusted =
      currentTop - pending.top - manualSinceSnapshot - claimedSinceSnapshot;
    if (adjustment) adjustment.claimed += alreadyAdjusted;
    const correction = heightDelta - alreadyAdjusted;
    if (correction === 0) return;
    if (pending.windowRoot) {
      window.scrollBy(0, correction);
    } else if (root) {
      root.scrollTop += correction;
    }
    if (adjustment) adjustment.manual += correction;
  }, [nearViewport, root, layout, measurementRevision]);

  useEffect(() => {
    if (typeof window === "undefined" || !window.IntersectionObserver) {
      setNearViewport(true);
      return;
    }

    const element = ref.current;
    if (!element) return;
    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries.find((candidate) => candidate.target === element);
        if (entry && entry.isIntersecting !== visibility.current) {
          visibility.current = entry.isIntersecting;
          const adjustment = getScrollAdjustment(root ?? window);
          const selection = window.getSelection();
          const selectionRange = selection && !selection.isCollapsed
            ? selection.getRangeAt(0).cloneRange()
            : null;
          pendingScroll.current = {
            top: root ? root.scrollTop : window.scrollY,
            windowRoot: !root,
            manualAdjustment: adjustment.manual,
            claimedAdjustment: adjustment.claimed,
            selectionRange,
            selectionText: selectionRange?.toString() ?? "",
            selectionTop: selectionRange?.getBoundingClientRect().top ?? null,
          };
          setNearViewport(entry.isIntersecting);
        }
      },
      { root, rootMargin: "600px 0px" },
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, [root]);

  return (
    <div
      ref={ref}
      data-issue-reference-tail-shell=""
      style={{ display: "flow-root", height: nearViewport ? undefined : reservedHeight }}
    >
      {nearViewport ? <IssueReferenceFooter references={references} /> : reservedHeight === undefined && estimatedRows > 0 ? (
        <div
          aria-hidden="true"
          data-issue-reference-placeholder=""
          style={{ height: `calc(0.75rem + 2px + var(--text-micro--line-height, 15px) + 1rem + ${estimatedRows} * (max(15px, var(--text-body--line-height, 20px)) + 1rem + 1px))` }}
        />
      ) : null}
    </div>
  );
}
