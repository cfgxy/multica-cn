"use client";

// The "has recommendation" pill (RUYI-547): one shape shared by the list row
// and the board card so a recommendation reads identically in both views.

export function DecisionRecommendedBadge({ label }: { label: string }) {
  return (
    <span
      data-testid="decision-inbox-recommended"
      className="shrink-0 rounded-full bg-brand/10 px-1.5 py-0.5 text-micro font-medium text-brand"
    >
      {label}
    </span>
  );
}
