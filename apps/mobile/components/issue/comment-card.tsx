/**
 * Comment timeline row. Rounded gray bubble containing the parent comment
 * plus, when applicable, every descendant reply stacked inline. The bubble
 * boundary itself is the thread indicator — no "↪ Replying to" header, no
 * recursive indentation. This matches the user's design call: "放在一个 card
 * 内部就行了 / no need for the Replying to label".
 *
 * Mobile flat-list rule (apps/mobile/CLAUDE.md): same comments as web,
 * different layout — web shows recursive tree, mobile shows one bubble per
 * thread. Counts agree (no comment is dropped or duplicated).
 *
 * V1 rendering rules (locked in plan):
 *   - Body is rendered as raw plain text. Markdown source like `**bold**`
 *     shows literally. The `react-native-marked` adapter is V2.
 *   - No reactions, no edit/delete affordances. Attachments render as a
 *     one-line "📎 N attachments" hint when present.
 *   - `(edited)` marker shown when updated_at differs from created_at.
 */
import { View } from "react-native";
import type { TimelineEntry } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { useActorLookup } from "@/data/use-actor-name";
import { timeAgo } from "@/lib/time-ago";
import { Markdown } from "@/lib/markdown";

interface Props {
  entry: TimelineEntry;
  /** Flattened descendant replies. Rendered inline below the parent inside
   *  the same bubble, separated by a hairline divider. */
  replies?: TimelineEntry[];
}

export function CommentCard({ entry, replies = [] }: Props) {
  return (
    <View className="px-4">
      <View className="bg-secondary rounded-2xl px-4 py-3 gap-3">
        <CommentBody entry={entry} />
        {replies.map((reply) => (
          <View
            key={reply.id}
            className="border-t border-border/60 pt-3"
          >
            <CommentBody entry={reply} />
          </View>
        ))}
      </View>
    </View>
  );
}

function CommentBody({ entry }: { entry: TimelineEntry }) {
  const { getName } = useActorLookup();
  const name = getName(
    entry.actor_type as "member" | "agent" | null | undefined,
    entry.actor_id,
  );
  const edited =
    entry.updated_at &&
    entry.created_at &&
    entry.updated_at !== entry.created_at;
  // Note: entry.attachments is not rendered separately — the markdown
  // renderer handles inline images (`![]()`) and file cards
  // (`!file[name](url)` → preprocessed into a 📎-prefixed link). The
  // attachments[] array is backend cleanup metadata, not display content
  // (matches web's behavior).
  return (
    <View className="gap-2">
      <View className="flex-row items-center gap-2">
        <ActorAvatar
          type={entry.actor_type as "member" | "agent"}
          id={entry.actor_id}
          size={24}
        />
        <Text className="text-sm font-medium text-foreground">{name}</Text>
        <Text className="text-xs text-muted-foreground">
          · {timeAgo(entry.created_at)}
          {edited ? " · (edited)" : ""}
        </Text>
      </View>
      {entry.content ? <Markdown content={entry.content} /> : null}
    </View>
  );
}
