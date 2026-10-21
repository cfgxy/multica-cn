import { useQuery } from "@tanstack/react-query";
import { router, useLocalSearchParams } from "expo-router";
import { useMemo } from "react";
import { ActivityIndicator, Linking, Pressable, ScrollView, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Ionicons } from "@expo/vector-icons";
import { api } from "@/data/api";
import { useT } from "@/lib/use-t";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { getMobileAttachmentPreviewKind } from "@/lib/attachment-preview";
import { openAttachmentDownload } from "@/lib/attachment-open";
import { resolveAttachmentUrl } from "@/lib/attachment-url";
import { Markdown } from "@/lib/markdown";
import { PdfDocument } from "@/lib/markdown/pdf-document";
import { Text } from "@/components/ui/text";

const MAX_MARKDOWN_BYTES = 2 * 1024 * 1024;
const MAX_PDF_BYTES = 20 * 1024 * 1024;

export default function AttachmentReaderScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const { t } = useT("editor");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const attachmentQuery = useQuery({
    queryKey: ["attachment-reader", id],
    queryFn: ({ signal }) => api.getAttachment(id, { signal }),
    enabled: !!id,
    retry: false,
  });
  const attachment = attachmentQuery.data;
  const kind = useMemo(
    () => getMobileAttachmentPreviewKind(attachment?.content_type ?? "", attachment?.filename ?? ""),
    [attachment?.content_type, attachment?.filename],
  );
  const markdownQuery = useQuery({
    queryKey: ["attachment-reader-content", id],
    queryFn: ({ signal }) => api.getAttachmentTextContent(id, { signal }),
    enabled: !!id && !!attachment && kind === "markdown" && attachment.size_bytes <= MAX_MARKDOWN_BYTES,
    retry: false,
  });

  const download = () => {
    if (!id || !attachment) return;
    void openAttachmentDownload(id, {
      source: api,
      opener: Linking,
      resolveUrl: resolveAttachmentUrl,
      fallbackUrl: attachment.download_url,
    });
  };

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["top", "bottom"]}>
      <View className="h-12 flex-row items-center gap-3 border-b border-border px-3">
        <Pressable onPress={() => router.back()} accessibilityRole="button" accessibilityLabel={t("close", "Close")} className="h-11 w-11 items-center justify-center">
          <Ionicons name="arrow-back" size={22} color={theme.foreground} />
        </Pressable>
        <View className="flex-1">
          <Text numberOfLines={1} className="text-sm font-semibold text-foreground">{attachment?.filename ?? t("preview_loading", "Loading preview")}</Text>
        </View>
        <Pressable onPress={download} disabled={!attachment} accessibilityRole="button" accessibilityLabel={t("download", "Download")} className="h-11 w-11 items-center justify-center">
          <Ionicons name="download-outline" size={21} color={theme.foreground} />
        </Pressable>
      </View>

      {attachmentQuery.isPending ? (
        <View className="flex-1 items-center justify-center"><ActivityIndicator color={theme.mutedForeground} /></View>
      ) : attachmentQuery.isError || !attachment ? (
        <ReaderMessage text={t("preview_error", "Preview couldn't be loaded. You can close and try again.")} />
      ) : kind === "markdown" ? (
        attachment.size_bytes > MAX_MARKDOWN_BYTES ? (
          <ReaderMessage text={t("preview_too_large", "File is too large to preview. Please download.")} />
        ) : markdownQuery.isPending ? (
          <View className="flex-1 items-center justify-center"><ActivityIndicator color={theme.mutedForeground} /></View>
        ) : markdownQuery.isError ? (
          <ReaderMessage text={t("preview_error", "Preview couldn't be loaded. You can close and try again.")} />
        ) : (
          <ScrollView className="flex-1" contentContainerClassName="px-4 py-4">
            {markdownQuery.data ? <Markdown content={markdownQuery.data} /> : <ReaderMessage text={t("preview_empty", "This file is empty.")} />}
          </ScrollView>
        )
      ) : kind === "pdf" ? (
        attachment.size_bytes > MAX_PDF_BYTES ? (
          <ReaderMessage text={t("preview_too_large", "File is too large to preview. Please download.")} />
        ) : (
          <PdfDocument url={attachment.attachment_download_url || attachment.download_url} />
        )
      ) : (
        <ReaderMessage text={t("preview_unsupported", "This file type can't be previewed.")} />
      )}
    </SafeAreaView>
  );
}

function ReaderMessage({ text }: { text: string }) {
  return <View className="flex-1 items-center justify-center px-6"><Text className="text-center text-sm text-muted-foreground">{text}</Text></View>;
}
