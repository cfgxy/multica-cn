-- Split a quiz item into a public half and a private half (RUYI-185 review, A2).
--
-- body is the PUBLIC half: it is the only free text a measuring run receives.
-- rubric is the PRIVATE half: the expected answer and the grading points. It
-- exists so a later grading pass has something to grade against, and it must
-- never reach the run being measured — an agent handed the answer key would
-- answer from it instead of from its prompt, and the reading would measure the
-- leak rather than the prompt.
--
-- WHY A COLUMN AND NOT A SEPARATE TABLE:
--   A rubric has no identity, no lifecycle and no cardinality of its own: it is
--   one text per item, edited with the item, deleted with the item. The
--   containment that matters is not physical storage, it is which query
--   selects it — the sweep's task payload is built from named columns and the
--   member-visible list response omits it, both asserted by test rather than by
--   convention.
--
-- Same 4000-byte ceiling as body, and the same application-layer isolation gate:
-- a rubric that names a production entity would carry the same contamination
-- into the grading side that the gate keeps out of the question side.
ALTER TABLE prompt_quiz_item
    ADD COLUMN IF NOT EXISTS rubric TEXT NOT NULL DEFAULT ''
        CHECK (length(rubric) <= 4000);

COMMENT ON COLUMN prompt_quiz_item.rubric IS
    'Private half of the item: expected answer and grading points. Never sent to a measuring run and never returned by the member-visible bank list. Empty string means no rubric has been written yet, which is why it is not NULL-able.';

COMMENT ON COLUMN prompt_quiz_item.body IS
    'Public half of the item: the question text, and the only free text a measuring run receives. Revision advances when this changes, because a reading taken against different wording is not comparable to one taken against the old wording. Editing the rubric does NOT advance revision: the private half is not part of what was measured.';
