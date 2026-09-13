export function shouldResolveWorkspaceMembership(
  isServerSwitching: boolean,
): boolean {
  return !isServerSwitching;
}
