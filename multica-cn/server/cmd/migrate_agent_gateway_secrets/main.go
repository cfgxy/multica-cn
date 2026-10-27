// migrate_agent_gateway_secrets performs the §7.3 Agent-level → Instance-level
// credential migration (RUYI-425). Historically, gateway-mode agents carried
// their bearer token INSIDE agent.runtime_config
// ({"mode":"gateway","gateway":{"host","port","token","tls"}}) — the exact
// shape design decision 3 retires: credentials belong to the runtime INSTANCE,
// sealed in the server-side runtime_credential store, with the agent row (now
// the instance row) holding nothing but the credential_ref pointer.
//
// For every agent whose runtime_config.gateway.token is non-empty the script:
//
//  1. seals the token into runtime_credential under the instance the agent is
//     bound to, keyed "gateway_token";
//  2. points agent_runtime.credential_ref at it ("<uuid>:gateway_token");
//  3. rewrites agent.runtime_config with gateway.token REMOVED and every
//     non-credential field (mode/host/port/tls) preserved. The GET mask
//     (maskGatewayToken) keeps working for any straggler row, but migrated
//     agents no longer carry the field at all.
//
// Without -apply the script is a DRY RUN: it prints exactly what would move,
// keyed by sha256 prefixes so operators can spot two agents on one instance
// holding DIFFERENT tokens (the one collision the (instance, key) store
// cannot represent — apply refuses those instead of silently keeping the
// last writer). Token values are never printed in either mode.
//
// Idempotent: rows without a token are skipped, so re-running after a
// partial apply finishes the remainder and a second full apply is a no-op.
// Requires migration 925 (runtime_credential table) to be applied, and — for
// -apply — MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY to be set; both failures
// exit non-zero without touching data.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

// gatewayCredentialKey is the credential_key half of the ref for migrated
// gateway tokens. Matches the handler's [a-z0-9_]{1,64} pattern.
const gatewayCredentialKey = "gateway_token"

type candidateRow struct {
	AgentID    string
	AgentName  string
	InstanceID string
	TokenHash  string // first 12 hex of sha256(token) — for diffing, never reversible enough to matter here
}

func main() {
	apply := flag.Bool("apply", false, "perform the migration (default: dry run)")
	flag.Parse()

	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		slog.Error("unable to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx,
		`SELECT 1 FROM runtime_credential LIMIT 0`); err != nil {
		slog.Error("runtime_credential table missing; apply migration 925 first", "error", err)
		os.Exit(1)
	}

	candidates, skipped, err := scanCandidates(ctx, pool)
	if err != nil {
		slog.Error("scan failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("agents with runtime_config.gateway.token: %d (unbound/skipped: %d)\n", len(candidates), skipped)
	for _, c := range candidates {
		fmt.Printf("  agent %s (%s) -> instance %s token sha256:%s\n", c.AgentID, c.AgentName, c.InstanceID, c.TokenHash)
	}
	if conflicts := detectConflicts(candidates); len(conflicts) > 0 {
		fmt.Println("CONFLICTS — one instance, multiple distinct tokens (not migratable as-is):")
		for _, c := range conflicts {
			fmt.Printf("  instance %s holds token variants %v\n", c.instance, c.hashes)
		}
		if *apply {
			slog.Error("refusing to apply: resolve the conflicts above by unifying each instance's token first")
			os.Exit(1)
		}
	}
	if !*apply {
		fmt.Println("dry run: no changes made. Re-run with -apply to migrate.")
		return
	}

	box, err := loadBox()
	if err != nil {
		slog.Error("secret box unavailable", "error", err)
		os.Exit(1)
	}

	migrated, err := applyMigration(ctx, pool, box, candidates)
	if err != nil {
		slog.Error("migration aborted", "error", err)
		os.Exit(1)
	}
	fmt.Printf("migrated %d agents; agent rows no longer carry gateway.token.\n", migrated)
}

// scanCandidates loads every agent carrying a non-empty gateway.token with a
// usable text-slot binding. The token itself stays in the database round
// trip: only its sha256 prefix ever reaches output.
func scanCandidates(ctx context.Context, pool *pgxpool.Pool) ([]candidateRow, int, error) {
	rows, err := pool.Query(ctx, `
		SELECT a.id::text, a.name, COALESCE(a.runtime_id::text, ''), a.runtime_config->'gateway'->>'token'
		FROM agent a
		WHERE a.kind = 'user'
		  AND a.runtime_id IS NOT NULL
		  AND COALESCE(a.runtime_config->'gateway'->>'token', '') <> ''
		ORDER BY a.id`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []candidateRow
	skipped := 0
	for rows.Next() {
		var id, name, instance, token string
		if err := rows.Scan(&id, &name, &instance, &token); err != nil {
			return nil, 0, err
		}
		if instance == "" {
			// The FK makes this unreachable today; kept so a future schema
			// relaxation degrades to a reported skip, not a crash.
			skipped++
			continue
		}
		sum := sha256.Sum256([]byte(token))
		out = append(out, candidateRow{
			AgentID:    id,
			AgentName:  name,
			InstanceID: instance,
			TokenHash:  hex.EncodeToString(sum[:])[:12],
		})
	}
	return out, skipped, rows.Err()
}

type instanceConflict struct {
	instance string
	hashes   []string
}

// detectConflicts groups candidates by instance and reports instances whose
// agents disagree on the token — the store keys on (instance, key), so two
// distinct tokens cannot both survive.
func detectConflicts(candidates []candidateRow) []instanceConflict {
	byInstance := map[string]map[string]struct{}{}
	for _, c := range candidates {
		if byInstance[c.InstanceID] == nil {
			byInstance[c.InstanceID] = map[string]struct{}{}
		}
		byInstance[c.InstanceID][c.TokenHash] = struct{}{}
	}
	var conflicts []instanceConflict
	for instance, hashes := range byInstance {
		if len(hashes) < 2 {
			continue
		}
		list := make([]string, 0, len(hashes))
		for h := range hashes {
			list = append(list, "sha256:"+h)
		}
		conflicts = append(conflicts, instanceConflict{instance: instance, hashes: list})
	}
	return conflicts
}

func loadBox() (*secretbox.Box, error) {
	key, err := secretbox.LoadKey("MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY")
	if err != nil {
		return nil, fmt.Errorf("MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY is required for -apply: %w", err)
	}
	return secretbox.New(key)
}

// applyMigration runs the whole move in ONE transaction: either every token
// lands in the store and leaves agent.runtime_config, or nothing changes.
func applyMigration(ctx context.Context, pool *pgxpool.Pool, box *secretbox.Box, candidates []candidateRow) (int, error) {
	if len(candidates) == 0 {
		return 0, nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	for _, c := range candidates {
		var token string
		if err := tx.QueryRow(ctx, `
			SELECT runtime_config->'gateway'->>'token' FROM agent WHERE id = $1
		`, c.AgentID).Scan(&token); err != nil {
			return 0, fmt.Errorf("reload agent %s: %w", c.AgentID, err)
		}
		if token == "" {
			continue // migrated by a concurrent run between scan and apply
		}
		sealed, err := box.Seal([]byte(token))
		if err != nil {
			return 0, fmt.Errorf("seal token for agent %s: %w", c.AgentID, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO runtime_credential (runtime_instance_id, credential_key, secret_encrypted)
			VALUES ($1, $2, $3)
			ON CONFLICT (runtime_instance_id, credential_key)
			DO UPDATE SET secret_encrypted = EXCLUDED.secret_encrypted, updated_at = now()
		`, c.InstanceID, gatewayCredentialKey, sealed); err != nil {
			return 0, fmt.Errorf("store credential for instance %s: %w", c.InstanceID, err)
		}
		ref := c.InstanceID + ":" + gatewayCredentialKey
		if _, err := tx.Exec(ctx, `
			UPDATE agent_runtime SET credential_ref = $2, updated_at = now() WHERE id = $1
		`, c.InstanceID, ref); err != nil {
			return 0, fmt.Errorf("point credential_ref for instance %s: %w", c.InstanceID, err)
		}
		if err := stripGatewayToken(ctx, tx, c.AgentID); err != nil {
			return 0, fmt.Errorf("strip gateway.token from agent %s: %w", c.AgentID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(candidates), nil
}

// stripGatewayToken removes gateway.token from the agent's runtime_config in
// Go, preserving every other field and key ordering. An empty gateway object
// is kept (the mode/host/port/tls siblings stay meaningful).
func stripGatewayToken(ctx context.Context, tx pgx.Tx, agentID string) error {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT runtime_config FROM agent WHERE id = $1 FOR UPDATE`, agentID).Scan(&raw); err != nil {
		return err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("unmarshal runtime_config: %w", err)
	}
	gw, ok := cfg["gateway"].(map[string]any)
	if !ok {
		return nil // nothing to strip; concurrent migration won the race
	}
	delete(gw, "token")
	updated, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE agent SET runtime_config = $2, updated_at = now() WHERE id = $1`, agentID, updated)
	return err
}
