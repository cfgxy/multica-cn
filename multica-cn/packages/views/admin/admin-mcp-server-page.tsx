"use client";

// Admin → MCP 服务器（RUYI-420）。只读状态页：本进程的 OAuth 授权服务
// 配置（发现端点、签名 key）+ Node MCP 进程的存活与工具目录。纯展示，
// 页面上的任何动作都不会触发 Agent Run。

import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { adminMCPStatusOptions } from "@multica/core/oauth-admin";
import { Badge } from "@multica/ui/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { SettingsSection, SettingsTab } from "../settings/components/settings-layout";

function StatusDot({ ok }: { ok: boolean }) {
  return (
    <span
      aria-hidden
      className={
        ok === true
          ? "inline-block size-2 rounded-full bg-emerald-500"
          : "inline-block size-2 rounded-full bg-red-500"
      }
    />
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5 py-2.5 md:flex-row md:items-baseline md:gap-4">
      <span className="w-56 shrink-0 text-caption font-medium text-muted-foreground">{label}</span>
      <span className="min-w-0 break-all font-mono text-caption">{children}</span>
    </div>
  );
}

export function AdminMCPServerPage() {
  const { t } = useTranslation("admin");
  const { data, isLoading } = useQuery(adminMCPStatusOptions());

  if (isLoading === true || data === undefined) {
    return (
      <div className="space-y-4 p-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-40 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }

  const oauth = data.oauth;
  const mcp = data.mcp;

  return (
    <SettingsTab title={t(($) => $.mcp_server.title)} description={t(($) => $.mcp_server.description)}>
      <SettingsSection title={t(($) => $.mcp_server.oauth_section)}>
        <div className="divide-y rounded-lg border">
          <Row label={t(($) => $.mcp_server.oauth_enabled)}>
            <span className="flex items-center gap-2">
              <StatusDot ok={oauth.enabled} />
              {oauth.enabled === true
                ? <Badge variant="outline">{t(($) => $.mcp_server.state_on)}</Badge>
                : <Badge variant="secondary">{t(($) => $.mcp_server.state_off)}</Badge>}
            </span>
          </Row>
          <Row label={t(($) => $.mcp_server.issuer)}>{oauth.issuer || "—"}</Row>
          <Row label={t(($) => $.mcp_server.authorization_endpoint)}>{oauth.authorization_endpoint || "—"}</Row>
          <Row label={t(($) => $.mcp_server.token_endpoint)}>{oauth.token_endpoint || "—"}</Row>
          <Row label={t(($) => $.mcp_server.jwks_url)}>{oauth.jwks_url || "—"}</Row>
          <Row label={t(($) => $.mcp_server.protected_resource_url)}>{oauth.protected_resource_url || "—"}</Row>
          <Row label={t(($) => $.mcp_server.key_id)}>{oauth.key_id || "—"}</Row>
          <Row label={t(($) => $.mcp_server.clients_count)}>
            {t(($) => $.mcp_server.count_value, { active: data.clients.active, total: data.clients.total })}
          </Row>
          <Row label={t(($) => $.mcp_server.grants_count)}>
            {t(($) => $.mcp_server.count_value, { active: data.grants.active, total: data.grants.total })}
          </Row>
        </div>
      </SettingsSection>

      <SettingsSection title={t(($) => $.mcp_server.process_section)}>
        <div className="divide-y rounded-lg border">
          <Row label={t(($) => $.mcp_server.mcp_url_configured)}>
            <span className="flex items-center gap-2">
              <StatusDot ok={mcp.url_configured} />
              {mcp.url_configured === true
                ? t(($) => $.mcp_server.state_on)
                : t(($) => $.mcp_server.mcp_url_unset)}
            </span>
          </Row>
          <Row label={t(($) => $.mcp_server.mcp_reachable)}>
            {mcp.url_configured === false
              ? "—"
              : (
                <span className="flex items-center gap-2">
                  <StatusDot ok={mcp.reachable} />
                  {mcp.reachable === true
                    ? `${mcp.version || "unknown"}`
                    : t(($) => $.mcp_server.mcp_unreachable)}
                </span>
              )}
          </Row>
        </div>
      </SettingsSection>

      {mcp.reachable === true && mcp.tools.length > 0 && (
        <SettingsSection title={t(($) => $.mcp_server.tools_title, { count: mcp.tool_count })}>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-64">{t(($) => $.mcp_server.col_tool)}</TableHead>
                <TableHead>{t(($) => $.mcp_server.col_description)}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {mcp.tools.map((tool) => (
                <TableRow key={tool.name}>
                  <TableCell className="font-mono text-caption">{tool.name}</TableCell>
                  <TableCell className="text-caption text-muted-foreground">{tool.description}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </SettingsSection>
      )}
    </SettingsTab>
  );
}
