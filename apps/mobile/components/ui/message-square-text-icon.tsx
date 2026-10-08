/**
 * MessageSquareText — react-native-svg implementation.
 *
 * Geometry mirrors lucide-react's MessageSquareText (packages/views renders it
 * from "lucide-react", e.g. the comment composer's quick-reply entry) so the
 * same control reads as the same glyph across desktop/web and mobile
 * (RUYI-550). Path data is copied verbatim from the installed lucide-react
 * v1.0.1 icon node; stroke presentation props match lucide's defaults
 * (2px round caps/joins, no fill).
 *
 * Code is mobile-owned — we adapt the SVG primitives with react-native-svg
 * instead of importing the web component (same posture as status-icon.tsx).
 */
import * as React from "react";
import Svg, { Path } from "react-native-svg";
import { useTheme } from "@react-navigation/native";

interface Props {
  /** Glyph size in points. Default 20 matches iOS toolbar icons. */
  size?: number;
  /** Override the stroke color. Defaults to the active theme's foreground. */
  color?: string;
}

export function MessageSquareTextIcon({ size = 20, color }: Props) {
  const { colors } = useTheme();
  return (
    <Svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke={color ?? colors.text}
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <Path d="M22 17a2 2 0 0 1-2 2H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.71.71 0 0 1 2 21.286V5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2z" />
      <Path d="M7 11h10" />
      <Path d="M7 15h6" />
      <Path d="M7 7h8" />
    </Svg>
  );
}
