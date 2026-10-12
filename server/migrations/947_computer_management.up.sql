-- RUYI-670: computer management domain schema (Linux execution-machine
-- provisioning, account bindings, durable operations, model-key directory).
-- Ported from trustbe-cn/multica @ a21d984ba75ae050f404e14e4c6a8bffcca62a9c
-- (Multica License = Apache-2.0 + Part I; original author yunfeng, trustbe.cn).
-- Renumbered from fork migrations 536,538-540,545,546,554-556 into one domain
-- stem per this repo's single-issue-single-number rule; the twelve CONCURRENTLY
-- index builds from fork 537,541-544,547-553 live in 948-959 (repo rule: one
-- concurrent build per migration file). No foreign keys, per repo rule.

-- fork 536_computer_provision: account-owned provisioning data. No workspace
-- admin can read another user's secrets.
CREATE TABLE computer (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL,
 host text NOT NULL, port integer NOT NULL CHECK(port BETWEEN 1 AND 65535),
 ssh_user text NOT NULL, enabled boolean NOT NULL DEFAULT true,
 created_by uuid NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE computer_credential (
 user_id uuid PRIMARY KEY, ciphertext bytea NOT NULL, updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE SEQUENCE computer_health_port_seq START 22000 MAXVALUE 60000 NO CYCLE;
CREATE TABLE computer_binding (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), computer_id uuid NOT NULL,
 user_id uuid NOT NULL, workspace_id uuid, username text NOT NULL,
 verified boolean NOT NULL DEFAULT false,
 health_port integer NOT NULL DEFAULT nextval('computer_health_port_seq'),
 state text NOT NULL DEFAULT 'pending', last_error text NOT NULL DEFAULT '',
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE computer_audit (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL,
 binding_id uuid, action text NOT NULL, outcome text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);

-- fork 538_computer_admin_audit
ALTER TABLE computer_audit ADD COLUMN computer_id uuid;

-- fork 539_computer_check: last connectivity check per computer, shown in the
-- instance admin list.
ALTER TABLE computer ADD COLUMN checked_at timestamptz;
ALTER TABLE computer ADD COLUMN check_ok boolean;
ALTER TABLE computer ADD COLUMN check_detail text NOT NULL DEFAULT '';

-- fork 540_computer_operations: durable metadata only — never persist
-- passwords, credential payloads or raw output.
CREATE TABLE computer_operation (
 id uuid NOT NULL DEFAULT gen_random_uuid(), binding_id uuid NOT NULL,
 computer_id uuid NOT NULL, username text NOT NULL, user_id uuid NOT NULL,
 kind text NOT NULL, runtime_id text NOT NULL DEFAULT '',
 requested_version text NOT NULL DEFAULT '', actual_version text NOT NULL DEFAULT '',
 state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','running','succeeded','failed','cancelled','interrupted')),
 step text NOT NULL DEFAULT 'queued', error_code text NOT NULL DEFAULT '', error_summary text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), started_at timestamptz, finished_at timestamptz,
 deadline_at timestamptz NOT NULL DEFAULT now()+interval '20 minutes'
);
CREATE TABLE computer_runtime_asset (
 binding_id uuid NOT NULL, runtime_id text NOT NULL, executable_path text NOT NULL DEFAULT '',
 install_dir text NOT NULL DEFAULT '', requested_version text NOT NULL DEFAULT '',
 actual_version text NOT NULL DEFAULT '', installer_source text NOT NULL DEFAULT '',
 installed_at timestamptz, checked_at timestamptz NOT NULL DEFAULT now(),
 probe_state text NOT NULL DEFAULT 'unknown', error_code text NOT NULL DEFAULT '',
 probe_environment text NOT NULL DEFAULT ''
);
ALTER TABLE computer_binding ADD COLUMN archived_at timestamptz;
ALTER TABLE computer_binding ADD COLUMN account_state text NOT NULL DEFAULT 'unknown';
ALTER TABLE computer_binding ADD COLUMN checked_at timestamptz;

-- fork 545_computer_daemon_observation
ALTER TABLE computer_binding ADD COLUMN daemon_state text NOT NULL DEFAULT 'unknown';
ALTER TABLE computer_binding ADD COLUMN daemon_checked_at timestamptz;

-- fork 546_model_key_configuration: central sub2api source, administrator-owned
-- key grants, and runtime config metadata. Secrets and remote file contents
-- stay outside these tables.
CREATE TABLE model_key_source (
    id smallint NOT NULL CHECK (id = 1),
    base_url text NOT NULL,
    directory_token_ciphertext bytea NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    status text NOT NULL DEFAULT 'unverified',
    last_checked_at timestamptz,
    last_error_code text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid NOT NULL
);
CREATE TABLE model_key_directory_entry (
    source_id smallint NOT NULL,
    upstream_id text NOT NULL,
    api_key_name text NOT NULL,
    status text NOT NULL,
    allowed_runtimes jsonb NOT NULL DEFAULT '[]'::jsonb,
    source_revision bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE user_model_key_grant (
    user_id uuid NOT NULL,
    source_id smallint NOT NULL,
    upstream_id text NOT NULL,
    position integer NOT NULL DEFAULT 0 CHECK (position >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE runtime_config_template (
    runtime_kind text NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    files jsonb NOT NULL DEFAULT '[]'::jsonb,
    supported_api_types jsonb NOT NULL DEFAULT '[]'::jsonb,
    default_model text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'draft',
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid NOT NULL
);
CREATE TABLE computer_runtime_model_config (
    binding_id uuid NOT NULL,
    runtime_kind text NOT NULL,
    source_id smallint,
    upstream_id text,
    model_id text NOT NULL DEFAULT '',
    template_revision bigint,
    file_revision text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'unconfigured',
    last_test_status text NOT NULL DEFAULT 'untested',
    last_test_error_code text NOT NULL DEFAULT '',
    last_tested_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- fork 554_runtime_model_configuration_metadata
ALTER TABLE user_model_key_grant ADD COLUMN IF NOT EXISTS api_key_name text NOT NULL DEFAULT '';
ALTER TABLE computer_runtime_model_config ADD COLUMN IF NOT EXISTS file_manifest jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE computer_runtime_model_config ADD COLUMN IF NOT EXISTS last_test_result jsonb NOT NULL DEFAULT '{}'::jsonb;

-- fork 555_model_key_source_retry_after
ALTER TABLE model_key_source ADD COLUMN IF NOT EXISTS retry_after timestamptz;

-- fork 556_runtime_model_save_operation
ALTER TABLE computer_runtime_model_config ADD COLUMN IF NOT EXISTS last_save_operation_id uuid;
