import {
  dedupeIssueReferences,
  extractIssueReferences,
  type IssueReference,
} from "@multica/core/markdown";

const MAX_ENTRIES = 128;
const MAX_KEY_LENGTH = 256 * 1024;
const cache = new Map<string, IssueReference[]>();
let cachedKeyLength = 0;

export function cachedIssueReferences(
  content: string,
  workspacePrefix: string | null | undefined,
): IssueReference[] {
  const key = JSON.stringify([workspacePrefix ?? null, content]);
  const cached = cache.get(key);
  if (cached) {
    cache.delete(key);
    cache.set(key, cached);
    return cached;
  }

  const references = dedupeIssueReferences(
    extractIssueReferences(content, { workspacePrefix }),
  );
  if (key.length > MAX_KEY_LENGTH) return references;

  cache.set(key, references);
  cachedKeyLength += key.length;
  while (cache.size > MAX_ENTRIES || cachedKeyLength > MAX_KEY_LENGTH) {
    const oldest = cache.keys().next().value;
    if (oldest === undefined) break;
    cachedKeyLength -= oldest.length;
    cache.delete(oldest);
  }
  return references;
}
