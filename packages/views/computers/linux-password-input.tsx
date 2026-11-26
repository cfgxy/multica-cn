"use client";

import { useId, useState, type ComponentProps } from "react";
import { Eye, EyeOff } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../i18n";

export function LinuxPasswordInput({
  label,
  ...props
}: Omit<ComponentProps<typeof Input>, "type"> & { label?: string }) {
  const { t } = useT("settings");
  const inputId = useId();
  const id = props.id ?? inputId;
  const [visible, setVisible] = useState(false);
  const toggleLabel = visible
    ? t(($) => $.computers.hide_password)
    : t(($) => $.computers.show_password);

  return (
    <div className="space-y-1">
      <label htmlFor={id}>{label ?? t(($) => $.computers.password)}</label>
      <div className="relative">
        <Input
          {...props}
          id={id}
          type={visible ? "text" : "password"}
          className="pr-10"
        />
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="absolute right-0.5 top-1/2 -translate-y-1/2"
          disabled={props.disabled}
          aria-label={toggleLabel}
          title={toggleLabel}
          aria-pressed={visible}
          aria-controls={id}
          onClick={() => setVisible((value) => !value)}
        >
          {visible ? <EyeOff aria-hidden="true" /> : <Eye aria-hidden="true" />}
        </Button>
      </div>
    </div>
  );
}
