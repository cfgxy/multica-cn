/**
 * Right-side actions for the Chat tab header.
 *
 * RUYI-51: the ⋯ button used to fire the delete confirm immediately — the
 * icon promised a menu but delivered a destructive action. It is now a
 * DropdownMenu matching the issue detail screen's interaction (same RNR
 * DropdownMenu, align="end", destructive item last behind a separator), and
 * it sits RIGHTMOST in the header — the unified "More lives at the far
 * right" rule shared with inbox / issue / project headers. The + (new chat)
 * button moved to its left.
 *
 * Menu items mirror web's ChatSessionHeader ⋯ menu
 * (packages/views/chat/components/chat-session-header.tsx):
 * rename / pin / archive|unarchive / delete. Labels reuse the existing
 * shared `chat:header.*` + `chat:list.*` keys — no new locale strings.
 *
 * Cross-platform: uses @rn-primitives DropdownMenu (pure JS, no native
 * deps) — same reason as issue/[id].tsx (ActionSheetIOS crashed Android).
 */
import { IconButton } from "@/components/ui/icon-button";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { Text } from "@/components/ui/text";
import { useT } from "@/lib/use-t";

interface Props {
  showMore: boolean;
  isArchived: boolean;
  isPinned: boolean;
  onRename: () => void;
  onTogglePin: () => void;
  onToggleArchive: () => void;
  onDelete: () => void;
  onNewPress: () => void;
}

export function ChatSessionActions({
  showMore,
  isArchived,
  isPinned,
  onRename,
  onTogglePin,
  onToggleArchive,
  onDelete,
  onNewPress,
}: Props) {
  const { t } = useT("chat");
  return (
    <>
      <IconButton
        name="add"
        iconSize={24}
        onPress={onNewPress}
        accessibilityLabel={t("window.new_chat_tooltip", "New chat")}
      />
      {showMore ? (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <IconButton
              name="ellipsis-horizontal"
              accessibilityLabel={t(
                "mobile.sessions.actions_a11y",
                "Session actions",
              )}
            />
          </DropdownMenuTrigger>
          {/* Same popover geometry as the issue / project / inbox menus
              (align="end", ~w-44/48) so the "More" popover feels identical
              across screens. */}
          <DropdownMenuContent align="end" className="w-44">
            <DropdownMenuItem onPress={onRename}>
              <Text>{t("header.rename", "Rename chat")}</Text>
            </DropdownMenuItem>
            <DropdownMenuItem onPress={onTogglePin}>
              <Text>
                {isPinned
                  ? t("list.unpin", "Unpin")
                  : t("list.pin", "Pin")}
              </Text>
            </DropdownMenuItem>
            <DropdownMenuItem onPress={onToggleArchive}>
              <Text>
                {isArchived
                  ? t("header.unarchive", "Unarchive chat")
                  : t("header.archive", "Archive chat")}
              </Text>
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onPress={onDelete}>
              <Text>{t("header.delete", "Delete chat")}</Text>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
    </>
  );
}
