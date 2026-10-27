"use client";

import { Check, Server, ServerCog } from "lucide-react";
import { useServerStore } from "../platform/desktop-servers";
import { useServerSwitcherStore } from "../stores/server-switcher-store";
import {
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
} from "@multica/ui/components/ui/dropdown-menu";
import { useT } from "@multica/views/i18n";

/**
 * 【服务器】group inside the workspace switcher dropdown (RUYI-59) — the
 * desktop counterpart of the mobile server settings. Lists the configured
 * multica instances with the active one marked, plus the manage entry.
 *
 * Rendered inside the dropdown portal via AppSidebar's serversSlot; the
 * dialogs it triggers live in ServerSwitcherDialogs, which is mounted
 * outside the menu because the portal unmounts on close.
 */
export function ServerSwitcherGroup() {
  const { t } = useT("layout");
  const { t: tSettings } = useT("settings");
  const servers = useServerStore((s) => s.servers);
  const activeServerId = useServerStore((s) => s.activeServerId);
  const hydrated = useServerStore((s) => s.hydrated);
  const requestSwitch = useServerSwitcherStore((s) => s.requestSwitch);
  const openManage = useServerSwitcherStore((s) => s.openManage);

  if (!hydrated) return null;

  return (
    <DropdownMenuGroup data-testid="server-switcher-group">
      <DropdownMenuLabel className="text-caption text-muted-foreground">
        {t(($) => $.sidebar.servers_label)}
      </DropdownMenuLabel>
      {servers.map((entry) => {
        const isActive = entry.id === activeServerId;
        return (
          <DropdownMenuItem
            key={entry.id}
            onClick={() => {
              // Same split as mobile: the active entry no-ops, everything
              // else routes through the confirm dialog outside the menu
              // (the host skips the confirmation when signed out).
              if (entry.id !== activeServerId) requestSwitch(entry);
            }}
          >
            <Server className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate">
              {entry.name || entry.apiUrl}
            </span>
            {entry.builtIn && (
              <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-micro text-muted-foreground">
                {tSettings(($) => $.server.built_in)}
              </span>
            )}
            {isActive && <Check className="h-3.5 w-3.5 shrink-0 text-primary" />}
          </DropdownMenuItem>
        );
      })}
      <DropdownMenuItem
        data-testid="manage-servers-item"
        onClick={() => openManage()}
      >
        <ServerCog className="size-3.5 shrink-0 text-muted-foreground" />
        {t(($) => $.sidebar.manage_servers)}
      </DropdownMenuItem>
    </DropdownMenuGroup>
  );
}
