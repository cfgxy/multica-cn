export function shouldRenderAuthenticatedStack(
  hasUser: boolean,
  isLoading: boolean,
  isServerSwitching: boolean,
  hadAuthenticatedSession: boolean,
): boolean {
  return hasUser || (hadAuthenticatedSession && (isLoading || isServerSwitching));
}

export function shouldHandleUnauthorized(
  isServerSwitching: boolean,
): boolean {
  return !isServerSwitching;
}
