-- Roll back the stage B system source: drop the automatic loop's own rows
-- first (re-adding NOT NULL would fail against them), then restore the
-- 939_scheduling_pause column shape. Human pause rows carry a member id and
-- survive; an in-flight suppression simply loses its anchor, as rolling back
-- the automatic loop itself demands. Per repo rule, down migrations run on
-- disposable databases only.

DELETE FROM scheduling_pause
WHERE created_by IS NULL AND reason = 'error_rate_suppression';

ALTER TABLE scheduling_pause
    ALTER COLUMN created_by SET NOT NULL;
