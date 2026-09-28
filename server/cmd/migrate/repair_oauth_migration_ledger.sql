BEGIN;
SET LOCAL lock_timeout = '5s';
LOCK TABLE schema_migrations IN SHARE ROW EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM schema_migrations WHERE version = '929_oauth_client')
       AND to_regclass('oauth_client') IS NULL THEN
        RAISE EXCEPTION 'OAuth table is missing; repair schema drift before renumbering its ledger';
    END IF;
    IF EXISTS (SELECT 1 FROM schema_migrations WHERE version = '930_oauth_client_client_id_index')
       AND NOT EXISTS (
           SELECT 1 FROM pg_index
           WHERE indexrelid = to_regclass('idx_oauth_client_client_id') AND indisvalid
       ) THEN
        RAISE EXCEPTION 'OAuth index is missing or invalid; repair schema drift before renumbering its ledger';
    END IF;
END $$;

DELETE FROM schema_migrations AS old
USING (VALUES
    ('929_oauth_client', '939_oauth_client'),
    ('930_oauth_client_client_id_index', '940_oauth_client_client_id_index')
) AS mapping(old_version, new_version)
WHERE old.version = mapping.old_version
  AND EXISTS (SELECT 1 FROM schema_migrations WHERE version = mapping.new_version);

UPDATE schema_migrations AS old
SET version = mapping.new_version
FROM (VALUES
    ('929_oauth_client', '939_oauth_client'),
    ('930_oauth_client_client_id_index', '940_oauth_client_client_id_index')
) AS mapping(old_version, new_version)
WHERE old.version = mapping.old_version;
COMMIT;
