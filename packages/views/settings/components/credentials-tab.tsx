"use client";

import { SettingsTab } from "./settings-layout";
import { CredentialManager } from "../../credentials/credential-manager";
import { useT } from "../../i18n";

export function CredentialsTab() {
  const { t } = useT("settings");
  return (
    <SettingsTab
      title={t(($) => $.page.tabs.credentials)}
      description={t(($) => $.credential_manager.description)}
    >
      <CredentialManager workspaceMode />
    </SettingsTab>
  );
}
