package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestAgentContextDualSlotMigrationRoundTrip exercises migration 930 in both
// directions inside a private schema. Up must add the voice slot, the
// capability / registration-source / credential-ref columns and the
// server-side credential store without touching a single pre-existing row
// (zero backfill: an agent bound through runtime_id stays exactly that, a
// text binding), widen the protocol_family whitelist with 'gemini_live', and
// give the voice slot the same ON DELETE RESTRICT foreign key the text slot
// has carried since migration 004. Down must return the schema to the exact
// pre-930 shape: voice-family rows removed (the only rows a pre-930 schema
// cannot represent), text-slot bindings and profiles untouched, whitelist
// narrowed back, credential store dropped.
func TestAgentContextDualSlotMigrationRoundTrip(t *testing.T) {
	adminPool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Uint32())
	schema := "migrate_agent_context_dual_slot_" + suffix
	schemaIdent := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schemaIdent); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := adminPool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+schemaIdent+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})

	pool := openTestPoolWithSearchPath(t, schema)

	// Minimal stand-ins for the production tables, carrying the shapes 930
	// and its down actually touch: the protocol_family CHECK keeps its
	// production constraint name (so both directions' DROP CONSTRAINT IF
	// EXISTS find it) with a short pre-930 whitelist, agent.runtime_id keeps
	// its RESTRICT foreign key from migration 004, and agent_runtime
	// carries the profile_id link column the down migration joins on.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE runtime_profile (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			display_name TEXT NOT NULL,
			protocol_family TEXT NOT NULL,
			command_name TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT runtime_profile_protocol_family_check
				CHECK (protocol_family IN ('claude', 'kimi', 'codex'))
		)
	`); err != nil {
		t.Fatalf("create runtime_profile fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE agent_runtime (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			profile_id UUID
		)
	`); err != nil {
		t.Fatalf("create agent_runtime fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE agent (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			workspace_id UUID NOT NULL,
			kind TEXT NOT NULL DEFAULT 'user',
			runtime_id UUID REFERENCES agent_runtime(id) ON DELETE RESTRICT
		)
	`); err != nil {
		t.Fatalf("create agent fixture: %v", err)
	}

	// The upgrading deployment's existing state: one text CLI profile, one
	// instance registered from it, one agent bound to it through runtime_id.
	// Their ids are captured so survival can be proven by identity, not
	// count — a round trip that deleted and recreated them would break every
	// agent_runtime and task row pointing at the instance.
	var textProfileID, textInstanceID, legacyAgentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO runtime_profile (display_name, protocol_family, command_name)
		VALUES ('Legacy Kimi CLI', 'kimi', 'kimi')
		RETURNING id::text
	`).Scan(&textProfileID); err != nil {
		t.Fatalf("insert text profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (profile_id) VALUES ($1::uuid)
		RETURNING id::text
	`, textProfileID).Scan(&textInstanceID); err != nil {
		t.Fatalf("insert text instance: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, kind, runtime_id)
		VALUES ('00000000-0000-0000-0000-000000000001', 'user', $1::uuid)
		RETURNING id::text
	`, textInstanceID).Scan(&legacyAgentID); err != nil {
		t.Fatalf("insert legacy agent: %v", err)
	}

	const version = "930_agent_context_dual_slot"
	lockKey := int64(rand.Uint64()&0x7fffffffffffffff) | 1
	run := func(direction string) error {
		return runMigrations(ctx, pool, runOptions{
			Direction:             direction,
			Files:                 realMigrationFiles(t, []string{version}, direction),
			SchemaMigrationsTable: schema + ".schema_migrations",
			AdvisoryLockKey:       lockKey,
			Hooks:                 hooksForDirection(direction),
		})
	}

	// Before 930 there is no voice slot and no place to store a gemini_live
	// profile.
	assertColumnExists(t, ctx, pool, "agent", "voice_runtime_id", false)
	assertFamilyRejected(t, ctx, pool, "gemini_live")

	if err := run("up"); err != nil {
		t.Fatalf("apply migration 930: %v", err)
	}
	assertMigrationLedger(t, ctx, pool, version, true)

	// Zero backfill: the legacy agent's new voice column is NULL and its
	// text binding is byte-for-byte what it was — every existing row is a
	// text binding and stays one (design §7.1), no data copied.
	var voiceIsNull bool
	var voiceRef, textRef *string
	if err := pool.QueryRow(ctx,
		`SELECT voice_runtime_id IS NULL, voice_runtime_id::text, runtime_id::text FROM agent WHERE id = $1::uuid`, legacyAgentID,
	).Scan(&voiceIsNull, &voiceRef, &textRef); err != nil {
		t.Fatalf("read legacy agent after up: %v", err)
	}
	if !voiceIsNull || voiceRef != nil {
		t.Errorf("legacy agent voice_runtime_id = %v after up, want NULL (zero backfill)", voiceRef)
	}
	if textRef == nil || *textRef != textInstanceID {
		t.Errorf("legacy agent runtime_id = %v after up, want %q — the text binding must survive untouched", textRef, textInstanceID)
	}

	// Pre-existing rows pick up the column defaults: '{}' resolves to the
	// family baseline via ResolveCapabilities, and every instance that
	// exists today was daemon-discovered.
	var caps, regSource string
	var credRef *string
	if err := pool.QueryRow(ctx, `
		SELECT rp.capabilities::text, ar.registration_source, ar.credential_ref
		FROM runtime_profile rp
		JOIN agent_runtime ar ON ar.profile_id = rp.id
		WHERE rp.id = $1::uuid
	`, textProfileID).Scan(&caps, &regSource, &credRef); err != nil {
		t.Fatalf("read pre-existing profile/instance defaults: %v", err)
	}
	if caps != "{}" {
		t.Errorf("pre-existing profile capabilities = %q, want '{}' (family-baseline default)", caps)
	}
	if regSource != "daemon_discovered" {
		t.Errorf("pre-existing instance registration_source = %q, want 'daemon_discovered'", regSource)
	}
	if credRef != nil {
		t.Errorf("pre-existing instance credential_ref = %q, want NULL", *credRef)
	}

	// The credential store is live: one row per (instance, key), composite
	// PK, upsert overwrites the encrypted payload in place. The payloads
	// are fake sealed bytes (raw-string hex literals — the shape Postgres
	// both accepts as bytea input and emits back as text), never real
	// ciphertext.
	const fakeSealedV1 = `\x0101`
	const fakeSealedV2 = `\x0102`
	if _, err := pool.Exec(ctx, `
		INSERT INTO runtime_credential (runtime_instance_id, credential_key, secret_encrypted)
		VALUES ($1::uuid, 'gemini_api_key', $2::bytea)
	`, textInstanceID, fakeSealedV1); err != nil {
		t.Fatalf("insert credential: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO runtime_credential (runtime_instance_id, credential_key, secret_encrypted)
		VALUES ($1::uuid, 'gemini_api_key', $2::bytea)
		ON CONFLICT (runtime_instance_id, credential_key)
		DO UPDATE SET secret_encrypted = EXCLUDED.secret_encrypted, updated_at = now()
	`, textInstanceID, fakeSealedV2); err != nil {
		t.Fatalf("upsert credential: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO runtime_credential (runtime_instance_id, credential_key, secret_encrypted)
		VALUES ($1::uuid, 'gateway_token', $2::bytea)
	`, textInstanceID, fakeSealedV1); err != nil {
		t.Fatalf("insert second credential key: %v", err)
	}
	var sealed string
	if err := pool.QueryRow(ctx, `
		SELECT secret_encrypted::text FROM runtime_credential
		WHERE runtime_instance_id = $1::uuid AND credential_key = 'gemini_api_key'
	`, textInstanceID).Scan(&sealed); err != nil {
		t.Fatalf("read upserted credential: %v", err)
	}
	if sealed != fakeSealedV2 {
		t.Errorf("credential payload after upsert = %s, want %s — the upsert must overwrite in place", sealed, fakeSealedV2)
	}

	// The whitelist widened by exactly one family: gemini_live in, the
	// pre-existing families untouched, and the CHECK did not degenerate
	// into "anything goes".
	assertFamilyAccepted(t, ctx, pool, "gemini_live")
	assertFamilyAccepted(t, ctx, pool, "kimi")
	assertFamilyRejected(t, ctx, pool, "gemini-live")

	// A voice deployment as the API creates it: gemini_live profile,
	// instance, credential, and an agent bound through the new voice slot.
	var voiceProfileID, voiceInstanceID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO runtime_profile (display_name, protocol_family, command_name)
		VALUES ('Gemini Live', 'gemini_live', '')
		RETURNING id::text
	`).Scan(&voiceProfileID); err != nil {
		t.Fatalf("insert gemini_live profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (profile_id) VALUES ($1::uuid)
		RETURNING id::text
	`, voiceProfileID).Scan(&voiceInstanceID); err != nil {
		t.Fatalf("insert voice instance: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE agent_runtime SET credential_ref = id::text || ':gemini_api_key' WHERE id = $1::uuid
	`, voiceInstanceID); err != nil {
		t.Fatalf("set credential_ref: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE agent SET voice_runtime_id = $1::uuid
		WHERE id = $2::uuid
	`, voiceInstanceID, legacyAgentID); err != nil {
		t.Fatalf("bind voice slot: %v", err)
	}

	// The voice slot carries the same ON DELETE RESTRICT foreign key as the
	// text slot (migration 004): the database itself refuses to delete a
	// referenced instance, making detach-first the application layer's job —
	// exactly the contract TeardownRuntime already implements for text.
	if _, err := pool.Exec(ctx,
		`DELETE FROM agent_runtime WHERE id = $1::uuid`, voiceInstanceID,
	); err == nil {
		t.Fatal("deleting a voice-referenced instance succeeded, want RESTRICT to reject it")
	} else if !strings.Contains(err.Error(), "agent_voice_runtime_id_fkey") {
		t.Fatalf("expected agent_voice_runtime_id_fkey to reject the delete, got: %v", err)
	}

	if err := run("down"); err != nil {
		t.Fatalf("roll back migration 930: %v", err)
	}
	assertMigrationLedger(t, ctx, pool, version, false)

	// The schema is back to the pre-930 shape: every added column and the
	// credential store are gone.
	assertColumnExists(t, ctx, pool, "agent", "voice_runtime_id", false)
	assertColumnExists(t, ctx, pool, "runtime_profile", "capabilities", false)
	assertColumnExists(t, ctx, pool, "agent_runtime", "registration_source", false)
	assertColumnExists(t, ctx, pool, "agent_runtime", "credential_ref", false)
	assertRelationExists(t, ctx, pool, schema+".runtime_credential", false)

	// Voice-family rows are gone — the only rows a pre-930 schema cannot
	// represent — and the whitelist is narrow again.
	var strandedVoice int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM runtime_profile WHERE protocol_family = 'gemini_live'`,
	).Scan(&strandedVoice); err != nil {
		t.Fatalf("count voice profiles after rollback: %v", err)
	}
	if strandedVoice != 0 {
		t.Errorf("gemini_live profiles left after rollback = %d, want 0 — a pre-930 daemon cannot interpret them", strandedVoice)
	}
	var voiceInstancesLeft int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM agent_runtime WHERE id = $1::uuid`, voiceInstanceID,
	).Scan(&voiceInstancesLeft); err != nil {
		t.Fatalf("count voice instances after rollback: %v", err)
	}
	if voiceInstancesLeft != 0 {
		t.Errorf("voice instance left after rollback = %d, want 0 (dangling profile_id)", voiceInstancesLeft)
	}
	assertFamilyRejected(t, ctx, pool, "gemini_live")
	assertFamilyAccepted(t, ctx, pool, "kimi")

	// The text world survives the round trip by identity: the legacy agent
	// keeps its binding, its instance, and its profile.
	var survivedText, survivedProfile string
	if err := pool.QueryRow(ctx, `
		SELECT a.runtime_id::text, rp.id::text
		FROM agent a
		JOIN agent_runtime ar ON a.runtime_id = ar.id
		JOIN runtime_profile rp ON ar.profile_id = rp.id
		WHERE a.id = $1::uuid
	`, legacyAgentID).Scan(&survivedText, &survivedProfile); err != nil {
		t.Fatalf("read legacy agent after rollback: %v", err)
	}
	if survivedText != textInstanceID || survivedProfile != textProfileID {
		t.Errorf("legacy binding after rollback = (instance %s, profile %s), want (instance %s, profile %s)",
			survivedText, survivedProfile, textInstanceID, textProfileID)
	}

	// Re-applying after a rollback must succeed: the down migration removed
	// everything the schema cannot re-derive, so a later upgrade path is
	// not poisoned by the round trip.
	if err := run("up"); err != nil {
		t.Fatalf("re-apply migration 930: %v", err)
	}
	assertMigrationLedger(t, ctx, pool, version, true)
	assertFamilyAccepted(t, ctx, pool, "gemini_live")
}
