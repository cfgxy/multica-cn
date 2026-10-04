/**
 * Squad member row (RUYI-346 S2) — one roster entry on the squad detail
 * screen, addressed by the (member_type, member_id) pair like every roster
 * API. The status pill renders the server-derived five-way bucket from
 * GET /members/status; humans carry status === null and get no pill (same
 * as web), and any UNKNOWN future value also renders no pill rather than a
 * wrong label — the switch's default branch is the neutral fallback
 * (`lib/squad-status-pill.ts` holds the mapping and its unit tests).
 */
import { useState } from "react";
import { Pressable, Text, TextInput, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import type { SquadMember, SquadMemberStatus } from "@multica/core/types";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { useActorLookup } from "@/data/use-actor-name";
import {
  formatLastActive,
  squadMemberStatusPill,
  type SquadStatusPill,
} from "@/lib/squad-status-pill";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

interface Props {
  member: SquadMember;
  /** Is this member the squad leader (leader chip)? */
  isLeader: boolean;
  status?: SquadMemberStatus | null;
  canManage: boolean;
  busy?: boolean;
  /** Agent members only — navigate to agent detail. */
  onOpenAgent?: () => void;
  onMakeLeader?: () => void;
  onRemove?: () => void;
  /** Managers only — commit a role edit (empty string clears the role). */
  onUpdateRole?: (role: string) => void;
}

const PILL_BG: Record<SquadStatusPill["tone"], string> = {
  success: "bg-success/15",
  warning: "bg-warning/15",
  brand: "bg-brand/10",
  muted: "bg-muted-foreground/10",
};

const PILL_TEXT: Record<SquadStatusPill["tone"], string> = {
  success: "text-success",
  warning: "text-warning",
  brand: "text-brand",
  muted: "text-muted-foreground",
};

export function SquadMemberRow({
  member,
  isLeader,
  status,
  canManage,
  busy,
  onOpenAgent,
  onMakeLeader,
  onRemove,
  onUpdateRole,
}: Props) {
  const { t } = useT("squads");
  const { getName } = useActorLookup();
  const { colorScheme } = useColorScheme();
  const [roleEditing, setRoleEditing] = useState(false);
  const [roleDraft, setRoleDraft] = useState(member.role);

  const name = getName(member.member_type, member.member_id);
  const pill = squadMemberStatusPill(status?.status);
  const lastActive = formatLastActive(status?.last_active_at);
  const activeIssues = status?.active_issues ?? [];

  return (
    <View className="rounded-lg border border-border px-3 py-2.5 gap-1">
      <View className="flex-row items-center gap-3">
        {member.member_type === "agent" && onOpenAgent ? (
          <Pressable
            onPress={onOpenAgent}
            hitSlop={4}
            className="flex-1 flex-row items-center gap-3 active:opacity-70"
            accessibilityRole="button"
            accessibilityLabel={t(
              "members_tab.view_agent_tooltip",
              "Open agent detail",
            )}
          >
            <RowIdentity
              name={name}
              memberType={member.member_type}
              memberId={member.member_id}
            />
          </Pressable>
        ) : (
          <View className="flex-1 flex-row items-center gap-3">
            <RowIdentity
              name={name}
              memberType={member.member_type}
              memberId={member.member_id}
            />
          </View>
        )}
        {isLeader ? (
          <View className="rounded bg-brand/10 px-1.5 py-0.5">
            <Text className="text-[10px] font-medium text-brand">
              {t("members_tab.leader_chip", "Leader")}
            </Text>
          </View>
        ) : null}
        {pill ? (
          <View className={`rounded px-1.5 py-0.5 ${PILL_BG[pill.tone]}`}>
            <Text className={`text-[10px] font-medium ${PILL_TEXT[pill.tone]}`}>
              {t(pill.key, pill.fallback)}
            </Text>
          </View>
        ) : null}
        {canManage ? (
          <View className="flex-row items-center gap-2">
            {!isLeader && member.member_type === "agent" && onMakeLeader ? (
              <Pressable
                onPress={onMakeLeader}
                disabled={busy}
                hitSlop={6}
                accessibilityRole="button"
                accessibilityLabel={t(
                  "members_tab.make_leader_tooltip",
                  "Make squad leader",
                )}
              >
                <Ionicons
                  name="star-outline"
                  size={16}
                  color={THEME[colorScheme].mutedForeground}
                />
              </Pressable>
            ) : null}
            {onRemove ? (
              <Pressable
                onPress={onRemove}
                disabled={busy}
                hitSlop={6}
                accessibilityRole="button"
                accessibilityLabel={t(
                  "members_tab.remove_member_tooltip",
                  "Remove from squad",
                )}
              >
                <Ionicons
                  name="remove-circle-outline"
                  size={16}
                  color={THEME[colorScheme].destructive}
                />
              </Pressable>
            ) : null}
          </View>
        ) : null}
      </View>
      {canManage && onUpdateRole && roleEditing ? (
        <TextInput
          value={roleDraft}
          onChangeText={setRoleDraft}
          placeholder={
            member.role
              ? t("role_editor.placeholder", "Role (e.g. Reviewer)")
              : t("role_editor.empty", "Add role…")
          }
          placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
          autoFocus
          autoCorrect={false}
          className="text-xs text-foreground bg-secondary/50 rounded px-2 py-1 ml-11"
          onBlur={() => {
            setRoleEditing(false);
            if (roleDraft.trim() !== member.role) onUpdateRole(roleDraft.trim());
          }}
          onSubmitEditing={() => {
            setRoleEditing(false);
            if (roleDraft.trim() !== member.role) onUpdateRole(roleDraft.trim());
          }}
        />
      ) : member.role ? (
        canManage && onUpdateRole ? (
          <Pressable
            onPress={() => {
              setRoleDraft(member.role);
              setRoleEditing(true);
            }}
            hitSlop={4}
            className="self-start"
            accessibilityRole="button"
            accessibilityLabel={t("role_editor.placeholder", "Role (e.g. Reviewer)")}
          >
            <Text className="text-xs text-muted-foreground pl-11 underline">
              {member.role}
            </Text>
          </Pressable>
        ) : (
          <Text className="text-xs text-muted-foreground pl-11">{member.role}</Text>
        )
      ) : canManage && onUpdateRole ? (
        <Pressable
          onPress={() => {
            setRoleDraft("");
            setRoleEditing(true);
          }}
          hitSlop={4}
          className="self-start"
          accessibilityRole="button"
          accessibilityLabel={t("role_editor.empty", "Add role…")}
        >
          <Text className="text-xs text-muted-foreground/60 pl-11">
            {t("role_editor.empty", "Add role…")}
          </Text>
        </Pressable>
      ) : null}
      {activeIssues.length > 0 ? (
        <Text numberOfLines={1} className="text-xs text-muted-foreground pl-11">
          {activeIssues
            .map((issue) => issue.identifier)
            .slice(0, 3)
            .join(", ")}
        </Text>
      ) : lastActive ? (
        <Text className="text-xs text-muted-foreground pl-11">
          {t("members_tab.last_active_label", "last active {{time}}", {
            time: lastActive,
          })}
        </Text>
      ) : null}
    </View>
  );
}

function RowIdentity({
  name,
  memberType,
  memberId,
}: {
  name: string;
  memberType: SquadMember["member_type"];
  memberId: string;
}) {
  const { t } = useT("squads");
  return (
    <>
      <ActorAvatar type={memberType} id={memberId} size={36} />
      <View className="flex-1 min-w-0">
        <Text
          numberOfLines={1}
          className="text-sm font-medium text-foreground"
        >
          {name}
        </Text>
        <Text className="text-[10px] uppercase tracking-wide text-muted-foreground">
          {memberType === "agent"
            ? t("member_type.agent", "Agent")
            : t("member_type.member", "Member")}
        </Text>
      </View>
    </>
  );
}
