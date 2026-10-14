/**
 * Structured issue relations (RUYI-689) — the read model behind
 * `GET /api/issues/{id}/relations`. Mirrors `IssueRelationsResponse` in
 * server/internal/handler/issue_relation.go: the anchor issue plus its
 * parent and the five edge views. Parent/children also have dedicated
 * first-class surfaces (sidebar Parent block, sub-issues section); this
 * response carries `parent` so mobile's grouped "Relations" page can
 * render every structured relation from one payload.
 */

/** One related issue — identifier + title + status, ready to render as a
 *  tappable row. `status` is the raw server status key; render through the
 *  status catalog so custom statuses degrade gracefully. */
export interface IssueRelationRef {
  id: string;
  identifier: string;
  title: string;
  status: string;
}

export interface IssueRelationsResponse {
  issue_id: string;
  identifier: string;
  /** Anchor issue revision at read time — informational. */
  revision: number;
  parent: IssueRelationRef | null;
  /** Issues this issue blocks (they are blocked_by this one). */
  blocks: IssueRelationRef[];
  /** Issues that block this issue. */
  blocked_by: IssueRelationRef[];
  relates_to: IssueRelationRef[];
  /** Issues this issue supersedes. */
  supersedes: IssueRelationRef[];
  /** Issues that supersede this issue. */
  superseded_by: IssueRelationRef[];
}
