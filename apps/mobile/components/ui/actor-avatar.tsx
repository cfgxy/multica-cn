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
import { Text } from "@/components/ui/text";
import { cn } from "@/lib/utils";
import { useActorLookup, getInitials } from "@/data/use-actor-name";

interface Props {
  type: "member" | "agent" | null | undefined;
  id: string | null | undefined;
  size?: number;
}

export function ActorAvatar({ type, id, size = 32 }: Props) {
  const { getName, getAvatarUrl } = useActorLookup();
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
