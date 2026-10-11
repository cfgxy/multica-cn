/**
 * Tail aggregation of issue references (RUYI-635, mobile half).
 *
 * Web renders every issue mention as plain body text and lists the collected
 * references in a footer (`IssueReferenceFooter`); mobile's demote pass
 * (`lib/markdown/preprocess.ts`) applies the same body-text contract, and
 * this component is the tail list: one tappable row per resolved issue —
 * `<status dot> <identifier> <title>` — and a plain-text slot for every
 * unresolved one, at first-mention order. The same issue reached via a UUID
 * mention and its bare identifier collapses into one row (cross-form merge
 * needs the resolved issue id, so it happens here, not in the extractor).
 *
 * Rows navigate through `resolveLinkAction` (lib/markdown/link-route.ts) —
 * the same pure decision a body mention link used to take, so the tail list
 * can never disagree with it about where a reference goes. A row that cannot
 * resolve (404, wrong workspace prefix, still loading) is never tappable:
 * degradation is plain text, not a dead link.
 *
 * Extraction happens once in `Markdown` (raw content, shared core extractor);
 * this component only resolves and renders.
 */
import { useMemo } from "react";
import { Pressable, Text, View } from "react-native";
import { router } from "expo-router";
import { useQueries } from "@tanstack/react-query";
import type { IssueReference } from "@multica/core/markdown";
import type { Issue } from "@multica/core/types";
import {
  issueDetailOptions,
  issueIdentifierOptions,
} from "@/data/queries/issues";
import { useWorkspaceStore } from "@/data/workspace-store";
import { resolveLinkAction } from "@/lib/markdown/link-route";
import { useIssueStatuses } from "@/lib/use-issue-statuses";
import { useT } from "@/lib/use-t";

interface ResolvedRow {
  key: string;
  issue: Issue;
}

interface DegradedRow {
  key: string;
  fallback: string;
}

export function IssueReferenceList({
  references,
}: {
  references: IssueReference[];
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { colorOf } = useIssueStatuses();

  const results = useQueries({
    queries: useMemo(
      () =>
        references.map((ref) =>
          ref.form === "identifier"
            ? issueIdentifierOptions(wsId, ref.ref)
            : issueDetailOptions(wsId, ref.ref),
        ),
      [references, wsId],
    ),
    // Zip positions with `references` below; a failed / loading query reads
    // as unresolved and degrades, the same "every miss looks alike" contract
    // the web footer follows.
    combine: (results) => results.map((r) => r.data ?? null),
  });

  const rows = useMemo(() => {
    const out: (ResolvedRow | DegradedRow)[] = [];
    const seenIssueIds = new Set<string>();
    references.forEach((token, i) => {
      const issue = results[i] ?? null;
      if (issue) {
        if (seenIssueIds.has(issue.id)) return;
        seenIssueIds.add(issue.id);
        out.push({ key: `${token.form}:${token.ref}`, issue });
      } else {
        out.push({
          key: `${token.form}:${token.ref}`,
          fallback: token.label || token.ref,
        });
      }
    });
    return out;
  }, [references, results]);

  if (references.length === 0) return null;

  return (
    <View
      className="gap-1 border-t border-border/60 pt-2"
      accessibilityLabel={t("detail.referenced_issues", "Referenced issues")}
    >
      {rows.map((row) =>
        "issue" in row ? (
          <Pressable
            key={row.key}
            testID="issue-reference-row"
            className="flex-row items-center gap-2 py-0.5"
            accessibilityRole="link"
            accessibilityLabel={`${row.issue.identifier} ${row.issue.title}`}
            onPress={() => {
              const action = resolveLinkAction(
                `mention://issue/${row.issue.id}`,
                wsSlug ?? null,
              );
              if (action.kind === "route") router.push(action.path);
            }}
          >
            <View
              className="h-2 w-2 shrink-0 rounded-full"
              style={{ backgroundColor: colorOf(row.issue.status) }}
            />
            <Text
              className="text-sm font-medium text-muted-foreground"
              translate="no"
            >
              {row.issue.identifier}
            </Text>
            <Text className="flex-1 text-sm text-foreground" numberOfLines={1}>
              {row.issue.title}
            </Text>
          </Pressable>
        ) : (
          <Text
            key={row.key}
            className="py-0.5 text-sm text-muted-foreground"
            numberOfLines={1}
          >
            {row.fallback}
          </Text>
        ),
      )}
    </View>
  );
}
