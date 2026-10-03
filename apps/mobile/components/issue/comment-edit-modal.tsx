/**
 * Edit-comment modal, opened from the long-press action sheet's Edit item
 * (useCommentLongPress → isEditing). Mounted conditionally by CommentBody
 * while the flow is open, so content state initializes from the entry's
 * current markdown each time.
 *
 * Saves through the existing useEditComment mutation — same optimistic
 * timeline patch + server replace as web's edit flow; the "(edited)"
 * suffix renders from updated_at ≠ created_at (comment-card CommentBody).
 * An empty-on-trim draft can't be saved (mobile composer's
 * requireVisibleText parity). Errors keep the modal open with the server
 * message surfaced in a native alert (contentBase conflict included).
 */
import { useCallback, useState } from "react";
import {
  Alert,
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useEditComment } from "@/data/mutations/issues";
import { AutosizeTextArea } from "@/components/ui/autosize-textarea";
import { Text } from "@/components/ui/text";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

interface Props {
  issueId: string;
  commentId: string;
  initialContent: string;
  onClose: () => void;
}

export function CommentEditModal({
  issueId,
  commentId,
  initialContent,
  onClose,
}: Props) {
  const { t } = useT("issues");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const insets = useSafeAreaInsets();
  const editComment = useEditComment(issueId);
  const [content, setContent] = useState(initialContent);

  const canSave = content.trim().length > 0 && !editComment.isPending;

  const onSave = useCallback(() => {
    if (!canSave) return;
    editComment.mutate(
      { commentId, content },
      {
        onSuccess: () => onClose(),
        onError: (err) =>
          Alert.alert(
            t("mobile.comment.edit_failed", "Couldn't save edit"),
            err instanceof Error ? err.message : String(err),
          ),
      },
    );
  }, [canSave, editComment, commentId, content, onClose, t]);

  return (
    <Modal transparent animationType="fade" onRequestClose={onClose}>
      <KeyboardAvoidingView
        behavior={Platform.OS === "ios" ? "padding" : undefined}
        className="flex-1"
      >
        <Pressable
          className="flex-1 bg-black/40"
          onPress={onClose}
          accessibilityLabel={t("common:cancel", "Cancel")}
        />
        <View
          className="rounded-t-2xl px-4 pt-3"
          style={{
            backgroundColor: theme.background,
            paddingBottom: insets.bottom + 12,
          }}
        >
          <View className="mb-2 flex-row items-center justify-between">
            <Pressable
              onPress={onClose}
              hitSlop={8}
              accessibilityRole="button"
              accessibilityLabel={t("common:cancel", "Cancel")}
            >
              <Text className="text-base text-muted-foreground">
                {t("common:cancel", "Cancel")}
              </Text>
            </Pressable>
            <Text className="text-sm font-semibold text-foreground">
              {t("mobile.comment.edit_title", "Edit comment")}
            </Text>
            <Pressable
              onPress={onSave}
              disabled={!canSave}
              hitSlop={8}
              accessibilityRole="button"
              accessibilityLabel={t("common:save", "Save")}
            >
              <Text
                className={
                  canSave
                    ? "text-base font-semibold text-primary"
                    : "text-base font-semibold text-muted-foreground opacity-50"
                }
              >
                {editComment.isPending
                  ? t("mobile.comment.edit_saving", "Saving…")
                  : t("common:save", "Save")}
              </Text>
            </Pressable>
          </View>
          <AutosizeTextArea
            value={content}
            onChangeText={setContent}
            minHeight={96}
            maxHeight={264}
            autoFocus
            textAlignVertical="top"
            className="bg-secondary/50 rounded-md px-3 py-2 text-base text-foreground"
          />
        </View>
      </KeyboardAvoidingView>
    </Modal>
  );
}
