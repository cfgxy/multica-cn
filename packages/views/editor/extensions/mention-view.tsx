"use client";

/**
 * MentionView — NodeView for rendering @mentions inline in the editor.
 *
 * Member/agent mentions: plain "@Name" text with .mention class styling.
 * Issue/project mentions render the same navigable chips as readonly content
 * (IssueMentionCard / ProjectMentionCard), so click behavior — plain click,
 * modifier click, middle click — cannot drift between an editing and a
 * readonly surface. The editor's ProseMirror click handler skips anything
 * inside `[data-node-view-wrapper]`, so the AppLink inside the card owns the
 * click alone.
 *
 * Exception (RUYI-635): a host may opt into `demoteIssueMentions` — the issue
 * body, whose editor IS the rendered content, must read like RichContent's
 * markdown surfaces — issue mentions as plain text, navigation in the tail
 * reference list (rendered by the host next to the editor).
 *
 * Issue chip sizing: must fit within the paragraph line box (14px * 1.625 =
 * 22.75px). Card is text-caption (12px) + py-0.5 + border ≈ 22px total. The
 * `vertical-align: middle` rule on `[data-node-view-wrapper]` in CSS handles
 * line-box alignment; setting it on an inner element has no effect because
 * the wrapper is the outermost inline element.
 */

import { NodeViewWrapper } from "@tiptap/react";
import type { NodeViewProps } from "@tiptap/react";
import { isIssueIdentifier } from "@multica/ui/markdown";
import { IssueMentionCard } from "../../issues/components/issue-mention-card";
import { ProjectMentionCard } from "../../projects/components/project-mention-card";

/**
 * Plain-text display for a demoted issue mention (RUYI-635). Same contract as
 * RichContent's markdown surfaces and core's `demoteIssueMentionLinks`: an
 * identifier-form mention shows the identifier, a UUID mention shows its
 * authored label, and an empty label falls back to the id segment so the
 * reference never vanishes from the prose. The node stays an atom — deleting
 * it is one keystroke, exactly like the chip it replaces.
 */
function DemotedIssueMentionView({ node }: Pick<NodeViewProps, "node">) {
  const ref = String(node.attrs.id ?? "");
  const label = node.attrs.label ? String(node.attrs.label) : "";
  return (
    <NodeViewWrapper as="span" className="inline" data-issue-mention-text="">
      {isIssueIdentifier(ref) ? ref : label || ref}
    </NodeViewWrapper>
  );
}

export function MentionView({ node, extension }: NodeViewProps) {
  const { type, id, label } = node.attrs;

  // stopPropagation mirrors the readonly renderer's mention wrappers: a chip
  // click must not reach surrounding click handlers.
  if (type === "issue") {
    if (extension?.options?.demoteIssueMentions === true) {
      return <DemotedIssueMentionView node={node} />;
    }
    return (
      <NodeViewWrapper
        as="span"
        className="inline"
        onClick={(e: React.MouseEvent) => e.stopPropagation()}
      >
        <IssueMentionCard issueId={id} fallbackLabel={label} />
      </NodeViewWrapper>
    );
  }

  if (type === "project") {
    return (
      <NodeViewWrapper
        as="span"
        className="inline"
        onClick={(e: React.MouseEvent) => e.stopPropagation()}
      >
        <ProjectMentionCard projectId={id} fallbackLabel={label} />
      </NodeViewWrapper>
    );
  }

  return (
    <NodeViewWrapper as="span" className="inline">
      <span className="mention">@{label ?? id}</span>
    </NodeViewWrapper>
  );
}
