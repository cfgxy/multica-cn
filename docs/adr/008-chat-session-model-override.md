# ADR-008: Chat Session Model Override

- Status: accepted
- Date: 2026-10-17

## Context

Chat sessions need an explicit model choice that survives refresh and opening the same session on another client. A client-only value loses that choice, while changing the Agent model changes unrelated sessions. Queued work also needs a stable model when a user changes the session setting later.

## Decision

Store a nullable `model_override` on `chat_session`. A null value follows the Agent's configured model. A non-empty value overrides that model for new messages in that session only. When a message is enqueued, snapshot the effective selection on its task; running and already queued tasks are not rewritten by later changes. Clearing the override restores the Agent model for future messages.

The server checks session access and invocation permission, and accepts only a model in the current runtime's available model catalog. Missing runtime/catalog, archived sessions, permission failures, or unavailable models fail without changing the saved selection. The API never changes Agent defaults, credentials, quotas, or other sessions.

## Consequences

- The session setting is durable and consistent across Web, desktop, and mobile.
- Each new task owns an immutable model selection snapshot.
- A session model change is rejected when the runtime has not reported a usable catalog; clients can retry after discovery.
- Clients must show the effective selection and offer the Agent default as the clear action.

## Verification

- Verify setting persistence across reload and clients, isolation between sessions, and unchanged Agent defaults.
- Verify task model snapshots survive later session changes and do not alter in-flight tasks.
- Verify permission, archived-session, missing-runtime/catalog, and unavailable-model failures preserve the prior selection.
