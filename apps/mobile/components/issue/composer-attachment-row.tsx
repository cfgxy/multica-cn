/**
 * Horizontal thumbnail row for the comment composer's pending attachments.
 *
 * Lives above the TextInput and below the (optional) reply-target chip.
 * Each item is a fixed-size card showing:
 *
 *   - Image (mime/^image\//) → expo-image preview off the LOCAL uri (file://
 *     from the picker), so the thumbnail renders instantly without waiting
 *     for the server URL. Even after upload completes we keep using
 *     localUri — it's already on disk, no network roundtrip.
 *   - Anything else → 📎 icon + truncated filename. Mime / size hints could
 *     be added later; for v1 the icon + name is enough signal.
 *
 * Status overlays (on top of the thumbnail):
 *
 *   - uploading → translucent spinner overlay across the whole card. The
 *     remove (×) button is hidden; user can't yank an upload mid-flight.
 *   - failed    → translucent destructive-tint overlay + small ⚠️ corner
 *                 badge. The card stays tappable for retry via the parent's
 *                 onRetry handler. Remove (×) is still shown so the user
 *                 can drop a wedged item.
 *   - completed → no overlay; just the × button in the corner.
 *
 * Why a separate component (not inlined): the composer file is already
 * dense (~400 LOC of textinput/keyboard/upload glue). Putting this here
 * keeps the composer focused on text + submit; this file only knows about
 * thumbnails. The data model is owned upstream (composer holds the array),
 * this is pure render + callback wiring.
 *
 * Placement rationale (above text, not below): aligns with iMessage / Slack
 * iOS / Lark / WhatsApp / Telegram — the attachment row is visual context
 * above the composing area. The toolbar below the text needs to stay flush
 * with the keyboard top, so inserting a row between text and toolbar would
 * conflict with KeyboardStickyView's bottom anchor.
 */
import { useMemo } from "react";
import { ActivityIndicator, Pressable, ScrollView, View } from "react-native";
import { Image as ExpoImage } from "expo-image";
import { Ionicons } from "@expo/vector-icons";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { Text } from "@/components/ui/text";

export type ComposerAttachmentStatus = "uploading" | "completed" | "failed";

export interface ComposerAttachmentItem {
  /** Stable local id assigned by the composer when the user picked. Used as
   *  the React key AND as the lookup id for status transitions. We don't use
   *  the server-returned `id` because it doesn't exist yet during upload. */
  localId: string;
  /** `file://...` from expo-image-picker / expo-document-picker. Stays the
   *  source of truth for thumbnail rendering even post-upload — it's on
   *  disk, free, and avoids fetching the server-side preview. */
  localUri: string;
  filename: string;
  mimeType: string;
  status: ComposerAttachmentStatus;
  /** Populated when status === "completed" — the server-side attachment id
   *  that the comment mutation will reference via `attachmentIds`. */
  id?: string;
  /** Populated when status === "completed" — the canonical `mc://file/<id>`
   *  URL the server returns. Currently unused at the composer level (we
   *  submit by id, not url), but kept on the item for future inline-insert
   *  affordances or debugging. */
  url?: string;
  /** Populated when status === "failed" — short human-readable error from
   *  the upload exception. */
  error?: string;
}

interface Props {
  items: ComposerAttachmentItem[];
  onRemove: (localId: string) => void;
  onRetry?: (localId: string) => void;
}

export function ComposerAttachmentRow({ items, onRemove, onRetry }: Props) {
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];

  if (items.length === 0) return null;

  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      contentContainerStyle={{ gap: 8, paddingHorizontal: 4 }}
      keyboardShouldPersistTaps="handled"
    >
      {items.map((item) => (
        <AttachmentCard
          key={item.localId}
          item={item}
          theme={theme}
          onRemove={onRemove}
          onRetry={onRetry}
        />
      ))}
    </ScrollView>
  );
}

interface CardProps {
  item: ComposerAttachmentItem;
  theme: typeof THEME["light"];
  onRemove: (localId: string) => void;
  onRetry?: (localId: string) => void;
}

function AttachmentCard({ item, theme, onRemove, onRetry }: CardProps) {
  const isImage = useMemo(
    () => item.mimeType.startsWith("image/"),
    [item.mimeType],
  );

  const handlePress = () => {
    // Tap on a failed card retries (if the parent wired onRetry). Other
    // statuses ignore tap — uploading is in-flight, completed has the ×
    // button as the only action.
    if (item.status === "failed" && onRetry) {
      onRetry(item.localId);
    }
  };

  return (
    <Pressable
      onPress={handlePress}
      accessibilityRole={item.status === "failed" ? "button" : "image"}
      accessibilityLabel={
        item.status === "failed"
          ? `Retry upload of ${item.filename}`
          : `Attachment ${item.filename}`
      }
      accessibilityHint={
        item.status === "failed"
          ? "Tap to retry the upload"
          : undefined
      }
      className="relative h-16 w-16 rounded-md overflow-hidden bg-secondary"
    >
      {isImage ? (
        <ExpoImage
          source={{ uri: item.localUri }}
          style={{ width: "100%", height: "100%" }}
          contentFit="cover"
          transition={120}
        />
      ) : (
        <View className="flex-1 items-center justify-center p-1">
          <Ionicons
            name="document-outline"
            size={22}
            color={theme.mutedForeground}
          />
          <Text
            className="text-[10px] text-muted-foreground mt-0.5"
            numberOfLines={1}
          >
            {item.filename}
          </Text>
        </View>
      )}

      {item.status === "uploading" ? (
        <View
          pointerEvents="none"
          className="absolute inset-0 items-center justify-center bg-black/40"
        >
          <ActivityIndicator color="#fff" />
        </View>
      ) : null}

      {item.status === "failed" ? (
        <View
          pointerEvents="none"
          className="absolute inset-0 items-center justify-center bg-destructive/30"
        >
          <Ionicons name="refresh" size={20} color="#fff" />
        </View>
      ) : null}

      {item.status !== "uploading" ? (
        <Pressable
          onPress={() => onRemove(item.localId)}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel={`Remove ${item.filename}`}
          className="absolute -top-1 -right-1 h-5 w-5 rounded-full bg-foreground items-center justify-center"
        >
          <Ionicons name="close" size={12} color={theme.background} />
        </Pressable>
      ) : null}
    </Pressable>
  );
}
