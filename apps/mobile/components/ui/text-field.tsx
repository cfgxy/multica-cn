/**
 * Single-line text input. Notes:
 * - `fontSize` is set inline; do NOT use Tailwind `text-*` here. NativeWind
 *   maps those to fontSize+lineHeight, and `lineHeight` on iOS TextInput
 *   clips descenders (RN issues #41240, #28012, #45268, #49886).
 * - Height anchored by `h-10` so vertical centering doesn't depend on
 *   font metrics.
 * - `includeFontPadding` / `textAlignVertical` are Android-only; iOS no-op,
 *   kept for when Android lands.
 */
import { TextInput, type TextInputProps } from "react-native";
import { cn } from "@/lib/utils";
import { MOBILE_PLACEHOLDER_COLOR } from "./input-tokens";

export interface TextFieldProps extends TextInputProps {
  className?: string;
}

export function TextField({ className, style, ...rest }: TextFieldProps) {
  return (
    <TextInput
      placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
      style={[
        { fontSize: 14, includeFontPadding: false, textAlignVertical: "center" },
        style,
      ]}
      className={cn(
        "bg-secondary/50 rounded-md px-3 h-10 text-foreground",
        className,
      )}
      {...rest}
    />
  );
}
