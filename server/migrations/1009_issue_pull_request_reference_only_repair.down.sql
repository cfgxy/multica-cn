-- reference_only is part of the original issue_pull_request schema from
-- migration 127. Rolling back this repair must not remove the healthy column.
SELECT 1;
