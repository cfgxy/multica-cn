export interface ManagedEnvironment {
  environmentId: string;
  taskId: string;
  issueId?: string;
  workspaceShort: string;
  taskShort: string;
  parentStatus: string;
  sizeBytes: number;
  ageSeconds: number;
  active: boolean;
  protectionReason: string;
  retentionReason: string;
}

export interface EnvironmentCachePreview {
  environmentId: string;
  revision: string;
  sizeBytes: number;
  candidates: number;
  reason: string;
}

export interface EnvironmentCacheResult extends EnvironmentCachePreview {
  removedBytes: number;
  removedCount: number;
}

export interface EnvironmentArchive {
  archiveId: string;
  taskId: string;
  taskName: string;
  kind: string;
  createdAt: string;
  archiveBytes: number;
  logicalBytes: number;
  restoreReason: string;
}

export interface EnvironmentArchiveResult {
  environmentId: string;
  revision: string;
  archiveId: string;
  reason: string;
  originalBytes: number;
  archiveBytes: number;
  reclaimed: boolean;
  restored: boolean;
}

export interface EnvironmentManagerAPI {
  inventory: () => Promise<ManagedEnvironment[]>;
  previewCache: () => Promise<EnvironmentCachePreview[]>;
  cleanCache: (rows: Array<{ environmentId: string; revision: string }>) => Promise<EnvironmentCacheResult[]>;
  archives: () => Promise<EnvironmentArchive[]>;
  previewArchive: () => Promise<EnvironmentArchiveResult[]>;
  archive: (rows: Array<{ environmentId: string; revision: string }>) => Promise<EnvironmentArchiveResult[]>;
  restoreArchive: (archiveId: string) => Promise<EnvironmentArchiveResult[]>;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function text(value: unknown, field: string): string {
  if (typeof value !== "string") throw new Error(`Invalid daemon response: ${field}`);
  return value;
}

function number(value: unknown, field: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) throw new Error(`Invalid daemon response: ${field}`);
  return value;
}

function records(value: unknown): Record<string, unknown>[] {
  if (!Array.isArray(value) || !value.every(isRecord)) throw new Error("Invalid daemon response: expected a list");
  return value;
}

export function parseManagedEnvironments(value: unknown): ManagedEnvironment[] {
  return records(value).map((row) => ({
    environmentId: text(row.environment_id, "environment_id"),
    taskId: text(row.task_id, "task_id"),
    issueId: typeof row.issue_id === "string" ? row.issue_id : undefined,
    workspaceShort: typeof row.workspace_short === "string" ? row.workspace_short : "",
    taskShort: typeof row.task_short === "string" ? row.task_short : "",
    parentStatus: typeof row.parent_status === "string" ? row.parent_status : "",
    sizeBytes: number(row.size_bytes, "size_bytes"),
    ageSeconds: number(row.age_seconds, "age_seconds"),
    active: row.active === true,
    protectionReason: typeof row.protection_reason === "string" ? row.protection_reason : "",
    retentionReason: typeof row.retention_reason === "string" ? row.retention_reason : "",
  }));
}

export function parseCachePreview(value: unknown): EnvironmentCachePreview[] {
  return records(value).map((row) => ({
    environmentId: text(row.environment_id, "environment_id"),
    revision: text(row.revision, "revision"),
    sizeBytes: number(row.size_bytes, "size_bytes"),
    candidates: Array.isArray(row.candidates) ? row.candidates.length : 0,
    reason: typeof row.reason === "string" ? row.reason : "",
  }));
}

export function parseCacheResults(value: unknown): EnvironmentCacheResult[] {
  return records(value).map((row) => ({
    ...parseCachePreview([row])[0],
    removedBytes: number(row.removed_bytes, "removed_bytes"),
    removedCount: number(row.removed_count, "removed_count"),
  }));
}

export function parseArchives(value: unknown): EnvironmentArchive[] {
  return records(value).map((row) => ({
    archiveId: text(row.archive_id, "archive_id"),
    taskId: typeof row.task_id === "string" ? row.task_id : "",
    taskName: typeof row.task_name === "string" ? row.task_name : "",
    kind: typeof row.kind === "string" ? row.kind : "",
    createdAt: text(row.created_at, "created_at"),
    archiveBytes: number(row.archive_bytes, "archive_bytes"),
    logicalBytes: number(row.logical_bytes, "logical_bytes"),
    restoreReason: typeof row.restore_reason === "string" ? row.restore_reason : "",
  }));
}

export function parseArchiveResults(value: unknown): EnvironmentArchiveResult[] {
  return records(value).map((row) => ({
    environmentId: typeof row.environment_id === "string" ? row.environment_id : "",
    revision: typeof row.revision === "string" ? row.revision : "",
    archiveId: typeof row.archive_id === "string" ? row.archive_id : "",
    reason: typeof row.reason === "string" ? row.reason : "",
    originalBytes: number(row.original_bytes, "original_bytes"),
    archiveBytes: number(row.archive_bytes, "archive_bytes"),
    reclaimed: row.reclaimed === true,
    restored: row.restored === true,
  }));
}
