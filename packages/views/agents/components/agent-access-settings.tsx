"use client";

import type { Agent, MemberWithUser } from "@multica/core/types";
import { canEditAgentAccess } from "@multica/core/permissions";
import {
  SettingsCard,
  SettingsSection,
} from "../../settings/components/settings-layout";
import { useT } from "../../i18n";
import { AccessPicker } from "./inspector/access-picker";

export function AgentAccessSettings({
  agent,
  members,
  currentUserId,
  onDirtyChange,
  onUpdate,
}: {
  agent: Agent;
  members: MemberWithUser[];
  currentUserId: string | null;
  onDirtyChange?: (dirty: boolean) => void;
  onUpdate: (id: string, data: Record<string, unknown>) => Promise<void>;
}) {
  const { t } = useT("agents");
  const currentUserRole =
    members.find((member) => member.user_id === currentUserId)?.role ?? null;

  return (
    <SettingsSection
      title={t(($) => $.access.section_title)}
      description={t(($) => $.inspector.section_access_hint)}
    >
      <SettingsCard>
        <AccessPicker
          permissionMode={agent.permission_mode}
          invocationTargets={agent.invocation_targets}
          visibility={agent.visibility}
          members={members}
          ownerId={agent.owner_id}
          canEdit={canEditAgentAccess(agent, {
            userId: currentUserId,
            role: currentUserRole,
          }).allowed}
          hasComposioAllowlist={
            (agent.composio_toolkit_allowlist ?? []).length > 0
          }
          onDirtyChange={onDirtyChange}
          onChange={(next) => onUpdate(agent.id, next)}
        />
      </SettingsCard>
    </SettingsSection>
  );
}
