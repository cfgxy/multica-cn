import * as React from "react";
import { TextInput, type TextInputProps } from "react-native";
import { cn } from "@/lib/utils";
import { MOBILE_PLACEHOLDER_COLOR } from "./input-tokens";

type Props = TextInputProps & { className?: string };

const Input = React.forwardRef<TextInput, Props>(
  ({ className, ...props }, ref) => {
    return (
      <TextInput
        ref={ref}
        className={cn(
          "h-12 rounded-md border border-border bg-background px-4 text-base text-foreground",
          className,
        )}
        placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
        {...props}
      />
    );
  },
);
Input.displayName = "Input";

export { Input };
