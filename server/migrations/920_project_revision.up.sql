-- Project revision for optimistic locking (RUYI-354). Same contract as the
-- issue/comment revision columns (migration 351): BIGINT starting at 1,
-- incremented on every successful update so a client can pass the value it
-- read as expected_revision and have the write fail when the row changed.
ALTER TABLE project
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1;
