import { cn } from "@multica/ui/lib/utils";

export function LinuxUserStatus({
  state,
  label,
}: {
  state: string;
  label: string;
}) {
  const positive = ["ready", "present", "online", "succeeded"].includes(state);
  const negative = ["failed", "interrupted", "missing", "unavailable"].includes(
    state,
  );
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-caption font-medium whitespace-nowrap",
        positive
          ? "border-success/20 bg-success/10 text-success"
          : negative
            ? "border-destructive/20 bg-destructive/10 text-destructive"
            : "border-border bg-muted/40 text-foreground",
      )}
    >
      <span
        aria-hidden="true"
        className="size-1.5 shrink-0 rounded-full bg-current"
      />
      {label}
    </span>
  );
}

export function LinuxUserTime({ value }: { value?: string | null }) {
  if (!value || !Number.isFinite(new Date(value).getTime()))
    return <span>—</span>;
  return (
    <time
      dateTime={value}
      className="text-caption tabular-nums whitespace-nowrap"
    >
      {new Date(value).toLocaleString()}
    </time>
  );
}
