DROP TABLE decision_requests;

ALTER TABLE issue_decisions DROP COLUMN executed_at;
ALTER TABLE issue_decisions DROP COLUMN execution_error;
ALTER TABLE issue_decisions DROP COLUMN execution_result;
ALTER TABLE issue_decisions DROP COLUMN pending_action_params;
ALTER TABLE issue_decisions DROP COLUMN pending_action_type;
ALTER TABLE issue_decisions DROP COLUMN pending_request_group_id;
ALTER TABLE issue_decisions DROP COLUMN auth_state;
ALTER TABLE issue_decisions DROP COLUMN answer_source;
ALTER TABLE issue_decisions DROP COLUMN expires_at;
ALTER TABLE issue_decisions DROP COLUMN deny_label;
ALTER TABLE issue_decisions DROP COLUMN approve_label;
ALTER TABLE issue_decisions DROP COLUMN named_approver_ids;
ALTER TABLE issue_decisions DROP COLUMN operator_tier;
ALTER TABLE issue_decisions DROP COLUMN visible_tier;
ALTER TABLE issue_decisions DROP COLUMN decision_kind;
