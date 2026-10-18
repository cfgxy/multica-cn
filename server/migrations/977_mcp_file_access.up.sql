-- RUYI-793: idempotent so an already-applied 977 can be safely re-run by the
-- controlled upgrade procedure after removing only this version from the ledger.
ALTER TABLE daemon_file_operation ADD COLUMN IF NOT EXISTS invocation_id UUID;
-- Bind new operations to the member authorized at submission time. Older
-- pending rows deliberately remain unclaimable because no actor can be
-- re-authorized after an ACL change.
ALTER TABLE daemon_file_operation ADD COLUMN IF NOT EXISTS requester_id UUID;

CREATE TABLE IF NOT EXISTS mcp_file_access_instance_binding (
    instance_id TEXT NOT NULL,
    workspace_id UUID NOT NULL,
    target_id TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    updated_by UUID NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS mcp_pre_auth_rejection (
    invocation_id UUID NOT NULL,
    instance_id TEXT NOT NULL,
    reason_code TEXT NOT NULL CHECK (reason_code IN ('unknown_tool', 'missing_workspace', 'invalid_workspace', 'unauthenticated_actor', 'entry_rejected')),
    file_tool_name TEXT CHECK (file_tool_name IS NULL OR file_tool_name IN ('list_files', 'get_file_metadata', 'read_file_chunk')),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS mcp_file_read_policy (
	 singleton BOOLEAN NOT NULL DEFAULT TRUE CHECK (singleton),
    version BIGINT NOT NULL CHECK (version > 0),
    rules JSONB NOT NULL,
    updated_by UUID,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO mcp_file_read_policy (singleton, version, rules)
SELECT TRUE, 1, '[
  {"id":"ssh","pattern":"**/.ssh/**","description":"SSH keys and configuration","enabled":true,"builtin":true},
  {"id":"gnupg","pattern":"**/.gnupg/**","description":"GPG keys and configuration","enabled":true,"builtin":true},
  {"id":"aws-credentials","pattern":"**/.aws/credentials","description":"AWS credentials","enabled":true,"builtin":true},
  {"id":"azure-credentials","pattern":"**/.azure/**","description":"Azure credentials","enabled":true,"builtin":true},
  {"id":"gcloud-credentials","pattern":"**/.config/gcloud/**","description":"Google Cloud credentials","enabled":true,"builtin":true},
  {"id":"kube-config","pattern":"**/.kube/config","description":"Kubernetes credentials","enabled":true,"builtin":true},
  {"id":"docker-config","pattern":"**/.docker/config.json","description":"Docker registry credentials","enabled":true,"builtin":true},
  {"id":"env-files","pattern":"**/.env*","description":"Environment configuration files","enabled":true,"builtin":true},
  {"id":"netrc","pattern":"**/.netrc","description":"Network login credentials","enabled":true,"builtin":true},
  {"id":"git-credentials","pattern":"**/.git-credentials","description":"Git credentials","enabled":true,"builtin":true},
  {"id":"credential-files","pattern":"**/*credential*","description":"Credential files","enabled":true,"builtin":true},
  {"id":"secret-files","pattern":"**/*secret*","description":"Secret files","enabled":true,"builtin":true},
  {"id":"token-files","pattern":"**/*token*","description":"Token files","enabled":true,"builtin":true},
  {"id":"npmrc","pattern":"**/.npmrc","description":"Package registry credentials","enabled":true,"builtin":true},
  {"id":"pypirc","pattern":"**/.pypirc","description":"Python package registry credentials","enabled":true,"builtin":true},
  {"id":"private-key-pem","pattern":"**/*.pem","description":"Private keys and certificate bundles","enabled":true,"builtin":true},
  {"id":"private-key-key","pattern":"**/*.key","description":"Private keys","enabled":true,"builtin":true},
  {"id":"ssh-private-key-names","pattern":"**/id_*","description":"SSH private keys","enabled":true,"builtin":true},
  {"id":"ai-client-auth","pattern":"**/.config/*/auth.json","description":"AI client authentication","enabled":true,"builtin":true}
]'::jsonb
WHERE NOT EXISTS (SELECT 1 FROM mcp_file_read_policy WHERE singleton = TRUE);
