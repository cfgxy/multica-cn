/**
 * 服务器配置的纯逻辑层 —— URL 规范化/校验、内置默认项合成、持久化载荷的
 * 序列化与解析。
 *
 * 拆成独立模块而不是塞进 server-store.ts,是为了让这些分支能在 vitest 的
 * node 环境里直接测(见 vitest.config.ts 的说明:store 本身依赖
 * AsyncStorage 原生模块,不在可测范围内;逻辑全在这里,store 只负责状态
 * 与持久化编排)。
 */

export interface ServerEntry {
  /** uuid;内置项固定 "default" */
  id: string;
  /** 显示名,内置项为 "Multica Official" */
  name: string;
  /** 规范化后的 http(s) 地址(无尾斜杠) */
  apiUrl: string;
  /** 可选;null = 回退 apiUrl(单域名部署零配置) */
  webUrl: string | null;
  /** 内置项 true:不可编辑、不可删除 */
  builtIn: boolean;
}

/** 内置默认项的固定 id。持久化数据里绝不出现这一项。 */
export const BUILT_IN_SERVER_ID = "default";

/** 持久化载荷版本号,留迁移余地。 */
export const SERVER_STORE_VERSION = 1;

export interface PersistedServerState {
  version: number;
  /** 只含自定义项 —— 内置项每次启动从构建期 env 重新合成 */
  servers: ServerEntry[];
  activeServerId: string;
}

/**
 * 去掉尾部斜杠与首尾空白。API 地址全程以「无尾斜杠」形态存储,
 * 因为所有请求路径本身以 `/api/...` 开头。
 */
export function normalizeUrl(raw: string): string {
  return raw.trim().replace(/\/+$/, "");
}

/**
 * 合法性校验:必须是 http/https 协议且带非空 host。
 * 注意用 URL 构造器而不是正则 —— Hermes 已内置 URL,行为与运行时 fetch 一致。
 */
export function isValidServerUrl(raw: string): boolean {
  const normalized = normalizeUrl(raw);
  if (!normalized) return false;
  let parsed: URL;
  try {
    parsed = new URL(normalized);
  } catch {
    return false;
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return false;
  return parsed.hostname.length > 0;
}

/** 明文 http:// —— 保存不拦截,只给一次性弱警告(自建内网场景)。 */
export function isPlainHttp(raw: string): boolean {
  const normalized = normalizeUrl(raw);
  if (!isValidServerUrl(normalized)) return false;
  return new URL(normalized).protocol === "http:";
}

/**
 * 重复地址拦截:同一 apiUrl 不允许出现两次(编辑时排除自身)。
 * 比较用规范化后的值,`https://x.com/` 与 `https://x.com` 视为同一个。
 */
export function findDuplicateServer(
  servers: ServerEntry[],
  apiUrl: string,
  excludeId?: string,
): ServerEntry | undefined {
  const target = normalizeUrl(apiUrl);
  return servers.find((s) => s.id !== excludeId && s.apiUrl === target);
}

/**
 * 从构建期 env 合成内置默认项。不落盘 —— 官方域名日后变更时,升级 App
 * 即自动跟上,不会被旧持久化数据钉死。
 */
export function buildBuiltInServer(
  apiUrl: string | undefined,
  webUrl: string | undefined,
): ServerEntry {
  if (!apiUrl) {
    throw new Error(
      "EXPO_PUBLIC_API_URL is not set. Add it to apps/mobile/.env.development.local " +
        "(see apps/mobile/.env.staging for an example).",
    );
  }
  return {
    id: BUILT_IN_SERVER_ID,
    name: "Multica Official",
    apiUrl: normalizeUrl(apiUrl),
    webUrl: webUrl ? normalizeUrl(webUrl) : null,
    builtIn: true,
  };
}

/** 内置项恒在列表首位,其后是自定义项(按加入顺序)。 */
export function composeServerList(
  builtIn: ServerEntry,
  customServers: ServerEntry[],
): ServerEntry[] {
  return [builtIn, ...customServers.filter((s) => !s.builtIn)];
}

/**
 * Web 地址解析,全应用唯一收口:`webUrl ?? apiUrl`。
 * 回退值绝不落盘 —— 用户日后改了服务器地址,Web 跳转自动跟着走。
 */
export function resolveWebUrl(entry: ServerEntry): string {
  return entry.webUrl ?? entry.apiUrl;
}

/**
 * 解析持久化载荷。任何形态异常(非法 JSON、版本不认、字段缺失)一律返回
 * null,由调用方回退到「只有内置项」的初始状态 —— 行为与现版完全一致。
 */
export function parsePersistedState(raw: string | null): PersistedServerState | null {
  if (!raw) return null;
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof data !== "object" || data === null) return null;
  const obj = data as Record<string, unknown>;
  if (obj.version !== SERVER_STORE_VERSION) return null;
  if (!Array.isArray(obj.servers)) return null;
  if (typeof obj.activeServerId !== "string" || !obj.activeServerId) return null;

  const servers: ServerEntry[] = [];
  for (const item of obj.servers) {
    const entry = parseServerEntry(item);
    // 单项损坏就丢掉这一项,不因此废掉整份配置 —— 用户的其他服务器还能用。
    if (entry) servers.push(entry);
  }
  return {
    version: SERVER_STORE_VERSION,
    servers,
    activeServerId: obj.activeServerId,
  };
}

function parseServerEntry(item: unknown): ServerEntry | null {
  if (typeof item !== "object" || item === null) return null;
  const o = item as Record<string, unknown>;
  if (typeof o.id !== "string" || !o.id) return null;
  if (o.id === BUILT_IN_SERVER_ID) return null; // 内置项不该落盘,读到就忽略
  if (typeof o.apiUrl !== "string" || !isValidServerUrl(o.apiUrl)) return null;
  const webUrl =
    typeof o.webUrl === "string" && isValidServerUrl(o.webUrl)
      ? normalizeUrl(o.webUrl)
      : null;
  return {
    id: o.id,
    name: typeof o.name === "string" ? o.name : "",
    apiUrl: normalizeUrl(o.apiUrl),
    webUrl,
    builtIn: false,
  };
}

/** 构造落盘载荷:内置项与回退出来的 webUrl 都不写进去。 */
export function serializePersistedState(
  servers: ServerEntry[],
  activeServerId: string,
): string {
  const payload: PersistedServerState = {
    version: SERVER_STORE_VERSION,
    servers: servers.filter((s) => !s.builtIn),
    activeServerId,
  };
  return JSON.stringify(payload);
}

/**
 * 选出当前生效项:activeServerId 指向的项;指向不存在的项(比如上次用的
 * 自定义服务器被外部清掉了)时回退内置项。
 */
export function pickActiveServer(
  servers: ServerEntry[],
  activeServerId: string,
): ServerEntry {
  return (
    servers.find((s) => s.id === activeServerId) ??
    servers.find((s) => s.builtIn) ??
    servers[0]
  );
}

/** http(s):// → ws(s)://,再接 `/ws`。realtime 连接时现算,不做模块级派生。 */
export function toWebSocketUrl(apiUrl: string): string {
  return `${normalizeUrl(apiUrl).replace(/^http/, "ws")}/ws`;
}
