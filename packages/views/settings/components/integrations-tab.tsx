"use client";

import { LarkTab } from "./lark-tab";

// Integrations is the umbrella tab for third-party platform connections.
// GitHub has its own top-level tab (see github-tab.tsx); everything else
// — currently just Lark — lives in here so the sidebar stays compact as
// more integrations land. Each integration owns its own section
// (heading, status, install flow); IntegrationsTab is just the host.
export function IntegrationsTab() {
  return (
    <div className="space-y-10">
      <LarkTab />
    </div>
  );
}
