interface ReloadableWindow {
  isDestroyed: () => boolean;
  close: () => void;
  webContents: { reload: () => void };
}

/** The main window remains authoritative when an issue renderer changes server. */
export function reloadForServerSwitch<T extends ReloadableWindow>(
  source: T | null,
  main: T | null,
  issues: Set<T>,
): boolean {
  if (!source || (source !== main && !issues.has(source))) return false;
  for (const issue of issues) {
    if (!issue.isDestroyed()) issue.close();
  }
  if (main && !main.isDestroyed()) main.webContents.reload();
  return true;
}
