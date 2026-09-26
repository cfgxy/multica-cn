-- The dashboard's only read shape: one scope, all versions, last 30/90 days,
-- newest first. The unique constraint from 925 leads with (scope, scope_id,
-- version), which cannot serve a range on day without scanning every version
-- of the scope, so the range column comes second here.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_prompt_quality_daily_scope_day
    ON prompt_quality_daily (scope, scope_id, day DESC);
