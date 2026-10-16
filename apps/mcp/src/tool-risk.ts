import type { ToolAnnotations } from "@modelcontextprotocol/sdk/types.js";

export type ToolRiskCategory = "read" | "write" | "destructive" | "external";

export interface ToolRiskEntry {
  category: ToolRiskCategory;
  annotations: Required<Pick<
    ToolAnnotations,
    "readOnlyHint" | "destructiveHint" | "idempotentHint" | "openWorldHint"
  >>;
  effect: string;
}

const read = (effect: string): ToolRiskEntry => ({
  category: "read",
  annotations: {
    readOnlyHint: true,
    destructiveHint: false,
    idempotentHint: true,
    openWorldHint: false,
  },
  effect,
});

const write = (effect: string, idempotentHint = false): ToolRiskEntry => ({
  category: "write",
  annotations: {
    readOnlyHint: false,
    destructiveHint: false,
    idempotentHint,
    openWorldHint: false,
  },
  effect,
});

const destructive = (effect: string, openWorldHint = false): ToolRiskEntry => ({
  category: openWorldHint ? "external" : "destructive",
  annotations: {
    readOnlyHint: false,
    destructiveHint: true,
    idempotentHint: false,
    openWorldHint,
  },
  effect,
});

const external = (effect: string): ToolRiskEntry => ({
  category: "external",
  annotations: {
    readOnlyHint: false,
    destructiveHint: true,
    idempotentHint: false,
    openWorldHint: true,
  },
  effect,
});

const externalRead = (effect: string): ToolRiskEntry => ({
  category: "external",
  annotations: {
    readOnlyHint: false,
    destructiveHint: false,
    idempotentHint: false,
    openWorldHint: true,
  },
  effect,
});

// Keep this inventory exhaustive. tests/server.test.ts compares it with the
// registered tools and checks the serialized tools/list annotations.
export const TOOL_RISK_MATRIX: Record<string, ToolRiskEntry> = {
  list_workspaces: read("读取当前凭据可见的工作区"),
  list_agents: read("读取工作区 Agent 列表"),
  list_projects: read("读取项目列表"),
  get_project: read("读取项目详情"),
  create_project: write("新增项目"),
  update_project: destructive("覆盖项目元数据"),
  list_issues: read("读取 Issue 列表"),
  get_issue: read("读取 Issue 及可选评论"),
  search_issues: read("搜索 Issue"),
  progress_digest: read("聚合读取项目进展"),
  create_issue: external("新增 Issue；指定 Agent 且 Issue 离开 backlog 时可能启动运行"),
  add_comment: external("新增评论；内容中的提及可触发 Agent 运行"),
  list_comments: read("读取评论与线程"),
  get_comment: read("读取评论及关联线程"),
  edit_comment: external("替换评论正文；内容变更会重新计算提及并可能触发运行"),
  delete_comment: external("永久删除评论，并取消由其提及触发且仍排队的运行"),
  update_issue_status: external("更新状态；状态转换可触发 Agent 运行"),
  update_issue: destructive("覆盖或清除 Issue 元数据；工具描述明确不会启动运行"),
  assign_issue: external("变更负责人；分配 Agent 或 Squad 可能触发运行"),
  get_issue_relations: read("读取 Issue 关系"),
  manage_issue_relations: destructive("新增、删除或变更 Issue 关系与父级；工具描述明确不会启动运行"),
  bulk_update_issues: external("批量更新 Issue；状态或分配变更可逐项触发运行"),
  dispatch_agent: external("创建并分配 Issue，立即触发 Agent 运行并消耗额度"),
  list_issue_runs: read("读取 Issue 运行历史"),
  search_audit_events: read("搜索审计事件"),
  get_run: read("读取运行详情"),
  cancel_run: external("请求取消正在执行的运行"),
  retry_run: external("重试已结束运行并创建新运行"),
  list_daemon_instances: read("读取 Daemon 实例"),
  get_daemon_instance: read("读取 Daemon 实例详情"),
  list_runtimes: read("读取运行时列表"),
  get_runtime: read("读取运行时详情"),
  get_runtime_models: externalRead("缓存未命中时向 Daemon 发起模型发现请求并轮询结果；重复新请求可能创建多个发现请求"),
  get_agent_runtime_config: read("读取 Agent 运行配置"),
  update_agent_runtime_config: destructive("覆盖 Agent 运行配置"),
  bulk_update_agent_runtime_config: destructive("批量覆盖多个 Agent 运行配置"),
  list_execution_profiles: read("读取执行配置档案列表"),
  get_execution_profile: read("读取执行配置档案详情"),
  create_execution_profile: write("新增执行配置档案"),
  update_execution_profile: destructive("覆盖执行配置档案及可选成员映射"),
  delete_execution_profile: destructive("删除执行配置档案"),
  apply_execution_profile: destructive("将配置档案应用到 Agent；可移除未列出的成员映射并改写 Agent 配置"),
  get_execution_topology: read("读取执行拓扑"),
  list_runs: read("搜索运行历史"),
  get_agent: read("读取 Agent 详情"),
  create_agent: write("新增 Agent 定义"),
  update_agent: destructive("覆盖 Agent 定义和运行设置"),
  archive_agent: external("归档 Agent 并取消其排队及进行中的运行"),
  restore_agent: write("恢复已归档 Agent"),
  list_squads: read("读取 Squad 列表"),
  get_squad: read("读取 Squad 详情"),
  create_squad: write("新增 Squad"),
  update_squad: destructive("覆盖 Squad 定义、指令或负责人"),
  archive_squad: external("归档 Squad 并将其 Issue 改派给负责人"),
  list_quick_replies: read("读取快捷回复模板"),
  create_quick_reply: write("新增快捷回复模板"),
  update_quick_reply: destructive("覆盖快捷回复模板"),
  delete_quick_reply: destructive("删除快捷回复模板"),
  list_project_resources: read("读取项目资源绑定；只读指针，不访问仓库或目录"),
  create_project_resource: write("新增项目资源绑定；只写入元数据指针"),
  update_project_resource: destructive("覆盖项目资源绑定元数据"),
  delete_project_resource: destructive("删除项目资源绑定；不删除实际仓库或目录"),
  list_decisions: read("读取决策卡"),
  get_decision: read("读取决策卡详情"),
  answer_decisions: external("回答决策卡并发布提及创建者 Agent 的回声评论，可能恢复其运行"),
};

export function getToolRisk(name: string): ToolRiskEntry {
  const entry = TOOL_RISK_MATRIX[name];
  if (entry === undefined) throw new Error(`Missing MCP tool risk metadata: ${name}`);
  return entry;
}
