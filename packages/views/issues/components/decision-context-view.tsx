import type { ReactNode } from "react";
import type { DecisionContext } from "@multica/core/types";
import { useT } from "../../i18n";

export function DecisionContextView({ context }: { context?: DecisionContext | null }) {
  const { t } = useT("issues");
  if (!context) return null;

  const hasContent = Boolean(
    context.why_now || context.facts?.length || context.impact ||
    context.recommendation_reason || context.unknowns?.length,
  );
  if (!hasContent) return null;

  return (
    <section className="mt-3 space-y-2 border-l-2 border-brand/40 pl-3" data-testid="decision-context">
      <h3 className="text-caption font-semibold">{t(($) => $.decisions.context_title)}</h3>
      {context.why_now && <ContextField label={t(($) => $.decisions.context_why_now)}>{context.why_now}</ContextField>}
      {context.facts && context.facts.length > 0 && (
        <ContextField label={t(($) => $.decisions.context_facts)}>
          <ul className="list-disc space-y-1 pl-4">
            {context.facts.map((fact, index) => (
              <li key={`${fact.statement}-${index}`}>
                <span>{fact.statement}</span>
                {fact.evidence && fact.evidence.length > 0 && (
                  <ul className="mt-1 list-[circle] space-y-0.5 pl-4 text-caption text-muted-foreground">
                    {fact.evidence.map((evidence, evidenceIndex) => <li key={`${evidence}-${evidenceIndex}`}>{evidence}</li>)}
                  </ul>
                )}
              </li>
            ))}
          </ul>
        </ContextField>
      )}
      {context.impact && <ContextField label={t(($) => $.decisions.context_impact)}>{context.impact}</ContextField>}
      {context.recommendation_reason && <ContextField label={t(($) => $.decisions.context_recommendation)}>{context.recommendation_reason}</ContextField>}
      {context.unknowns && context.unknowns.length > 0 && (
        <ContextField label={t(($) => $.decisions.context_unknowns)}>
          <ul className="list-disc space-y-1 pl-4">{context.unknowns.map((item, index) => <li key={`${item}-${index}`}>{item}</li>)}</ul>
        </ContextField>
      )}
    </section>
  );
}

function ContextField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="text-caption">
      <p className="font-medium text-muted-foreground">{label}</p>
      <div className="mt-0.5 whitespace-pre-wrap break-words">{children}</div>
    </div>
  );
}
