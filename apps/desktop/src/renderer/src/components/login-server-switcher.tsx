"use client";

import { ServerCog } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { useT } from "@multica/views/i18n";
import { ServerSwitcherDialogs } from "./server-switcher-dialogs";
import { ServerSwitcherGroup } from "./server-switcher-group";

export function LoginServerSwitcher() {
  const { t } = useT("auth");
  const label = t(($) => $.mobile.server_settings);

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-label={label}
              className="text-muted-foreground"
            />
          }
        >
          <ServerCog className="size-3.5" />
          {label}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="center" side="top" className="w-72">
          <ServerSwitcherGroup />
        </DropdownMenuContent>
      </DropdownMenu>
      <ServerSwitcherDialogs />
    </>
  );
}
