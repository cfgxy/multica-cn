import { useCallback, useSyncExternalStore } from "react";

type Listener = () => void;

const visibleByScope = new Map<string, ReadonlySet<string>>();
const listenersByKey = new Map<string, Set<Listener>>();
const visibleBodiesByScope = new Map<string, ReadonlySet<string>>();
const bodyListenersByScope = new Map<string, Map<string, Set<Listener>>>();
const bodyHeightsByScope = new Map<string, Map<string, number>>();
const POTENTIAL_REFERENCE_SHAPE =
  /[A-Z][A-Z0-9]*-\d|\]\(mention:\/\/issue\/|[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9][A-Za-z0-9_.-]*#\d|(?<![A-Za-z0-9])MR ?!\d/;

export function hasPotentialIssueReference(content: string): boolean {
  return POTENTIAL_REFERENCE_SHAPE.test(content);
}

function subscriptionKey(scope: string, commentId: string): string {
  return `${scope}\u0000${commentId}`;
}

export function isIssueReferenceTailVisible(
  scope: string,
  commentId: string,
): boolean {
  return visibleByScope.get(scope)?.has(commentId) ?? false;
}

export function subscribeIssueReferenceTailVisibility(
  scope: string,
  commentId: string,
  listener: Listener,
): () => void {
  const key = subscriptionKey(scope, commentId);
  const listeners = listenersByKey.get(key) ?? new Set<Listener>();
  listeners.add(listener);
  listenersByKey.set(key, listeners);

  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) listenersByKey.delete(key);
  };
}

export function replaceIssueReferenceTailVisibleIds(
  scope: string,
  next: ReadonlySet<string>,
): void {
  const previous = visibleByScope.get(scope) ?? new Set<string>();
  const changed = new Set<string>();

  for (const id of previous) if (!next.has(id)) changed.add(id);
  for (const id of next) if (!previous.has(id)) changed.add(id);
  if (changed.size === 0) return;

  visibleByScope.set(scope, new Set(next));
  for (const id of changed) {
    for (const listener of listenersByKey.get(subscriptionKey(scope, id)) ?? []) {
      listener();
    }
  }
}

export function clearIssueReferenceTailVisibility(scope: string): void {
  replaceIssueReferenceTailVisibleIds(scope, new Set());
  visibleByScope.delete(scope);
}

export function useIssueReferenceTailVisible(
  scope: string | undefined,
  commentId: string,
): boolean {
  const subscribe = useCallback(
    (listener: Listener) =>
      scope
        ? subscribeIssueReferenceTailVisibility(scope, commentId, listener)
        : () => {},
    [scope, commentId],
  );
  const getSnapshot = useCallback(
    () => (scope ? isIssueReferenceTailVisible(scope, commentId) : true),
    [scope, commentId],
  );

  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

export function isIssueCommentBodyVisible(
  scope: string,
  commentId: string,
): boolean {
  return visibleBodiesByScope.get(scope)?.has(commentId) ?? true;
}

export function replaceIssueCommentBodyVisibleIds(
  scope: string,
  next: ReadonlySet<string>,
): void {
  const previous = visibleBodiesByScope.get(scope);
  visibleBodiesByScope.set(scope, new Set(next));

  for (const [commentId, listeners] of bodyListenersByScope.get(scope) ?? []) {
    const wasVisible = previous?.has(commentId) ?? true;
    if (wasVisible === next.has(commentId)) continue;
    for (const listener of listeners) listener();
  }
}

export function subscribeIssueCommentBodyVisibility(
  scope: string,
  commentId: string,
  listener: Listener,
): () => void {
  const scopedListeners = bodyListenersByScope.get(scope) ?? new Map();
  const listeners = scopedListeners.get(commentId) ?? new Set<Listener>();
  listeners.add(listener);
  scopedListeners.set(commentId, listeners);
  bodyListenersByScope.set(scope, scopedListeners);

  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      scopedListeners.delete(commentId);
      if (scopedListeners.size === 0) bodyListenersByScope.delete(scope);
    }
  };
}

export function useIssueCommentBodyVisible(
  scope: string | undefined,
  commentId: string,
): boolean {
  const subscribe = useCallback(
    (listener: Listener) =>
      scope
        ? subscribeIssueCommentBodyVisibility(scope, commentId, listener)
        : () => {},
    [scope, commentId],
  );
  const getSnapshot = useCallback(
    () => (scope ? isIssueCommentBodyVisible(scope, commentId) : true),
    [scope, commentId],
  );

  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

export function getIssueCommentBodyHeight(
  scope: string,
  commentId: string,
): number | null {
  return bodyHeightsByScope.get(scope)?.get(commentId) ?? null;
}

export function setIssueCommentBodyHeight(
  scope: string,
  commentId: string,
  height: number,
): void {
  const heights = bodyHeightsByScope.get(scope) ?? new Map();
  heights.set(commentId, height);
  bodyHeightsByScope.set(scope, heights);
}

export function clearIssueCommentBodyVisibility(scope: string): void {
  visibleBodiesByScope.delete(scope);
  bodyHeightsByScope.delete(scope);
}
