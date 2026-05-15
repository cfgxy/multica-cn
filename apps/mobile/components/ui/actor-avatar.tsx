/**
 * Mobile ActorAvatar. Mirrors the role of packages/views/common/actor-avatar.tsx
 * (member/agent → avatar URL or initials chip), but stripped down for phone
 * use: no hover card, no presence dot, no nested focus management.
 *
 * Behavioral parity rules (apps/mobile/CLAUDE.md):
 *   - Same actor type → same name → same initials. Lookup is shared via
 *     useActorLookup which reads the same MemberWithUser / Agent lists.
 *   - Agents get distinct visual treatment (brand-tinted background) to
 *     match web's "agents render with distinct styling" rule from the
 *     repo-root CLAUDE.md "Agent Assignees" section.
 */
import { Image, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { Text } from "@/components/ui/text";
import { cn } from "@/lib/utils";
import { useActorLookup, getInitials } from "@/data/use-actor-name";

// `system` actors are server-side automation (state changes triggered by the
// platform itself, not a member or an agent). InboxItem.actor_type carries
// this third value (packages/core/types/inbox.ts:28). `squad` is a third
// assignee polymorph (packages/core/types/issue.ts IssueAssigneeType) that
// mobile doesn't yet have a list query for — render a generic group glyph so
// squad-assigned issues from web don't disappear or render as blank circles.
interface Props {
  type: "member" | "agent" | "system" | "squad" | null | undefined;
  id: string | null | undefined;
  size?: number;
}

export function ActorAvatar({ type, id, size = 32 }: Props) {
  const { getName, getAvatarUrl } = useActorLookup();

  if (type === "system") {
    return (
      <View
        style={{ width: size, height: size, borderRadius: size / 2 }}
        className="items-center justify-center bg-muted"
      >
        <Ionicons name="cog" size={Math.round(size * 0.55)} color="#71717a" />
      </View>
    );
  }

  if (type === "squad") {
    return (
      <View
        style={{ width: size, height: size, borderRadius: size / 2 }}
        className="items-center justify-center bg-muted"
      >
        <Ionicons name="people" size={Math.round(size * 0.55)} color="#71717a" />
      </View>
    );
  }

  const name = getName(type, id);
  const url = getAvatarUrl(type, id);

  if (url) {
    return (
      <Image
        source={{ uri: url }}
        style={{ width: size, height: size, borderRadius: size / 2 }}
        className="bg-muted"
      />
    );
  }

  const isAgent = type === "agent";
  return (
    <View
      style={{ width: size, height: size, borderRadius: size / 2 }}
      className={cn(
        "items-center justify-center",
        isAgent ? "bg-brand/15" : "bg-muted",
      )}
    >
      <Text
        className={cn(
          "text-xs font-medium",
          isAgent ? "text-brand" : "text-muted-foreground",
        )}
      >
        {getInitials(name)}
      </Text>
    </View>
  );
}
