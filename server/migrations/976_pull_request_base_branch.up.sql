ALTER TABLE github_pull_request
    ADD COLUMN target_branch TEXT;

ALTER TABLE vcs_pull_request
    ADD COLUMN target_branch TEXT;
