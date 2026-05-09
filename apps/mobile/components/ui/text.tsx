import * as React from "react";
import { Text as RNText, type TextProps } from "react-native";
import { cn } from "@/lib/utils";

type Props = TextProps & { className?: string };

const Text = React.forwardRef<RNText, Props>(({ className, ...props }, ref) => {
  return (
    <RNText
      ref={ref}
      className={cn("text-base text-foreground", className)}
      {...props}
    />
  );
});
Text.displayName = "Text";

export { Text };
