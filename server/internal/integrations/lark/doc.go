// Package lark contains the Multica ↔ 飞书 (Lark) Bot integration.
//
// MVP scope is tracked in MUL-2671. This first PR lands the migration
// surface (`109_lark_integration.up.sql`) and the package skeleton.
// Concrete OAuth, WebSocket hub, inbound dispatcher, and outbound card
// patcher land in follow-up PRs.
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
