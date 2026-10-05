ALTER TABLE live_session DROP COLUMN summary;
DROP INDEX idx_agent_fact_event_session;
DROP INDEX idx_agent_fact_event_agent_seq;
DROP TABLE agent_fact_event;
