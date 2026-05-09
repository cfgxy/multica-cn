import * as React from "react";
import { Pressable, View, type PressableProps } from "react-native";
import { cva, type VariantProps } from "class-variance-authority";
import { Text } from "@/components/ui/text";
import { cn } from "@/lib/utils";

const buttonVariants = cva("items-center justify-center rounded-md", {
  variants: {
    variant: {
      default: "bg-primary active:opacity-80",
      secondary: "bg-secondary active:opacity-80",
      outline: "border border-border bg-background active:bg-secondary",
      brand: "bg-brand active:opacity-80",
    },
    size: {
      default: "h-12 px-6",
      sm: "h-10 px-4",
      lg: "h-14 px-8",
    },
  },
  defaultVariants: {
    variant: "default",
    size: "default",
  },
});

const buttonTextVariants = cva("text-sm font-medium", {
  variants: {
    variant: {
      default: "text-primary-foreground",
      secondary: "text-secondary-foreground",
      outline: "text-foreground",
      brand: "text-brand-foreground",
    },
  },
  defaultVariants: {
    variant: "default",
  },
});

interface ButtonProps
  extends PressableProps,
    VariantProps<typeof buttonVariants> {
  children: React.ReactNode;
  className?: string;
}

const Button = React.forwardRef<View, ButtonProps>(
  ({ className, variant, size, children, ...props }, ref) => {
    return (
      <Pressable
        ref={ref as React.Ref<View>}
        className={cn(buttonVariants({ variant, size }), className)}
        {...props}
      >
        <Text className={buttonTextVariants({ variant })}>{children}</Text>
      </Pressable>
    );
  },
);
Button.displayName = "Button";

export { Button, buttonVariants };
