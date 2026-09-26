-- Rolling back the gap backfill removes only the rows it wrote, identified by
-- the change note it stamps. 922's baselines carry a different note and stay,
-- as do every 'edit' and 'revert' version — those are real history.
DELETE FROM prompt_version
WHERE version = 1
  AND source = 'import'
  AND change_note = 'v1 基线：补齐缺失的创建时基线';
