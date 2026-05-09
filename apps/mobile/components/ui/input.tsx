import * as React from "react";
import { TextInput, type TextInputProps } from "react-native";
import { cn } from "@/lib/utils";

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
        placeholderTextColor="#a1a1aa"
        {...props}
      />
    );
  },
);
Input.displayName = "Input";

export { Input };
