-- The three indexes (919/920/921) are dropped by their own down files
-- before this one runs; DROP TABLE also removes any indexes still left.
DROP TABLE IF EXISTS prompt_version;
