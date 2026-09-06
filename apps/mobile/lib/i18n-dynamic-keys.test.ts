// @vitest-environment node
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { RESOURCES } from "@multica/views/locales";
import type {
  AgentTask,
  IssueStatus,
  IssuePriority,
} from "@multica/core/types";

/**
 * 动态拼接 key 的资源解析测试（P1 批次 8）。
 *
 * 为什么单独一支：lib/i18n-keys.test.ts 的采集器只审「字面量」key，明确
 * 拒绝模板串与动态 key。本批次接的五张表全部是 `t(\`前缀.${枚举值}\`)`
 * 形态——被那支防线整体漏过。漏过的后果不是假红而是假绿：key 拼错一个
 * 字符，i18next 静默退回英文 fallback，四项闸门全绿而中文永远出不来。
 * 这里把枚举集合与资源做双向对账，补上那个缺口。
 *
 * 断言分三层：
 *  1. 每个枚举值在 zh-Hans 都解析得到非空字符串（key 拼对了）；
 *  2. 解析结果 != en 的同 key 值（真的翻译过，不是把英文抄进 zh 充数）；
 *  3. 四语齐备（en/zh-Hans/ja/ko 都有，不缺一门）。
 *
 * 第 2 层有意为之的豁免：极少数条目中英同形（如 `run_summary.direct` 的
 * "task"），逐条列名豁免而不是整体放宽——放宽等于把这层断言废掉。
 */

const LOCALES = ["en", "zh-Hans", "ja", "ko"] as const;
type Bundle = Record<string, Record<string, unknown>>;

function lookup(locale: string, ns: string, dotted: string): string | null {
  let cur: unknown = (RESOURCES as unknown as Record<string, Bundle>)[locale]?.[
    ns
  ];
  for (const seg of dotted.split(".")) {
    if (cur == null || typeof cur !== "object") return null;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return typeof cur === "string" ? cur : null;
}

/**
 * 「照抄 en」的判据：大小写 + 空白不敏感。
 *
 * 与 lib/i18n-keys.test.ts 里对大小写**敏感**的比较不冲突，两处的比较对象
 * 根本不同：
 *   - 那边比的是「代码里的 fallback 参数」vs「en 资源原文」——两边都是英文，
 *     `"Retry"` 与 `"RETRY"` 是要显示给英文用户的两种不同呈现，大小写差异
 *     就是真的文案不一致，必须报红。
 *   - 这边比的是「zh-Hans 译文」是否照抄了 en 原文。在「翻没翻」这个问题上
 *     `'task'` 与 `'Task'` 是同一个答案：没翻。放过大小写差异等于给「把英文
 *     小写一下塞进 zh」开后门。
 * 两者同向：都是不让「看起来不一样」掩盖「实质一样」。
 */
const norm = (s: string) => s.replace(/\s+/g, " ").trim().toLowerCase();

/** 中英同形、不要求 zh != en 的条目（ns:key 全路径）。 */
const SAME_AS_EN_OK = new Set<string>([
  // 「task」在本产品的中文语境里保留英文原词（见 zh-Hans 各处 "task 已完成"）。
  "issues:mobile.run_summary.direct",
]);

/**
 * 一组动态 key 的通用对账。
 * @param label   报错时的可读名
 * @param ns      namespace
 * @param prefix  key 前缀（不含尾点）
 * @param枚举值   源码里会拼进 key 的全部取值
 */
function checkTable(
  label: string,
  ns: string,
  prefix: string,
  values: string[],
) {
  describe(label, () => {
    it("枚举值非空（防止表被清空后本组断言退化成空跑）", () => {
      expect(values.length).toBeGreaterThan(0);
    });

    it("每个枚举值都在 zh-Hans 解析到非空字符串", () => {
      const misses = values.filter((v) => {
        const s = lookup("zh-Hans", ns, `${prefix}.${v}`);
        return s == null || s.trim() === "";
      });
      expect(misses).toEqual([]);
    });

    it("zh-Hans 的值确实是译文，而不是照抄 en", () => {
      const untranslated = values.filter((v) => {
        const full = `${ns}:${prefix}.${v}`;
        if (SAME_AS_EN_OK.has(full)) return false;
        const zh = lookup("zh-Hans", ns, `${prefix}.${v}`);
        const en = lookup("en", ns, `${prefix}.${v}`);
        return zh != null && en != null && norm(zh) === norm(en);
      });
      expect(untranslated).toEqual([]);
    });

    it("四语齐备", () => {
      const gaps: string[] = [];
      for (const loc of LOCALES) {
        for (const v of values) {
          const s = lookup(loc, ns, `${prefix}.${v}`);
          if (s == null || s.trim() === "")
            gaps.push(`${loc} ${ns}:${prefix}.${v}`);
        }
      }
      expect(gaps).toEqual([]);
    });
  });
}

// ─── 各表的枚举集合 ────────────────────────────────────────────────
// 这些数组是源码里那几张 Record 的 key 集合的副本。副本会漂移，所以下面
// 每组都配一条「与源码表同步」的对账断言，从真实模块里读回来比对。

const FAILURE_REASONS = [
  "queued_expired",
  "runtime_offline",
  "runtime_recovery",
  "timeout",
  "iteration_limit",
  "agent_blocked",
  "api_invalid_request",
  "skill_bundle_unavailable",
  "runtime_cli_timeout",
  "environment_prepare_failed",
  "agent_error__provider_auth_or_access",
  "agent_error__provider_quota_limit",
  "agent_error__provider_capacity_or_rate_limit",
  "agent_error__provider_server_error",
  "agent_error__provider_network",
  "agent_error__process_failure",
  "agent_error__empty_or_unparseable_output",
  "agent_error__agent_timeout",
  "agent_error__context_overflow",
  "agent_error__missing_config",
  "agent_error__model_not_found_or_unavailable",
  "agent_error__runtime_version_unsupported",
  "agent_error__runtime_missing_executable",
  "agent_error__unknown",
  "agent_error",
  "codex_semantic_inactivity",
  "manual",
];

const TASK_STATUSES: AgentTask["status"][] = [
  "queued",
  "dispatched",
  "waiting_local_directory",
  "running",
  "completed",
  "failed",
  "cancelled",
];

const TASK_KINDS = ["comment", "autopilot", "chat", "quick_create", "direct"];

const ISSUE_STATUSES: IssueStatus[] = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
  "cancelled",
];

const ISSUE_PRIORITIES: IssuePriority[] = [
  "urgent",
  "high",
  "medium",
  "low",
  "none",
];

const INBOX_TYPES = [
  "issue_assigned",
  "issue_subscribed",
  "unassigned",
  "assignee_changed",
  "status_changed",
  "priority_changed",
  "start_date_changed",
  "due_date_changed",
  "new_comment",
  "mentioned",
  "review_requested",
  "task_completed",
  "task_failed",
  "agent_blocked",
  "agent_completed",
  "reaction_added",
  "quick_create_done",
  "quick_create_failed",
  "quick_create_unconfirmed",
];

const PILL_STAGES = [
  "offline",
  "reconnecting",
  "retrying",
  "queued",
  "starting_up",
  "thinking",
  "typing",
];

const PROJECT_STATUSES = [
  "planned",
  "in_progress",
  "paused",
  "completed",
  "cancelled",
];

const PROJECT_PRIORITIES = ["urgent", "high", "medium", "low", "none"];

const PILL_TOOLS = [
  "running_command",
  "reading_files",
  "searching_code",
  "making_edits",
  "searching_web",
  "fallback",
];

// lib/failure-reason-label.ts 的长文案表（聊天气泡）
checkTable(
  "chat:mobile.failure_reason.*（长版，聊天气泡）",
  "chat",
  "mobile.failure_reason",
  [...FAILURE_REASONS, "fallback"],
);

// run-row.tsx 的短文案表（badge，与状态词共享一行）
checkTable(
  "issues:mobile.run_failure.*（短版，run-row badge）",
  "issues",
  "mobile.run_failure",
  FAILURE_REASONS,
);

checkTable(
  "issues:mobile.run_status.*",
  "issues",
  "mobile.run_status",
  TASK_STATUSES as string[],
);

checkTable(
  "issues:mobile.run_summary.*",
  "issues",
  "mobile.run_summary",
  TASK_KINDS,
);

checkTable(
  "issues:status.*（format-activity / detail-label 共用）",
  "issues",
  "status",
  ISSUE_STATUSES as string[],
);

checkTable(
  "issues:priority.*（format-activity / detail-label 共用）",
  "issues",
  "priority",
  ISSUE_PRIORITIES as string[],
);

checkTable("inbox:types.*", "inbox", "types", INBOX_TYPES);

checkTable(
  "chat:status_pill.stages.*",
  "chat",
  "status_pill.stages",
  PILL_STAGES,
);

checkTable("chat:status_pill.tools.*", "chat", "status_pill.tools", PILL_TOOLS);

// lib/project-status.ts —— 这两组此前连枚举对账都没有（批次 9 补）。
checkTable(
  "projects:status.*",
  "projects",
  "status",
  PROJECT_STATUSES,
);

checkTable(
  "projects:priority.*",
  "projects",
  "priority",
  PROJECT_PRIORITIES,
);

// components/agents/agent-presence-line.tsx —— presence 两维标签
// （RUYI-76 ②）。枚举副本取自 @multica/core/agents 的
// AgentAvailability / Workload；资源节点是 web agents 命名空间的既有键。
checkTable("agents:availability.*", "agents", "availability", [
  "online",
  "unstable",
  "offline",
  "archived",
]);

checkTable("agents:workload.*", "agents", "workload", [
  "working",
  "queued",
  "idle",
]);

describe("两份 failure 表的关系", () => {
  it("key 集合完全一致（长版多一个 fallback）", () => {
    const long = Object.keys(
      (
        (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
          .chat as Record<string, Record<string, unknown>>
      ).mobile.failure_reason as Record<string, string>,
    )
      .filter((k) => k !== "fallback")
      .sort();
    const short = Object.keys(
      (
        (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
          .issues as Record<string, Record<string, unknown>>
      ).mobile.run_failure as Record<string, string>,
    ).sort();
    expect(short).toEqual(long);
  });

  it("同 key 的两版中文文案不同 —— badge 那版必须另写更短的", () => {
    // 合并成一套是本批次明确否决的做法：badge 与状态词、时间戳共享一行，
    // 中文同样需要更短的呈现。若两版逐字相同，说明有人偷懒复制了长版。
    const chat = (
      (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
        .chat as Record<string, Record<string, unknown>>
    ).mobile.failure_reason as Record<string, string>;
    const issues = (
      (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
        .issues as Record<string, Record<string, unknown>>
    ).mobile.run_failure as Record<string, string>;
    const identical = FAILURE_REASONS.filter((k) => chat[k] === issues[k]);
    expect(identical).toEqual([]);
  });

  it("badge 版中文两两不同 —— 缩写撞车会让两种失败原因在徽标上无法区分", () => {
    // 实际踩到过：agent_timeout 缩成「超时」正好与 timeout 那条同字，
    // 用户在 badge 上完全分不出是调度侧超时还是 agent 自身超时。
    const issues = (
      (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
        .issues as Record<string, Record<string, unknown>>
    ).mobile.run_failure as Record<string, string>;
    // agent_error 与 agent_error.unknown 是同一语义的新旧两个 wire 值，
    // 文案相同是正确的（源码 LABELS 表里也一样），成对放行。
    const ALIASES = [new Set(["agent_error", "agent_error__unknown"])];
    const isAlias = (a: string, b: string) =>
      ALIASES.some((s) => s.has(a) && s.has(b));

    const seen = new Map<string, string>();
    const collisions: string[] = [];
    for (const k of FAILURE_REASONS) {
      const v = issues[k]!;
      const prev = seen.get(v);
      if (prev && !isAlias(prev, k))
        collisions.push(`${prev} / ${k} 同为 "${v}"`);
      seen.set(v, k);
    }
    expect(collisions).toEqual([]);
  });

  it("badge 版的中文普遍不长于气泡版", () => {
    const chat = (
      (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
        .chat as Record<string, Record<string, unknown>>
    ).mobile.failure_reason as Record<string, string>;
    const issues = (
      (RESOURCES as unknown as Record<string, Bundle>)["zh-Hans"]
        .issues as Record<string, Record<string, unknown>>
    ).mobile.run_failure as Record<string, string>;
    const longer = FAILURE_REASONS.filter(
      (k) => (issues[k]?.length ?? 0) > (chat[k]?.length ?? 0),
    );
    expect(longer).toEqual([]);
  });
});

// ─── 前缀锁（批次 9）────────────────────────────────────────────────
//
// 上面的 checkTable 只锁住了 key 的**后半段**（枚举值）——前半段（前缀）
// 是裸奔的：把 `mobile.run_failure` 敲成 `mobile.run_failuer`，26 条文案
// 会静默全部退回英文 fallback，而 tsc 0 + vitest 全绿。金小欣与蔡小星各
// 复现过一次。这支 describe 从**源码**正则读回真正拼出的前缀，再回资源里
// 核对该节点确实存在。

/**
 * 组件文件的 ns 读不到：`useT("chat")` 把 ns 绑在 hook 上，模板串里只剩
 * 后半段。这里从源码正则读回该文件的 `useT("<ns>")` 补上映射。lib/ 下走
 * `i18n.t("ns:…")`，ns 就在串里，不走这条路径。
 *
 * 早先这是一张手写映射表。手写表会漂移：文件把 useT("issues") 改成别的
 * ns，表还停在旧值，前缀锁就会拿错 ns 去解析——解析得到的节点可能碰巧
 * 存在，于是整支防线静默失效。从源码读回则不存在这个偏差源。
 *
 * 一个文件出现多个 useT 时不猜：返回 null 让调用侧记成「ns 不可判定」并
 * 报红，由人显式处理，而不是取第一个赌它对。
 */
function nsOfFile(src: string): string | null {
  const nss = [...src.matchAll(/useT\(\s*["']([^"']+)["']\s*\)/g)].map(
    (m) => m[1]!,
  );
  const uniq = [...new Set(nss)];
  return uniq.length === 1 ? uniq[0]! : null;
}

/** 前缀 → 该前缀下源码会拼出的枚举值（资源节点必须是它的超集）。 */
const PREFIX_VALUES: Record<string, string[]> = {
  "chat:mobile.failure_reason": [...FAILURE_REASONS, "fallback"],
  "issues:mobile.run_failure": FAILURE_REASONS,
  "issues:mobile.run_status": TASK_STATUSES as string[],
  "issues:status": ISSUE_STATUSES as string[],
  "issues:priority": ISSUE_PRIORITIES as string[],
  "chat:status_pill.stages": PILL_STAGES,
  "chat:status_pill.tools": PILL_TOOLS,
  "projects:status": PROJECT_STATUSES,
  "projects:priority": PROJECT_PRIORITIES,
  "agents:availability": ["online", "unstable", "offline", "archived"],
  "agents:workload": ["working", "queued", "idle"],
};

const MOBILE_ROOT = join(__dirname, "..");

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (e.name === "node_modules" || e.name.startsWith(".")) continue;
    const p = join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name)) out.push(p);
  }
  return out;
}

/** 从源码里正则读回全部「模板串动态 key」的前缀，附带 ns 归属。 */
function collectPrefixes(): { file: string; full: string }[] {
  const hits: { file: string; full: string }[] = [];
  for (const root of ["lib", "components", "app", "data"]) {
    for (const file of walk(join(MOBILE_ROOT, root))) {
      const rel = file.slice(MOBILE_ROOT.length + 1).replace(/\\/g, "/");
      const src = readFileSync(file, "utf8");
      const fileNs = nsOfFile(src);
      // `t(` 与反引号之间允许空白/换行：prettier 会把超长调用拆成多行
      // （`t(\n  \`前缀.${v}\`,\n  en,\n)`），紧贴形态的正则会整条漏采。
      //
      // 函数名放宽到 `t` 与 `t<大写开头>` 两种：一个文件同时用两个 ns 时
      // 必须重命名解构（`const { t: tIssues } = useT("issues")`），此形态
      // 出现在 new-issue.tsx / issue/[id]/edit.tsx / project/new.tsx /
      // project/[id]/edit.tsx / project/[id]/add-resource.tsx 五个文件。
      // 旧正则的 `\bt\(` 采不到 `tIssues(`，这五个文件的动态前缀无论拼错
      // 成什么都不会报红——前缀锁在它们身上等于不存在。
      //
      // 注意 `t[A-Z]\w*` 只是形态匹配，不保证该别名真的绑到某个 ns：这些
      // 文件有多个 useT，nsOfFile 会返回 null，于是相对前缀落到
      // 「<ns 不可判定>」并被「每个源码前缀的 ns 都能确定」那条挡住——要求
      // 人在这类文件里显式写绝对 key（`\`issues:xxx.${v}\``）。这是刻意的：
      // 猜别名到 ns 的映射正是本文件开头批判的「手写表会漂移」。
      for (const m of src.matchAll(/(?:i18n\.)?\bt(?:[A-Z]\w*)?\(\s*`([^`]*?)\$\{/g)) {
        const head = m[1]!;
        // 只处理「前缀 + 尾点 + ${枚举}」这一种形态；句中插值（`{{name}}`
        // 之类走的是 options，不会进这个分支）不是 key 拼接，跳过。
        if (!head.endsWith(".")) continue;
        const body = head.slice(0, -1);
        const full = body.includes(":")
          ? body
          : `${fileNs ?? "<ns 不可判定>"}:${body}`;
        hits.push({ file: rel, full });
      }
    }
  }
  return hits;
}

/** 资源里定位一个前缀节点；不存在或不是对象则返回 null。 */
function resolveNode(full: string): Record<string, unknown> | null {
  const [ns, prefix] = full.split(":") as [string, string];
  let cur: unknown = (RESOURCES as unknown as Record<string, Bundle>)[
    "zh-Hans"
  ]?.[ns];
  for (const seg of prefix.split(".")) {
    if (cur == null || typeof cur !== "object") return null;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur != null && typeof cur === "object"
    ? (cur as Record<string, unknown>)
    : null;
}

describe("动态 key 的前缀锁", () => {
  const found = collectPrefixes();

  // ── 非恒真自证（硬约束）──────────────────────────────────────
  // 正则一旦失配，found 为空，下面所有 for 循环空跑、断言恒真通过——那正是
  // 本文件存在的理由（假绿）的同构版本。三条自证把它堵死。

  it("自证 1：正则确实扫到了源码里的动态前缀（否则本组断言恒真）", () => {
    expect(found.length).toBeGreaterThanOrEqual(
      Object.keys(PREFIX_VALUES).length,
    );
  });

  it("自证 2：编造的前缀必须判为缺失（证明 resolveNode 不恒真）", () => {
    expect(resolveNode("issues:mobile.run_failuer")).toBeNull();
    expect(resolveNode("issues:mobile.run_failure.timeout")).toBeNull(); // 叶子不是对象
    expect(resolveNode("nosuchns:whatever")).toBeNull();
  });

  it("自证 3：真实前缀必须判为存在（证明 resolveNode 不恒假）", () => {
    expect(resolveNode("issues:mobile.run_failure")).not.toBeNull();
    expect(resolveNode("chat:status_pill.stages")).not.toBeNull();
  });

  // ── 正题 ──────────────────────────────────────────────────────

  it("每个源码前缀的 ns 都能确定（组件文件须有唯一的 useT 绑定）", () => {
    expect(found.filter((h) => h.full.startsWith("<ns 不可判定>"))).toEqual([]);
  });

  it("每个源码前缀在资源里都解析到一个对象节点", () => {
    const misses = found
      .filter((h) => resolveNode(h.full) == null)
      .map((h) => `${h.file} → ${h.full}`);
    expect([...new Set(misses)]).toEqual([]);
  });

  it("资源节点的子键集合 ⊇ 源码枚举副本", () => {
    // 只能用 ⊇ 不能用 == ：`chat:status_pill.stages` 资源侧 8 键，源码
    // STAGE_LABEL_EN 7 键，多出的 waiting_local_directory 是 web 侧既有
    // key（88bcce1 之前就在），mobile 不拼它，枚举副本 7 条是正确的。
    const gaps: string[] = [];
    for (const [full, values] of Object.entries(PREFIX_VALUES)) {
      const node = resolveNode(full);
      if (node == null) {
        gaps.push(`${full}：节点缺失`);
        continue;
      }
      for (const v of values) {
        if (!(v in node)) gaps.push(`${full}.${v}`);
      }
    }
    expect(gaps).toEqual([]);
  });

  it("PREFIX_VALUES 覆盖源码里出现的每一个前缀（新增前缀不能漏登记）", () => {
    const unregistered = [...new Set(found.map((h) => h.full))].filter(
      (f) => !(f in PREFIX_VALUES),
    );
    expect(unregistered).toEqual([]);
  });
});

describe("枚举副本与源码表同步", () => {
  it("failure reason 集合覆盖 lib/failure-reason-label.ts 的全部 wire 值", () => {
    // 从真实源码读回 LABELS 的 key —— 源码加了新 reason 而这里没跟，
    // 本文件的所有断言都会对新增项失效（假绿）。这条把它挡住。
    const src = readFileSync(
      join(__dirname, "failure-reason-label.ts"),
      "utf8",
    );
    const body = src.slice(
      src.indexOf("const LABELS"),
      src.indexOf("export function"),
    );
    const wire = [...body.matchAll(/^\s*"?([a-z_][a-z_.]*)"?:\s*"/gm)].map(
      (m) => m[1]!,
    );
    expect(wire.length).toBeGreaterThan(0);
    const normalized = wire.map((w) => w.replace(/\./g, "__")).sort();
    expect(normalized).toEqual([...FAILURE_REASONS].sort());
  });
});
