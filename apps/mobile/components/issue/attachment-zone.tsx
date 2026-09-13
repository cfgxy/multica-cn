/**
 * Domain attachment strip shared by smart create, manual create, and the
 * message composer. Images occupy a fixed thumbnail row; mentions and other
 * files share a capsule row below it. Both rows preserve join order and scroll
 * horizontally without wrapping.
 */
import { useMemo } from "react";
import {
  ActivityIndicator,
  Linking,
  Pressable,
  ScrollView,
  View,
} from "react-native";
import { Image as ExpoImage } from "expo-image";
import { Ionicons } from "@expo/vector-icons";
import { api } from "@/data/api";
import {
  groupAttachmentZoneItems,
  type AttachmentZoneItem,
} from "@/lib/attachment-zone";
import type { MentionMarker } from "@/lib/mention-serialize";
import { openAttachmentDownload } from "@/lib/attachment-open";
import { resolveAttachmentUrl } from "@/lib/attachment-url";
import { useLightbox } from "@/lib/markdown/lightbox-provider";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { cn } from "@/lib/utils";
import { Text } from "@/components/ui/text";

interface Props {
  mentions: MentionMarker[];
  attachments: AttachmentZoneItem[];
  onRemoveMention: (type: MentionMarker["type"], id: string) => void;
  onRemoveAttachment: (localId: string) => void;
  onRetryAttachment?: (localId: string) => void;
  className?: string;
}

const IMAGE_SIZE = 56;
const IMAGE_ROW_CONTENT = {
  gap: 8,
  paddingTop: 6,
  paddingRight: 8,
  paddingBottom: 2,
  paddingLeft: 2,
} as const;
const CHIP_ROW_CONTENT = {
  gap: 6,
  paddingHorizontal: 2,
  paddingVertical: 2,
} as const;

export function AttachmentZone({
  mentions,
  attachments,
  onRemoveMention,
  onRemoveAttachment,
  onRetryAttachment,
  className,
}: Props) {
  const grouped = useMemo(
    () => groupAttachmentZoneItems(attachments),
    [attachments],
  );
  const completedImageUris = useMemo(
    () =>
      grouped.images
        .filter((item) => item.status === "completed")
        .map((item) => item.localUri),
    [grouped.images],
  );

  if (mentions.length === 0 && attachments.length === 0) return null;

  return (
    <View className={cn("gap-2", className)}>
      {grouped.images.length > 0 ? (
        <ScrollView
          horizontal
          showsHorizontalScrollIndicator={false}
          contentContainerStyle={IMAGE_ROW_CONTENT}
          keyboardShouldPersistTaps="handled"
        >
          {grouped.images.map((item) => (
            <ImageThumb
              key={item.localId}
              item={item}
              completedImageUris={completedImageUris}
              onRemove={onRemoveAttachment}
              onRetry={onRetryAttachment}
            />
          ))}
        </ScrollView>
      ) : null}

      {mentions.length > 0 || grouped.files.length > 0 ? (
        <ScrollView
          horizontal
          showsHorizontalScrollIndicator={false}
          contentContainerStyle={CHIP_ROW_CONTENT}
          keyboardShouldPersistTaps="handled"
        >
          {mentions.map((mention) => (
            <MentionChipView
              key={`mention:${mention.type}:${mention.id}`}
              mention={mention}
              onRemove={onRemoveMention}
            />
          ))}
          {grouped.files.map((item) => (
            <FileChipView
              key={item.localId}
              item={item}
              onRemove={onRemoveAttachment}
              onRetry={onRetryAttachment}
            />
          ))}
        </ScrollView>
      ) : null}
    </View>
  );
}

function MentionChipView({
  mention,
  onRemove,
}: {
  mention: MentionMarker;
  onRemove: (type: MentionMarker["type"], id: string) => void;
}) {
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const iconName =
    mention.type === "all"
      ? "people"
      : mention.type === "issue"
        ? "git-branch-outline"
        : "person";
  const label = mention.type === "issue" ? mention.name : `@${mention.name}`;

  return (
    <View className="flex-row items-center gap-1 h-7 px-2 rounded-full bg-primary/10">
      <Ionicons name={iconName} size={12} color={theme.primary} />
      <Text
        className="text-xs font-medium text-foreground max-w-[120px]"
        numberOfLines={1}
      >
        {label}
      </Text>
      <Pressable
        onPress={() => onRemove(mention.type, mention.id)}
        hitSlop={8}
        accessibilityRole="button"
        accessibilityLabel={`Remove mention ${mention.name}`}
        className="h-4 w-4 items-center justify-center"
      >
        <Ionicons name="close" size={12} color={theme.mutedForeground} />
      </Pressable>
    </View>
  );
}

function ImageThumb({
  item,
  completedImageUris,
  onRemove,
  onRetry,
}: {
  item: AttachmentZoneItem;
  completedImageUris: string[];
  onRemove: (localId: string) => void;
  onRetry?: (localId: string) => void;
}) {
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const { open } = useLightbox();
  const failed = item.status === "failed";
  const uploading = item.status === "uploading";

  const onPress = () => {
    if (failed) {
      onRetry?.(item.localId);
      return;
    }
    if (!uploading) open(item.localUri, completedImageUris);
  };

  const accessibilityLabel = uploading
    ? `Image attachment ${item.filename}, uploading`
    : failed
      ? `Image attachment ${item.filename}, upload failed, tap to retry`
      : `Image attachment ${item.filename}, tap to preview`;

  return (
    <View style={{ width: IMAGE_SIZE, height: IMAGE_SIZE }}>
      <Pressable
        onPress={onPress}
        disabled={uploading}
        accessibilityRole={failed ? "button" : "image"}
        accessibilityLabel={accessibilityLabel}
        className={cn(
          "overflow-hidden border bg-muted active:opacity-80",
          failed ? "border-destructive/50" : "border-border",
        )}
        style={{
          width: IMAGE_SIZE,
          height: IMAGE_SIZE,
          borderRadius: 12,
          borderCurve: "continuous",
        }}
      >
        <ExpoImage
          source={{ uri: item.localUri }}
          contentFit="cover"
          style={{
            width: "100%",
            height: "100%",
            opacity: failed ? 0.4 : 1,
          }}
        />
        {uploading ? (
          <View className="absolute inset-0 items-center justify-center bg-background/60">
            <ActivityIndicator size="small" color={theme.foreground} />
          </View>
        ) : null}
        {failed ? (
          <View className="absolute inset-0 items-center justify-center bg-background/50">
            <View className="size-[22px] rounded-full items-center justify-center bg-background/80">
              <Ionicons name="refresh" size={13} color={theme.destructive} />
            </View>
          </View>
        ) : null}
      </Pressable>
      <Pressable
        onPress={() => onRemove(item.localId)}
        hitSlop={8}
        accessibilityRole="button"
        accessibilityLabel={`Remove ${item.filename}`}
        className="absolute -right-1.5 -top-1.5 size-[18px] rounded-full border border-border bg-secondary items-center justify-center"
      >
        <Ionicons name="close" size={10} color={theme.mutedForeground} />
      </Pressable>
    </View>
  );
}

function FileChipView({
  item,
  onRemove,
  onRetry,
}: {
  item: AttachmentZoneItem;
  onRemove: (localId: string) => void;
  onRetry?: (localId: string) => void;
}) {
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];

  const onPress = () => {
    if (item.status === "failed") {
      onRetry?.(item.localId);
      return;
    }
    if (item.status !== "completed" || !item.id) return;
    void openAttachmentDownload(item.id, {
      source: api,
      opener: Linking,
      resolveUrl: resolveAttachmentUrl,
      fallbackUrl: item.downloadUrl,
    });
  };

  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={
        item.status === "uploading"
          ? `${item.filename}, uploading`
          : item.status === "failed"
          ? `Retry upload of ${item.filename}`
          : `Open ${item.filename}`
      }
      className="flex-row items-center gap-1 h-7 px-2 rounded-full bg-secondary active:opacity-80"
    >
      {item.status === "uploading" ? (
        <ActivityIndicator size="small" color={theme.mutedForeground} />
      ) : (
        <Ionicons
          name={item.status === "failed" ? "refresh" : "document-outline"}
          size={12}
          color={
            item.status === "failed"
              ? theme.destructive
              : theme.mutedForeground
          }
        />
      )}
      <Text
        className="text-xs text-foreground max-w-[120px]"
        numberOfLines={1}
      >
        {item.filename}
      </Text>
      <Pressable
        onPress={() => onRemove(item.localId)}
        hitSlop={8}
        accessibilityRole="button"
        accessibilityLabel={`Remove ${item.filename}`}
        className="h-4 w-4 items-center justify-center"
      >
        <Ionicons name="close" size={12} color={theme.mutedForeground} />
      </Pressable>
    </Pressable>
  );
}
