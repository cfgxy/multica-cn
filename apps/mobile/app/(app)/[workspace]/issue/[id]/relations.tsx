/**
 * Structured relations page (RUYI-689) — opened from the issue detail
 * "…" menu ("Relations"), the mobile counterpart of the web sidebar's
 * Relations section (issue-relations-section.tsx). One payload
 * (`GET /api/issues/:id/relations`) supplies parent + the five edge
 * views; children ride on `GET /api/issues/:id/children` so the page can
 * group every structured relation: parent, sub-issues, then the five
 * edge types.
 *
 * Stage-3 restyle (Owner 2026-10-11 15:13: 列表参考主页任务 TAB，筛选栏
 * 去掉，其余照抄): the list is a SectionList in the Tasks-tab visual
 * system — section headers with per-group counts (tasks.tsx
 * SectionHeader), the inbox row container (`bg-background
 * active:bg-secondary px-4 py-3`), the `h-px bg-border ml-4` separators,
 * the IssuesLoading skeleton, pull-to-refresh, and the same centered
 * empty/error states. No toolbar: the tasks tab's status pills / sort /
 * filter row is exactly the filter bar the Owner dropped; a relations
 * page has nothing to filter anyway. Divergence from the tasks rows,
 * both intentional: no assignee avatar / priority / description / time
 * (the `IssueRelationRef` payload carries only id/identifier/title/
 * status — nothing to render there), and the per-row status dot STAYS
 * (tasks drops it because its section header names a status category;
 * ours names a relation type, so the status would otherwise be lost).
 * Tapping a row pushes that issue's detail route, so back returns here
 * and the original issue stays in the stack.
 *
 * `presentation: "modal"` (registered in the workspace _layout, mirroring
 * issue/[id]/pull-requests): full-page slide-up with a native header;
 * Android system back closes it. Unlike the tasks tab there is no
 * useIsFocused guard on the refresh indicator — a dismissed modal is
 * unmounted, so it never refreshes off-screen.
 */
import { useMemo } from "react";
import { Pressable, SectionList, View } from "react-native";
import { router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import type { IssueRelationRef } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { IssuesLoading } from "@/components/issue/issues-loading";
import {
  childIssuesOptions,
  issueRelationsOptions,
} from "@/data/queries/issues";
import { useWorkspaceStore } from "@/data/workspace-store";
import { resolveLinkAction } from "@/lib/markdown/link-route";
import { useIssueStatuses } from "@/lib/use-issue-statuses";
import { useT } from "@/lib/use-t";

type EdgeGroupKey =
  | "blocks"
  | "blocked_by"
  | "relates_to"
  | "supersedes"
  | "superseded_by";

interface RelationSection {
  key: string;
  label: string;
  // SectionList's section contract field; holds the group's refs.
  data: IssueRelationRef[];
}

export default function IssueRelationsRoute() {
  const { t } = useT("issues");
  const { id } = useLocalSearchParams<{ id: string }>();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { colorOf } = useIssueStatuses();

  const relations = useQuery(issueRelationsOptions(wsId ?? null, id ?? null));
  const children = useQuery(childIssuesOptions(wsId ?? null, id ?? null));

  const sections = useMemo<RelationSection[]>(() => {
    const data = relations.data;
    if (!data) return [];
    const out: RelationSection[] = [];
    if (data.parent) {
      out.push({
        key: "parent",
        label: t("detail.section_parent_issue", "Parent issue"),
        data: [data.parent],
      });
    }
    const childIssues = children.data?.issues;
    if (childIssues && childIssues.length > 0) {
      out.push({
        key: "children",
        label: t("detail.sub_issues_label", "Sub-issues"),
        data: childIssues.map((issue) => ({
          id: issue.id,
          identifier: issue.identifier,
          title: issue.title,
          status: issue.status,
        })),
      });
    }
    const edgeGroups: [EdgeGroupKey, string][] = [
      ["blocks", t("detail.relations_blocks", "Blocks")],
      ["blocked_by", t("detail.relations_blocked_by", "Blocked by")],
      ["relates_to", t("detail.relations_relates_to", "Related")],
      ["supersedes", t("detail.relations_supersedes", "Supersedes")],
      ["superseded_by", t("detail.relations_superseded_by", "Superseded by")],
    ];
    for (const [key, label] of edgeGroups) {
      const refs = data[key];
      if (refs.length > 0) out.push({ key, label, data: refs });
    }
    return out;
  }, [relations.data, children.data?.issues, t]);

  const onOpen = (relation: IssueRelationRef) => {
    const action = resolveLinkAction(
      `mention://issue/${relation.id}`,
      wsSlug ?? null,
    );
    if (action.kind === "route") router.push(action.path);
  };

  const refetch = () => {
    relations.refetch();
    children.refetch();
  };
  const isRefetching = relations.isRefetching || children.isRefetching;

  return (
    <View className="flex-1 bg-background">
      {relations.isLoading ? (
        <IssuesLoading />
      ) : relations.error ? (
        // Same block as the tasks tab: one interpolated sentence, retry
        // below — no "failed:" + details concatenation (CJK word order).
        <View className="px-4 gap-3 pt-4">
          <Text className="text-sm text-destructive">
            {t(
              "my-issues:mobile.page.load_failed",
              "Failed to load issues: {{reason}}",
              {
                reason:
                  relations.error instanceof Error
                    ? relations.error.message
                    : t(
                        "common:mobile.common.unknown_error",
                        "unknown error",
                      ),
              },
            )}
          </Text>
          <Button variant="outline" onPress={() => relations.refetch()}>
            <Text>{t("mobile.detail.retry", "Retry")}</Text>
          </Button>
        </View>
      ) : sections.length === 0 ? (
        <View className="flex-1 items-center justify-center px-6">
          <Text className="text-center text-sm text-muted-foreground">
            {t(
              "mobile.detail.relations_empty",
              "No structured relations yet. Blocks, related, and supersede links appear here.",
            )}
          </Text>
        </View>
      ) : (
        <SectionList
          sections={sections}
          keyExtractor={(item) => item.id}
          stickySectionHeadersEnabled={false}
          ItemSeparatorComponent={() => (
            <View className="h-px bg-border ml-4" />
          )}
          renderSectionHeader={({ section }) => (
            // Tasks-tab SectionHeader visual minus the StatusIcon: a
            // relation type is not a status category, so there is no
            // glyph to resolve — the label + count carry the semantics.
            <View className="flex-row items-center gap-2 px-4 py-2 bg-background">
              <Text className="text-xs uppercase tracking-wider text-muted-foreground font-medium">
                {section.label}
              </Text>
              <Text className="text-xs text-muted-foreground/60">
                {section.data.length}
              </Text>
            </View>
          )}
          contentContainerClassName="pb-2"
          renderItem={({ item }) => (
            <RelationRow relation={item} colorOf={colorOf} onOpen={onOpen} />
          )}
          refreshing={isRefetching}
          onRefresh={refetch}
        />
      )}
    </View>
  );
}

function RelationRow({
  relation,
  colorOf,
  onOpen,
}: {
  relation: IssueRelationRef;
  colorOf: (status: string) => string | null | undefined;
  onOpen: (relation: IssueRelationRef) => void;
}) {
  return (
    // Tasks-tab inbox row container (issue-row-inbox.tsx); the row keeps
    // the status dot because the section header names the relation type,
    // not the status.
    <View className="bg-background active:bg-secondary">
      <Pressable
        testID="issue-relation-row"
        accessibilityRole="link"
        accessibilityLabel={`${relation.identifier} ${relation.title}`}
        onPress={() => onOpen(relation)}
        className="flex-row items-center gap-2 px-4 py-3"
      >
        <View
          className="h-2 w-2 shrink-0 rounded-full"
          style={{ backgroundColor: colorOf(relation.status) ?? undefined }}
        />
        <Text className="text-xs text-muted-foreground shrink-0">
          {relation.identifier}
        </Text>
        <Text
          className="flex-1 text-sm font-semibold text-foreground"
          numberOfLines={1}
        >
          {relation.title}
        </Text>
      </Pressable>
    </View>
  );
}
