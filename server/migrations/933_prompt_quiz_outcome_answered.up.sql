-- Narrow prompt_quiz_result.outcome to what the code actually measures
-- (RUYI-185 review, blocking item 2).
--
-- Migration 929 declared 'passed' / 'failed' as "the graded outcomes", but
-- nothing grades a quiz answer: the only writer maps a terminal
-- agent_task_queue status onto an outcome, so 'passed' meant "the run reached
-- status completed" and 'failed' had no writer at all. A dashboard reading
-- "passed 12 / 12" as a pass RATE would be reporting a number nobody computed.
--
-- The vocabulary is therefore narrowed to the distinction the mechanism can
-- actually make: did the run produce an answer, or did it produce nothing.
-- Grading an answer against a rubric is a separate judgement; when it lands it
-- gets its own column rather than reusing this one, because "the run finished"
-- and "the answer was correct" are independent facts and one value cannot
-- carry both.
--
-- 'passed' rows are rewritten rather than kept as a legal value: they were
-- written by OutcomeForStatus with exactly the meaning 'answered' now names, so
-- there is no old row whose meaning this rename changes.
ALTER TABLE prompt_quiz_result
    DROP CONSTRAINT IF EXISTS prompt_quiz_result_outcome_check;

UPDATE prompt_quiz_result SET outcome = 'answered' WHERE outcome = 'passed';

-- 'failed' had no writer, so there is nothing to migrate; the value is simply
-- gone from the vocabulary. Should a row exist in a database this project does
-- not know about, the constraint below rejects it and the operator sees why
-- rather than the value surviving as a silent third meaning.
ALTER TABLE prompt_quiz_result
    ADD CONSTRAINT prompt_quiz_result_outcome_check
    CHECK (outcome IN ('answered', 'errored'));

COMMENT ON COLUMN prompt_quiz_result.outcome IS
    'answered = the run produced an answer to the question; errored = it produced none (timeout, provider outage, cancellation). This is NOT a grade: no correctness judgement is made anywhere in RUYI-185, so neither value may be presented as a pass rate. Only answered rows with a measured run_tokens enter the distribution sample.';
