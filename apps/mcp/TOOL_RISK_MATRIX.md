# MCP Tool Risk Matrix

This inventory covers the complete static `TOOL_DEFINITIONS` registry exposed
by `tools/list` (65 tools at this revision). `src/tool-risk.ts` is the runtime
source of truth; `test/server.test.ts` verifies exact name coverage and the
serialized annotations over an in-memory MCP client/server connection.

The four booleans are MCP hints, not authorization or confirmation controls.
MCP defaults are `readOnlyHint=false`, `destructiveHint=true`,
`idempotentHint=false`, and `openWorldHint=true`. Read-only entries explicitly
declare no mutation, no destructive effect, idempotent reads, and a closed
Multica domain. Mutations default to non-idempotent because repeated calls can
change revisions, create duplicates, retrigger work, or fail after state has
changed. `openWorldHint=true` is reserved for tools whose reachable effects can
start, resume, or cancel agent execution, or initiate Daemon work outside the
bounded metadata domain.

| Tool | Risk class | Read only | Destructive | Idempotent | Open world | Effect evidence |
| --- | --- | --- | --- | --- | --- | --- |
| `list_workspaces` | read | true | false | true | false | Reads workspaces visible to the credential. |
| `list_agents` | read | true | false | true | false | Reads workspace agents. |
| `list_projects` | read | true | false | true | false | Reads projects. |
| `get_project` | read | true | false | true | false | Reads project details. |
| `create_project` | write | false | false | false | false | Adds a project. |
| `update_project` | destructive | false | true | false | false | Replaces project metadata. |
| `list_issues` | read | true | false | true | false | Reads issues. |
| `get_issue` | read | true | false | true | false | Reads issue and optional comments. |
| `search_issues` | read | true | false | true | false | Searches issues. |
| `progress_digest` | read | true | false | true | false | Aggregates project progress reads. |
| `create_issue` | external | false | true | false | true | Adds an issue; assigning an agent can start a run when it leaves backlog. |
| `add_comment` | external | false | true | false | true | Adds a comment; mentions can trigger an agent run. |
| `list_comments` | read | true | false | true | false | Reads comments and threads. |
| `get_comment` | read | true | false | true | false | Reads a comment and its thread. |
| `edit_comment` | external | false | true | false | true | Replaces content; changed mentions can trigger runs. |
| `delete_comment` | external | false | true | false | true | Permanently deletes a comment and cancels queued runs triggered by it. |
| `update_issue_status` | external | false | true | false | true | Changes status; a transition can start an agent run. |
| `update_issue` | destructive | false | true | false | false | Replaces or clears issue metadata; description says it never starts a run. |
| `assign_issue` | external | false | true | false | true | Changes assignee; assigning an agent or squad can start a run. |
| `get_issue_relations` | read | true | false | true | false | Reads issue relations. |
| `manage_issue_relations` | destructive | false | true | false | false | Adds, removes, or changes relations and parent; description rules out runs. |
| `bulk_update_issues` | external | false | true | false | true | Batch status or assignment changes can start runs per item. |
| `dispatch_agent` | external | false | true | false | true | Creates and assigns an issue, immediately starting a run and consuming quota. |
| `list_issue_runs` | read | true | false | true | false | Reads issue run history. |
| `search_audit_events` | read | true | false | true | false | Searches audit events. |
| `get_run` | read | true | false | true | false | Reads run details. |
| `cancel_run` | external | false | true | false | true | Requests cancellation of an executing run. |
| `retry_run` | external | false | true | false | true | Creates a new run from a finished run. |
| `list_daemon_instances` | read | true | false | true | false | Reads Daemon instances. |
| `get_daemon_instance` | read | true | false | true | false | Reads a Daemon instance. |
| `list_runtimes` | read | true | false | true | false | Reads runtimes. |
| `get_runtime` | read | true | false | true | false | Reads runtime details. |
| `get_runtime_models` | external | false | false | false | true | On a cold cache, starts a model-discovery request with the Daemon and polls; a new call may create another request. |
| `get_agent_runtime_config` | read | true | false | true | false | Reads an Agent runtime configuration. |
| `update_agent_runtime_config` | destructive | false | true | false | false | Replaces an Agent runtime configuration. |
| `bulk_update_agent_runtime_config` | destructive | false | true | false | false | Replaces several Agent runtime configurations. |
| `list_execution_profiles` | read | true | false | true | false | Reads execution profiles. |
| `get_execution_profile` | read | true | false | true | false | Reads an execution profile. |
| `create_execution_profile` | write | false | false | false | false | Adds an execution profile. |
| `update_execution_profile` | destructive | false | true | false | false | Replaces profile metadata and optional member mappings. |
| `delete_execution_profile` | destructive | false | true | false | false | Deletes an execution profile. |
| `apply_execution_profile` | destructive | false | true | false | false | Applies Agent configuration and may remove absent mappings. |
| `get_execution_topology` | read | true | false | true | false | Reads execution topology. |
| `list_runs` | read | true | false | true | false | Searches run history. |
| `get_agent` | read | true | false | true | false | Reads Agent details. |
| `create_agent` | write | false | false | false | false | Adds an Agent definition. |
| `update_agent` | destructive | false | true | false | false | Replaces Agent definition and runtime settings. |
| `archive_agent` | external | false | true | false | true | Archives an Agent and cancels its queued and in-flight runs. |
| `restore_agent` | write | false | false | false | false | Restores an archived Agent. |
| `list_squads` | read | true | false | true | false | Reads squads. |
| `get_squad` | read | true | false | true | false | Reads a squad. |
| `create_squad` | write | false | false | false | false | Adds a squad. |
| `update_squad` | destructive | false | true | false | false | Replaces squad definition, instructions, or leader. |
| `archive_squad` | external | false | true | false | true | Archives a squad and reassigns its issues to the leader. |
| `list_quick_replies` | read | true | false | true | false | Reads quick reply templates. |
| `create_quick_reply` | write | false | false | false | false | Adds a quick reply template. |
| `update_quick_reply` | destructive | false | true | false | false | Replaces a quick reply template. |
| `delete_quick_reply` | destructive | false | true | false | false | Deletes a quick reply template. |
| `list_project_resources` | read | true | false | true | false | Reads metadata pointers without accessing repositories or directories. |
| `create_project_resource` | write | false | false | false | false | Adds a project resource metadata pointer. |
| `update_project_resource` | destructive | false | true | false | false | Replaces project resource metadata. |
| `delete_project_resource` | destructive | false | true | false | false | Deletes a binding, not its repository or directory. |
| `list_decisions` | read | true | false | true | false | Reads decision cards. |
| `get_decision` | read | true | false | true | false | Reads a decision card. |
| `answer_decisions` | external | false | true | false | true | Answers a card and posts a creator-Agent mention that can resume its run. |

## Coverage Notes

- Registration is static in `src/tools.ts`; there is no runtime or transport-specific tool registration path. Both stdio and Streamable HTTP use `createMcpServer` from `src/server.ts`.
- The repository pins `@modelcontextprotocol/sdk` `^1.27.1`; installed SDK 1.27.1 negotiates `2025-11-25`, `2025-06-18`, `2025-03-26`, `2024-11-05`, and `2024-10-07`. Tool annotations are assembled from the same `TOOL_RISK_MATRIX` for every supported protocol version and both stdio and Streamable HTTP transports; there is no version- or transport-specific projection.
- `tools/list` hints do not guarantee a ChatGPT confirmation prompt. The ChatGPT path and observed confirmation behavior remain for independent QA.
- High-risk tools keep their current combined APIs. Splitting read/write or mixed actions would change the public tool surface and is not needed to expose accurate annotations in this task.

## Sensitive Operations

`create_issue`, `add_comment`, `edit_comment`, `delete_comment`,
`update_issue_status`, `assign_issue`, `bulk_update_issues`, `dispatch_agent`,
`cancel_run`, `retry_run`, `archive_agent`, `archive_squad`, and
`answer_decisions` can start, resume, cancel, or otherwise affect agent runs.
`get_runtime_models` may enqueue a Daemon model-discovery request when the
catalog cache is cold.

## Follow-up: Server-side Per-call Approval

Recommendation for a separately scoped change: place an approval gate at the
server operation boundary for destructive writes and actions that can start,
resume, or cancel execution. Keep MCP annotations as descriptive hints; a
server gate would enforce the decision for every MCP client, including clients
that ignore hints. Do not route ordinary bounded metadata reads or additive
writes through that gate.

The follow-up should compare a server-enforced one-time approval with
client-only confirmation and a hybrid policy. The server option provides
consistent enforcement and an auditable decision, but requires a way to
present, bind, expire, and consume approval for the exact operation and
arguments. Client-only confirmation is simpler but depends on each client.
The hybrid option can use client prompts for usability while retaining a
server gate for high-risk operations.

Open decisions for that work: which operation classes require approval;
whether approval binds to normalized arguments and a short expiry; what happens
for unattended agent calls; and how approval records appear in the audit
trail. This recommendation does not alter the current OAuth authorization
model and is not implemented by this change.
Deletion and destructive configuration changes are identified by the
`destructiveHint` column. No server-side per-call approval or OAuth behavior is
changed here.
