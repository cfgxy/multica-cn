// Package lark contains the Multica ↔ 飞书 (Lark) Bot integration.
//
// MVP scope is tracked in MUL-2671. After the migration / service
// boundary PRs landed, this package now covers:
//
//  1. DB schema + sqlc wrappers (migration 109_lark_integration.up.sql)
//  2. InstallationService (encrypted app_secret, workspace-scoped lookups)
//  3. BindingTokenService (15-minute single-use, transactional redeem
//     that rejects cross-user rebinds in-DB)
//  4. ChatSessionService (channel-aware chat_session ensure / append
//     with /issue command parsing)
//  5. Dispatcher (inbound pipeline: installation route → group filter
//     → identity check → ensure session → append + dedup → /issue
//     → enqueue chat task; typed outcomes for offline / archived)
//  6. AuditLogger (lark_inbound_audit; deliberately no body column)
//  7. APIClient interface + stub (transport surface for outbound;
//     real Lark wire-protocol implementation lands in a follow-up
//     behind this same interface)
//  8. Hub (WS lease + per-installation supervisor goroutines with
//     exponential backoff + jitter; EventConnector interface is the
//     seam for the real wire protocol)
//  9. Patcher (subscribes to task / chat-done events; keeps the
//     per-task Lark interactive card in sync; throttled patches +
//     final/error bypass)
// 10. OAuthService (signed-state install URL + callback that exchanges
//     the code via APIClient and writes through InstallationService)
//
// Architectural boundaries (frozen from Elon's 二审, MUL-2671 §4.8):
//
//  1. Issue creation goes through internal/service.IssueService.Create —
//     this package never calls qtx.CreateIssue directly.
//  2. Inbound message ingestion uses ChatSessionService here, NOT the
//     HTTP `SendChatMessage` handler. Group chat_sessions have multi-
//     member creator semantics that the HTTP handler's single-creator
//     guard rejects on purpose.
//  3. Outbound card-message mapping lives in `lark_outbound_card_message`
//     (per task/message), never on `chat_session.metadata`.
//  4. Unbound users and non-workspace members never reach
//     chat_session/chat_message. They land in `lark_inbound_audit` (no
//     body) with a drop_reason and nothing else.
//  5. `app_secret` is encrypted at rest via internal/util/secretbox.
//     The DB never sees plaintext.
package lark
