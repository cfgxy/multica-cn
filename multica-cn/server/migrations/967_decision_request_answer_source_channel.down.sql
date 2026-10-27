-- Fold decision_center rows back to card_click first: the pre-967 code wrote
-- card_click on the row answer endpoint, so the fold restores exactly what
-- the old writer produced and lets the narrowed CHECK apply even on a
-- database that already saw decision_center values.
UPDATE decision_requests SET answer_source = 'card_click' WHERE answer_source = 'decision_center';

ALTER TABLE decision_requests DROP CONSTRAINT decision_requests_answer_source_check;
ALTER TABLE decision_requests ADD CONSTRAINT decision_requests_answer_source_check
    CHECK (answer_source IN ('card_click', 'text_token', 'batch'));
