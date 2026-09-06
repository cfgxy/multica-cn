/**
 * Mermaid diagram — the React Native side of mobile's mermaid renderer
 * (RUYI-80).
 *
 * The actual drawing happens in `mermaid-diagram.dom.tsx` inside an Expo DOM
 * component WebView (the ADR's reserved path C); this host owns the RN
 * integration:
 *
 *   - resolves the light/dark theme tokens into mermaid theme variables
 *     (same four color roles web resolves from CSS custom properties);
 *   - renders the inline diagram scaled to the column (`matchContents` makes
 *     the WebView track the body height the SVG produces);
 *   - puts a transparent Pressable overlay on top of the inline WebView —
 *     a WebView consumes touches, so an underlay Pressable would never fire;
 *     inline diagrams aren't interactive under mermaid's strict mode, so
 *     nothing is lost — and opens the fullscreen viewer on tap, mirroring
 *     the web inline tap-to-open (`MermaidDiagram` in packages/views);
 *   - presents the fullscreen viewer as a plain RN `<Modal>`: this is a
 *     media lightbox like `react-native-image-viewing` (a Modal under the
 *     hood), not a formSheet — same mental model as the image lightbox,
 *     documented here as the intentional divergence point per mobile
 *     CLAUDE.md §Behavioral parity.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Modal, Pressable, View } from "react-native";
import * as Clipboard from "expo-clipboard";
import * as Haptics from "expo-haptics";
import { SafeAreaView } from "react-native-safe-area-context";
import Svg, { Path, Rect } from "react-native-svg";
import type { ComponentProps } from "react";
import type { DOMProps } from "expo/dom";
import { Text } from "@/components/ui/text";
import { THEME } from "@/lib/theme";
import { useColorScheme } from "@/lib/use-color-scheme";
import {
  buildMermaidThemeVariables,
  type MermaidThemeColors,
} from "./mermaid-config";
import MermaidDiagramDom from "./mermaid-diagram.dom";

/**
 * The babel client-reference proxy accepts everything the .dom component
 * declares plus the `dom` bag of WebView props; the imported module's own
 * type doesn't carry `dom`, so extend it here once.
 */
type DomDiagramProps = ComponentProps<typeof MermaidDiagramDom> & {
  dom?: DOMProps;
};

// matchContents reports the real body height after mermaid renders; this
// only keeps the pre-render frame from collapsing to zero.
const INLINE_MIN_HEIGHT = 120;

export function MermaidDiagram({ code }: { code: string }) {
  const { isDarkColorScheme } = useColorScheme();
  const theme = isDarkColorScheme ? THEME.dark : THEME.light;
  const [viewerOpen, setViewerOpen] = useState(false);
  const copyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [copied, setCopied] = useState(false);

  // THEME.light/dark are module constants, so this memo is stable per scheme.
  const colors = useMemo<MermaidThemeColors>(
    () => ({
      primaryColor: theme.muted,
      primaryBorderColor: theme.primary,
      primaryTextColor: theme.foreground,
      lineColor: theme.mutedForeground,
      fontFamily: "inherit",
    }),
    [theme],
  );

  useEffect(() => {
    return () => {
      if (copyTimerRef.current) clearTimeout(copyTimerRef.current);
    };
  }, []);

  const closeViewer = useCallback(() => setViewerOpen(false), []);

  const onCopySource = useCallback(async () => {
    try {
      await Clipboard.setStringAsync(code);
      void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
      setCopied(true);
      if (copyTimerRef.current) clearTimeout(copyTimerRef.current);
      copyTimerRef.current = setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard write failed (extremely rare on iOS). Silent — no
      // recovery path beats a confusing toast.
    }
  }, [code]);

  // Inline: track the rendered SVG's height (matchContents) and fill the
  // column so the DOM-side `max-width` scaling has a width to scale to.
  const inlineProps: DomDiagramProps = {
    chart: code,
    colors,
    mode: "inline",
    dom: {
      useExpoDOMWebView: true,
      matchContents: true,
      style: { width: "100%" },
    },
  };
  const fullProps: DomDiagramProps = {
    chart: code,
    colors,
    mode: "full",
    dom: { useExpoDOMWebView: true, style: { flex: 1 } },
  };

  return (
    <View className="gap-2">
      <View
        className="overflow-hidden rounded-lg border border-border bg-code-surface"
        style={{ minHeight: INLINE_MIN_HEIGHT }}
      >
        <MermaidDiagramDom {...inlineProps} />
        {/* Tap-to-open overlay: the WebView below swallows touches, so the
            gesture must live above it. */}
        <Pressable
          className="absolute inset-0"
          accessibilityRole="button"
          accessibilityLabel="Open diagram full screen"
          onPress={() => setViewerOpen(true)}
        />
      </View>

      <Modal
        visible={viewerOpen}
        animationType="fade"
        onRequestClose={closeViewer}
      >
        <SafeAreaView className="flex-1 bg-background">
          <View className="flex-row items-center justify-between px-4 py-2">
            <Pressable
              onPress={closeViewer}
              hitSlop={8}
              accessibilityRole="button"
              accessibilityLabel="Close diagram viewer"
            >
              <CloseIcon color={theme.foreground} />
            </Pressable>
            <Text className="text-sm text-muted-foreground">mermaid</Text>
            <Pressable
              onPress={onCopySource}
              hitSlop={8}
              accessibilityRole="button"
              accessibilityLabel={
                copied ? "Diagram source copied" : "Copy diagram source"
              }
            >
              {copied ? (
                <CheckIcon color={theme.success} />
              ) : (
                <CopyIcon color={theme.mutedForeground} />
              )}
            </Pressable>
          </View>
          <View className="flex-1">
            <MermaidDiagramDom {...fullProps} />
          </View>
        </SafeAreaView>
      </Modal>
    </View>
  );
}

// Inline SVG icons follow the code-block.tsx pattern: react-native-svg
// primitives take colors as props from THEME[scheme] (no NativeWind).

function CloseIcon({ color }: { color: string }) {
  return (
    <Svg width={20} height={20} viewBox="0 0 16 16" fill="none">
      <Path
        d="M4 4l8 8M12 4l-8 8"
        stroke={color}
        strokeWidth={1.6}
        strokeLinecap="round"
      />
    </Svg>
  );
}

function CopyIcon({ color }: { color: string }) {
  return (
    <Svg width={18} height={18} viewBox="0 0 16 16" fill="none">
      <Rect
        x={5}
        y={5}
        width={9}
        height={9}
        rx={1.5}
        stroke={color}
        strokeWidth={1.4}
      />
      <Path
        d="M11 4.5V3.5A1.5 1.5 0 0 0 9.5 2H3.5A1.5 1.5 0 0 0 2 3.5v6A1.5 1.5 0 0 0 3.5 11h1"
        stroke={color}
        strokeWidth={1.4}
        strokeLinecap="round"
      />
    </Svg>
  );
}

function CheckIcon({ color }: { color: string }) {
  return (
    <Svg width={18} height={18} viewBox="0 0 16 16" fill="none">
      <Path
        d="M3.5 8.5L6.5 11.5L12.5 5"
        stroke={color}
        strokeWidth={1.6}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </Svg>
  );
}
