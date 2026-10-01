-- Grading pass for the prompt quiz (RUYI-286): the score columns on the
-- measurement, and the structured half of the answer key on the item.
--
-- Migration 933's narrowing reserved this landing spot in advance: outcome was
-- narrowed to answered/errored because "the run finished" and "the answer was
-- correct" are independent facts, and its comment committed to giving grading
-- "its own column rather than reusing this one". These are those columns.
--
-- ON prompt_quiz_item:
--   tags / difficulty are bank presentation metadata and are member-visible —
--   they say what a question covers and how hard it is, which is exactly what
--   the bank list is for. Eight coverage categories are carried as tags
--   (discipline / conflict / boundary / tool / format / context / refusal /
--   regression), which is what makes the bank's type coverage mechanically
--   checkable instead of a claim in a comment.
--   rubric_checks is the STRUCTURED half of the answer key: machine-checkable
--   assertions evaluated against the run's answer at collection time. It is
--   private exactly like rubric (migration 935) — it names what a correct
--   answer must and must not contain, so a measured run that saw it would
--   answer from the key rather than from its prompt. The containment is the
--   same named-column discipline that already works for rubric: the member
--   list and the enqueue payload select columns by name, and the Go row types
--   those queries generate have no field for it, so the key cannot leak by a
--   column being added to the table.
--   The free-text rubric stays and keeps its meaning: checks grade, rubric
--   explains. An item with no checks is simply never scored — its results keep
--   score NULL, which reads as "not graded", never as 0.
--
-- ON prompt_quiz_result:
--   score is the weighted pass ratio over the item's rubric_checks at grading
--   time, 0..1. NULL = not graded: the item has no checks, the run left no
--   answer text, or the run errored. It is a separate fact from outcome by
--   construction — an errored run can have no score, and an answered run
--   against a check-less item has none either.
--   score_detail carries the per-assertion verdicts with their evidence: the
--   explanation a reader re-traces the grade from. The answer text itself is
--   deliberately NOT copied here — it stays in task_message, joined via
--   task_id, so the run's transcript remains the single source of truth for
--   what the agent actually said.
--   graded_at is when the grade was computed. measured_at is when the
--   measurement row was stored; both happen in the same collection tick today
--   but they are independent facts and a re-collection rewrites both.

ALTER TABLE prompt_quiz_item
    ADD COLUMN IF NOT EXISTS tags TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS difficulty TEXT NOT NULL DEFAULT 'medium'
        CHECK (difficulty IN ('easy', 'medium', 'hard')),
    ADD COLUMN IF NOT EXISTS rubric_checks JSONB
        CHECK (rubric_checks IS NULL OR jsonb_typeof(rubric_checks) = 'array');

ALTER TABLE prompt_quiz_result
    ADD COLUMN IF NOT EXISTS score REAL
        CHECK (score IS NULL OR (score >= 0 AND score <= 1)),
    ADD COLUMN IF NOT EXISTS score_detail JSONB,
    ADD COLUMN IF NOT EXISTS graded_at TIMESTAMPTZ;

COMMENT ON COLUMN prompt_quiz_item.tags IS
    'Bank presentation metadata, member-visible. One entry per coverage category (discipline / conflict / boundary / tool / format / context / refusal / regression), which is what makes type coverage mechanically checkable.';

COMMENT ON COLUMN prompt_quiz_item.difficulty IS
    'Bank presentation metadata, member-visible: easy / medium / hard.';

COMMENT ON COLUMN prompt_quiz_item.rubric_checks IS
    'Structured half of the answer key: machine-checkable assertions graded against the run answer at collection time. Private like rubric (migration 935): never sent to a measuring run, never returned by the member-visible list. NULL or [] = the item is not scored.';

COMMENT ON COLUMN prompt_quiz_result.score IS
    'Weighted pass ratio over the item''s rubric_checks, 0..1, computed at collection. NULL = not graded: no checks on the item, no answer text, or an errored run. Independent of outcome by design (migration 933).';

COMMENT ON COLUMN prompt_quiz_result.score_detail IS
    'Per-assertion verdicts with evidence — the explanation the grade can be re-traced from. Evidence names the assertion and the verdict, never quotes the answer; the answer stays in task_message (join via task_id).';

COMMENT ON COLUMN prompt_quiz_result.graded_at IS
    'When the grade was computed (collection tick). measured_at is when the measurement row was stored; grading happens in the same tick today but the two are independent facts.';
