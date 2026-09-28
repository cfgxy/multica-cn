CREATE UNIQUE INDEX CONCURRENTLY idx_skill_version_identity
ON skill_version (skill_id, version);
