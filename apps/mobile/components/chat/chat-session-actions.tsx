/**
 * Right-side actions for the Chat tab header. Two buttons:
 *   - ⋯ (session menu): only when an active session exists.
 *   - + (new chat): always shown.
 *
 * Both are RNR `<Button variant="ghost" size="icon">` via IconButton, so
 * touch feedback / sizing / dark-mode tinting are all consistent with the
 * rest of the header toolbar.
 */
import { IconButton } from "@/components/ui/icon-button";
import { useT } from "@/lib/use-t";

interface Props {
  showMore: boolean;
  onMorePress: () => void;
  onNewPress: () => void;
}

export function ChatSessionActions({
  showMore,
  onMorePress,
  onNewPress,
}: Props) {
  const { t } = useT("chat");
  return (
    <>
      {showMore ? (
        <IconButton
          name="ellipsis-horizontal"
          onPress={onMorePress}
          accessibilityLabel={t(
            "mobile.sessions.actions_a11y",
            "Session actions",
          )}
        />
      ) : null}
      <IconButton
        name="add"
        iconSize={24}
        onPress={onNewPress}
        accessibilityLabel={t("window.new_chat_tooltip", "New chat")}
      />
    </>
  );
}
