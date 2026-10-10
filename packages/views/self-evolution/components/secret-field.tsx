"use client";

import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../../i18n";

/**
 * A write-only credential field (RUYI-551 §5.2 / walk #13): masked while
 * typed, never echoes a stored value — the server returns only
 * `has_api_key`, so there is nothing to render. Leaving it empty keeps the
 * existing stored key, which the placeholder states.
 */
export function SecretField({
  value,
  onChange,
  hasExisting,
  disabled,
  id,
}: {
  value: string;
  onChange: (next: string) => void;
  /** Whether a key is already stored (adjusts the placeholder copy). */
  hasExisting?: boolean;
  disabled?: boolean;
  id?: string;
}) {
  const { t } = useT("self-evolution");
  return (
    <Input
      id={id}
      type="password"
      value={value}
      disabled={disabled}
      autoComplete="off"
      onChange={(e) => onChange(e.target.value)}
      placeholder={
        hasExisting
          ? t(($) => $.secretField.keepExisting)
          : t(($) => $.secretField.placeholder)
      }
    />
  );
}
