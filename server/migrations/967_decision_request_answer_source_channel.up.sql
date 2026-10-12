-- RUYI-630: the row answer endpoint (POST /workspaces/{id}/decision-requests/
-- {requestId}/answer — the decision-center row buttons and the bare API share
-- it) is its own answer channel. Its answers are neither card clicks nor
-- question-card surface answers, so the server now labels its rows and
-- approval audits decision_center instead of pretending a card click happened
-- (the endpoint had hardcoded card_click since 940).
--
-- Widens only decision_requests.answer_source to the four-channel enum; the
-- card surface (issue_decisions.answer_source) keeps its three channels —
-- cards never produce decision_center. No data backfill: the feature has
-- never shipped beyond dev/test databases (PR unmerged).
ALTER TABLE decision_requests DROP CONSTRAINT decision_requests_answer_source_check;
ALTER TABLE decision_requests ADD CONSTRAINT decision_requests_answer_source_check
    CHECK (answer_source IN ('card_click', 'text_token', 'batch', 'decision_center'));
