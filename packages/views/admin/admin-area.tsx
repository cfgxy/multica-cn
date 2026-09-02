"use client";

import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuthStore } from "@multica/core/auth";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { AdminUsersPage } from "./admin-users-page";
import { AdminWorkspacesPage } from "./admin-workspaces-page";

/**
 * Top-level /admin shell shared by web and desktop. Guarded here AND by
 * RequireSuperAdmin server-side; a non-admin sees the no-access note
 * instead of the directory.
 */
export function AdminArea() {
  const { t } = useTranslation("admin");
  const user = useAuthStore((s) => s.user);
  const [tab, setTab] = useState<"users" | "workspaces">("users");

  if (user === null) return null;
  // Explicit boolean check per API-compat rules.
  if (user.is_super_admin !== true) {
    return (
      <div className="flex h-svh flex-col items-center justify-center text-muted-foreground">
        {t(($) => $.not_super_admin)}
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="border-b px-4 py-3">
        <h1 className="text-title">{t(($) => $.title)}</h1>
        <p className="text-caption text-muted-foreground">{t(($) => $.subtitle)}</p>
      </div>
      <Tabs
        value={tab}
        onValueChange={(v) => {
          if (v === "users" || v === "workspaces") setTab(v);
        }}
        className="flex min-h-0 flex-1 flex-col"
      >
        <TabsList className="mx-4 mt-3 w-fit">
          <TabsTrigger value="users">{t(($) => $.tabs.users)}</TabsTrigger>
          <TabsTrigger value="workspaces">{t(($) => $.tabs.workspaces)}</TabsTrigger>
        </TabsList>
        <TabsContent value="users" className="min-h-0 flex-1 overflow-hidden">
          <AdminUsersPage />
        </TabsContent>
        <TabsContent value="workspaces" className="min-h-0 flex-1 overflow-hidden">
          <AdminWorkspacesPage />
        </TabsContent>
      </Tabs>
    </div>
  );
}
