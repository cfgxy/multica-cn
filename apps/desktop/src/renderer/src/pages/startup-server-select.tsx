import { useCallback, useEffect, useRef, useState } from "react";
import { Loader2, Plus, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import { MulticaIcon } from "@multica/ui/components/common/multica-icon";
import { useT } from "@multica/views/i18n";
import { DragStrip } from "@multica/views/platform";
import { useServerStore, switchToServer } from "../platform/desktop-servers";
import { probeServer } from "../platform/probe-server";
import { useServerSwitcherStore } from "../stores/server-switcher-store";
import { ServerSettingsDialog } from "../components/server-settings-dialog";

const COUNTDOWN_SECONDS = 5;
type ProbeState = "checking" | "reachable" | "unreachable";

export function StartupServerSelect({
  previousId,
  onContinue,
  onClose,
}: {
  previousId: string | null;
  onContinue?: () => void;
  onClose?: () => void;
}) {
  const { t } = useT("settings");
  const servers = useServerStore((s) => s.servers);
  const activeId = useServerStore((s) => s.activeServerId);
  const openManage = useServerSwitcherStore((s) => s.openManage);
  const manageOpen = useServerSwitcherStore((s) => s.manageOpen);
  const closeManage = useServerSwitcherStore((s) => s.closeManage);
  const [countdown, setCountdown] = useState<number | null>(previousId ? COUNTDOWN_SECONDS : null);
  const [connectingId, setConnectingId] = useState<string | null>(null);
  const [probes, setProbes] = useState<Record<string, ProbeState>>({});
  const controllers = useRef<AbortController[]>([]);
  const connecting = useRef(false);

  const recheck = useCallback(() => {
    controllers.current.forEach((controller) => controller.abort());
    const next = servers.map(() => new AbortController());
    controllers.current = next;
    setProbes(Object.fromEntries(servers.map((server) => [server.id, "checking"])));
    servers.forEach((server, index) => {
      const controller = next[index];
      void probeServer(server.apiUrl, controller.signal).then((reachable) => {
        if (controllers.current !== next) return;
        setProbes((current) => ({ ...current, [server.id]: reachable && !controller.signal.aborted ? "reachable" : "unreachable" }));
      });
    });
  }, [servers]);

  useEffect(() => {
    recheck();
    return () => {
      controllers.current.forEach((controller) => controller.abort());
      controllers.current = [];
    };
  }, [recheck]);

  const connect = useCallback((id: string) => {
    if (connecting.current) return;
    connecting.current = true;
    setCountdown(null);
    setConnectingId(id);
    if (id === activeId && (previousId !== null || !onContinue)) {
      if (onContinue) onContinue();
      else onClose?.();
      return;
    }
    try {
      if (!switchToServer(id)) throw new Error("Server is unavailable");
      window.desktopAPI.applyServerSwitch();
    } catch {
      connecting.current = false;
      setConnectingId(null);
      toast.error(t(($) => $.server.switch_failed_message));
    }
  }, [activeId, onClose, onContinue, previousId, t]);

  useEffect(() => {
    if (countdown === null) return;
    if (countdown === 0) {
      if (previousId && servers.some((server) => server.id === previousId)) connect(previousId);
      else setCountdown(null);
      return;
    }
    const timer = setTimeout(() => setCountdown(countdown - 1), 1_000);
    return () => clearTimeout(timer);
  }, [countdown, previousId, servers, connect]);

  useEffect(() => {
    if (servers.length === 1 && onContinue && !connecting.current) connect(servers[0].id);
  }, [servers, onContinue, connect]);

  const previous = servers.find((s) => s.id === previousId);
  const pending = Object.values(probes).some((state) => state === "checking");
  const interrupt = () => setCountdown(null);

  return (
    <div className="flex h-screen flex-col bg-background">
      <DragStrip />
      <main className="flex flex-1 items-center justify-center overflow-y-auto p-6">
        <div className="w-full max-w-sm space-y-5">
          <div className="text-center">
            <MulticaIcon bordered size="lg" />
            <h1 className="mt-5 text-title font-semibold">{t(($) => $.server.startup.title)}</h1>
          </div>
          {countdown !== null && previous && (
            <div className="rounded-md border bg-card p-3" aria-live="polite">
              <p className="text-body">{t(($) => $.server.startup.countdown, { n: countdown, name: previous.name || previous.apiUrl })}</p>
              <div className="mt-3 flex gap-2">
                <Button size="sm" onClick={() => connect(previous.id)} disabled={connectingId !== null}>
                  {t(($) => $.server.startup.connect_now)}
                </Button>
                <Button size="sm" variant="ghost" onClick={interrupt}>
                  {t(($) => $.server.startup.cancel_auto)}
                </Button>
              </div>
              <div className="mt-3 h-0.5 bg-muted">
                <div className="h-full bg-primary" style={{ width: `${(countdown / COUNTDOWN_SECONDS) * 100}%` }} />
              </div>
            </div>
          )}
          <div className="overflow-hidden rounded-md border bg-card">
            {servers.map((server) => {
              const status = probes[server.id] ?? "checking";
              return (
                <button key={server.id} type="button" disabled={connectingId !== null}
                  onClick={() => connect(server.id)}
                  className="flex w-full items-center gap-3 px-3 py-2.5 text-left hover:bg-accent disabled:opacity-60">
                  <span role="img" aria-label={t(($) => $.server.startup[status])}
                    className={`size-2 shrink-0 rounded-full ${status === "reachable" ? "bg-green-600" : status === "unreachable" ? "bg-destructive" : "border border-muted-foreground animate-pulse"}`} />
                  <span className="min-w-0 flex-1 truncate text-body">{server.name || server.apiUrl}</span>
                  {server.builtIn && <span className="text-caption text-muted-foreground">{t(($) => $.server.built_in)}</span>}
                  {connectingId === server.id ? <Loader2 className="size-4 animate-spin" /> : countdown !== null && server.id === previousId ? <span className="text-caption text-muted-foreground">{t(($) => $.server.startup.next)}</span> : null}
                </button>
              );
            })}
          </div>
          <div className="flex items-center justify-between gap-2">
            <Button size="sm" variant="ghost" disabled={pending || connectingId !== null} onClick={() => { interrupt(); recheck(); }}>
              <RefreshCw className="size-4" />{t(($) => $.server.startup.recheck)}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => { interrupt(); openManage(); }} disabled={connectingId !== null}>
              <Plus className="size-4" />{t(($) => $.server.manage_title)}
            </Button>
          </div>
          {onClose && <Button variant="outline" className="w-full" onClick={onClose}>{t(($) => $.server.cancel)}</Button>}
        </div>
      </main>
      {manageOpen && <ServerSettingsDialog open onClose={closeManage} preAuth={onContinue != null} />}
    </div>
  );
}
