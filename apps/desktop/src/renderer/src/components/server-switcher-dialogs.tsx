"use client";

import { useEffect } from "react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { useT } from "@multica/views/i18n";
import { useAuthStore } from "@multica/core/auth";
import { switchToServer } from "../platform/desktop-servers";
import { useServerSwitcherStore } from "../stores/server-switcher-store";
import { ServerSettingsDialog } from "./server-settings-dialog";

/**
 * Host for the dialogs the workspace-switcher's server group triggers
 * (RUYI-59). Mounted OUTSIDE the dropdown because the dropdown portal
 * unmounts when the menu closes; the switcher store carries the intent
 * across that unmount.
 *
 * Signed-out switches skip the confirmation (mobile parity): there is no
 * session to disrupt, and the login screen is one click away either way.
 */
export function ServerSwitcherDialogs() {
  const { t } = useT("settings");
  const pendingSwitch = useServerSwitcherStore((s) => s.pendingSwitch);
  const clearPendingSwitch = useServerSwitcherStore((s) => s.clearPendingSwitch);
  const manageOpen = useServerSwitcherStore((s) => s.manageOpen);
  const closeManage = useServerSwitcherStore((s) => s.closeManage);

  const performSwitch = (serverId: string) => {
    clearPendingSwitch();
    if (switchToServer(serverId)) {
      // Full reload rebuilds every boot-time singleton (ApiClient, WS,
      // query cache, tab groups) against the target server — the same
      // mechanism as the language switch, and the desktop equivalent of
      // mobile's re-initialize on switch.
      window.location.reload();
    } else {
      toast.error(t(($) => $.server.switch_failed_message));
    }
  };

  // Signed-out users switch directly — nothing to confirm.
  useEffect(() => {
    if (pendingSwitch && !useAuthStore.getState().user) {
      performSwitch(pendingSwitch.id);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pendingSwitch]);

  return (
    <>
      <AlertDialog
        open={pendingSwitch != null}
        onOpenChange={(next) => {
          if (!next) clearPendingSwitch();
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.server.switch_title)}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.server.switch_message, {
                name: pendingSwitch?.name || pendingSwitch?.apiUrl || "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.server.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => pendingSwitch && performSwitch(pendingSwitch.id)}
            >
              {t(($) => $.server.switch_confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {manageOpen && (
        <ServerSettingsDialog open onClose={closeManage} />
      )}
    </>
  );
}
