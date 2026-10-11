/**
 * Structured relations page (RUYI-689) — opened from the issue detail
 * "…" menu ("Relations"), the mobile counterpart of the web sidebar's
 * Relations section (issue-relations-section.tsx). One payload
 * (`GET /api/issues/:id/relations`) supplies parent + the five edge
 * views; children ride on `GET /api/issues/:id/children` so the page can
 * group every structured relation: parent, sub-issues, then the five
 * edge types.
 *
 * Rows reuse the IssueReferenceList row visual (status dot + identifier +
 * title) — the same `<row> ` shape the body mention tail uses — but the
 * data source is entirely the structured relations API, never the
 * markdown extractor (mention semantics stay separated, RUYI-689
 * acceptance 6). Tapping a row pushes that issue's detail route, so back
 * returns here and the original issue stays in the stack.
 *
 * `presentation: "modal"` (registered in the workspace _layout, mirroring
 * issue/[id]/pull-requests): full-page slide-up with a native header;
 * Android system back closes it. Loading / error / empty states mirror
 * the pull-requests page.
 */
import { useMemo } from "react";
import {
  ActivityIndicator,
  Pressable,
  ScrollView,
  View,
} from "react-native";
import { router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import type { IssueRelationRef } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
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

interface RelationGroup {
  key: string;
  label: string;
  refs: IssueRelationRef[];
}

export default function IssueRelationsRoute() {
  const { t } = useT("issues");
  const { id } = useLocalSearchParams<{ id: string }>();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { colorOf } = useIssueStatuses();

  const relations = useQuery(issueRelationsOptions(wsId ?? null, id ?? null));
  const children = useQuery(childIssuesOptions(wsId ?? null, id ?? null));

  const groups = useMemo<RelationGroup[]>(() => {
    const data = relations.data;
    if (!data) return [];
    const out: RelationGroup[] = [];
    if (data.parent) {
      out.push({
        key: "parent",
        label: t("detail.section_parent_issue", "Parent issue"),
        refs: [data.parent],
      });
    }
    const childIssues = children.data?.issues;
    if (childIssues && childIssues.length > 0) {
      out.push({
        key: "children",
        label: t("detail.sub_issues_label", "Sub-issues"),
        refs: childIssues.map((issue) => ({
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
      if (refs.length > 0) out.push({ key, label, refs });
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

  return (
    <View className="flex-1 bg-background">
      {relations.isLoading ? (
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator />
        </View>
      ) : relations.error ? (
        <View className="flex-1 items-center justify-center gap-3 px-6">
          <Text className="text-center text-sm text-destructive">
            {relations.error instanceof Error
              ? relations.error.message
              : String(relations.error)}
          </Text>
          <Button variant="outline" onPress={() => relations.refetch()}>
            <Text>{t("mobile.detail.retry", "Retry")}</Text>
          </Button>
        </View>
      ) : groups.length === 0 ? (
        <View className="flex-1 items-center justify-center px-6">
          <Text className="text-center text-sm text-muted-foreground">
            {t(
              "mobile.detail.relations_empty",
              "No structured relations yet. Blocks, related, and supersede links appear here.",
            )}
          </Text>
        </View>
      ) : (
        <ScrollView showsVerticalScrollIndicator={false}>
          <View className="gap-3 px-4 pb-6 pt-2">
            {groups.map((group) => (
              <View key={group.key}>
                <Text className="mb-1 text-xs font-medium text-muted-foreground">
                  {group.label}
                </Text>
                <View className="gap-0.5">
                  {group.refs.map((relation) => (
                    <Pressable
                      key={relation.id}
                      testID="issue-relation-row"
                      accessibilityRole="link"
                      accessibilityLabel={`${relation.identifier} ${relation.title}`}
                      onPress={() => onOpen(relation)}
                      className="flex-row items-center gap-2 rounded-lg px-2 py-2 active:bg-accent/50"
                    >
                      <View
                        className="h-2 w-2 shrink-0 rounded-full"
                        style={{ backgroundColor: colorOf(relation.status) ?? undefined }}
                      />
                      <Text
                        className="text-sm font-medium text-muted-foreground"
                      >
                        {relation.identifier}
                      </Text>
                      <Text
                        className="flex-1 text-sm text-foreground"
                        numberOfLines={1}
                      >
                        {relation.title}
                      </Text>
                    </Pressable>
                  ))}
                </View>
              </View>
            ))}
          </View>
        </ScrollView>
      )}
    </View>
  );
}
