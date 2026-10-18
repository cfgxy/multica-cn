ALTER TABLE github_pull_request
    DROP COLUMN target_branch;

ALTER TABLE vcs_pull_request
    DROP COLUMN target_branch;
