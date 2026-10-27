"use client";

import { useRef, type ReactNode } from "react";
import { Paperclip } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";

interface FileUploadButtonProps {
  /** Called with the selected File — caller handles upload. */
  onSelect: (file: File) => void;
  disabled?: boolean;
  className?: string;
  size?: "sm" | "default";
  multiple?: boolean;
  /** HTML accept filter — e.g. "image/*" narrows the OS picker to images
   *  (RUYI-550 mobile-parity image entry). Unrestricted when omitted. */
  accept?: string;
  /** Overrides the default paperclip glyph (e.g. an image glyph on a
   *  dedicated image entry that shares the upload pipeline). */
  icon?: ReactNode;
  /** Overrides the default attach-file label for aria/title. */
  label?: string;
}

function FileUploadButton({
  onSelect,
  disabled,
  className,
  size = "default",
  multiple = false,
  accept,
  icon,
  label,
}: FileUploadButtonProps) {
  const { t } = useTranslation("ui");
  const inputRef = useRef<HTMLInputElement>(null);
  const attachLabel = label ?? t(($) => $.attach_file);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files ?? []);
    if (files.length === 0) return;
    e.target.value = "";
    for (const file of files) onSelect(file);
  };

  const iconSize = size === "sm" ? "h-3.5 w-3.5" : "h-4 w-4";
  const buttonSize = size === "sm" ? "icon-xs" : "icon-sm";

  return (
    <>
      <Button
        type="button"
        variant="ghost"
        size={buttonSize}
        onClick={() => inputRef.current?.click()}
        disabled={disabled}
        aria-label={attachLabel}
        title={attachLabel}
        className={cn("text-muted-foreground", className)}
      >
        {icon ?? <Paperclip className={iconSize} />}
      </Button>
      <input
        ref={inputRef}
        type="file"
        multiple={multiple}
        accept={accept}
        className="hidden"
        onChange={handleChange}
      />
    </>
  );
}

export { FileUploadButton, type FileUploadButtonProps };
