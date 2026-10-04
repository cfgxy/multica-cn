// oauth_client registers and manages the pre-registered OAuth clients the
// MCP authorization server authenticates at its token endpoint.
//
// The flow deliberately has no Dynamic Client Registration (RFC 7591) — see
// docs/adr/001-mcp-oauth-behind-nextjs-proxy.md §3.6 — so without this
// command (or the System Settings management UI, RUYI-420) the oauth_client
// table has no writer and no client can ever be issued a code. An operator
// creates a row here and pastes the printed client_id and client_secret
// into the consumer's advanced OAuth settings.
//
// The secret is printed once and stored only as a SHA-256 hash, the same
// shape personal access tokens use. Rotate replaces the hash in place: the
// old secret stops authenticating at the token endpoint immediately, while
// access tokens already issued stay valid until expiry — use disable for
// that.
//
// Cache-window semantics (RUYI-420): disable deletes the client's live
// grants, but this CLI has no Redis handle, so the grant-gate entries the
// auth middleware caches keep answering for at most the gate TTL
// (auth.AuthCacheTTL). The management UI path invalidates those entries
// on the same write; the CLI path converges within the TTL window.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/oauth"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const usage = `usage:
  oauth_client create --name <name> --redirect-uri <url> [--redirect-uri <url>...] [--created-by <user-uuid>]
  oauth_client list
  oauth_client update --client-id <id> [--name <name>] [--redirect-uri <url>...]
  oauth_client disable --client-id <id>
  oauth_client enable --client-id <id>
  oauth_client rotate --client-id <id>
  oauth_client delete --client-id <id>
`

type redirectURIList []string

func (l *redirectURIList) String() string { return strings.Join(*l, ",") }

func (l *redirectURIList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

func main() {
	logger.Init()
	if err := run(os.Args[1:]); err != nil {
		slog.Error("oauth client command failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing subcommand\n%s", usage)
	}
	ctx := context.Background()
	switch args[0] {
	case "create":
		return runCreate(ctx, args[1:])
	case "list":
		return runList(ctx, args[1:])
	case "update":
		return runUpdate(ctx, args[1:])
	case "disable":
		return runSetDisabled(ctx, args[1:], true)
	case "enable":
		return runSetDisabled(ctx, args[1:], false)
	case "rotate":
		return runRotate(ctx, args[1:])
	case "delete":
		return runDelete(ctx, args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q\n%s", args[0], usage)
	}
}

func connect(ctx context.Context) (*pgxpool.Pool, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// requireClientRow resolves the public client_id to its row; every verb
// below operates on the public id, the management UI on the row UUID.
func requireClientRow(ctx context.Context, q *db.Queries, clientID string) (db.OauthClient, error) {
	client, err := q.GetOAuthClientByClientID(ctx, strings.TrimSpace(clientID))
	if err != nil {
		return db.OauthClient{}, fmt.Errorf("client %q not found", strings.TrimSpace(clientID))
	}
	return client, nil
}

func runCreate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	name := fs.String("name", "", "human-readable client name")
	createdBy := fs.String("created-by", "", "user UUID recorded as the creator (optional)")
	var redirectURIs redirectURIList
	fs.Var(&redirectURIs, "redirect-uri", "allowed redirect_uri, repeatable; matched exactly at authorize time")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("--name is required")
	}
	if err := oauth.ValidateRedirectURIs(redirectURIs); err != nil {
		return err
	}

	clientID, err := oauth.NewClientSecret()
	if err != nil {
		return fmt.Errorf("generate client_id: %w", err)
	}
	clientSecret, err := oauth.NewClientSecret()
	if err != nil {
		return fmt.Errorf("generate client_secret: %w", err)
	}

	var creator pgtype.UUID
	if strings.TrimSpace(*createdBy) != "" {
		creator, err = util.ParseUUID(strings.TrimSpace(*createdBy))
		if err != nil {
			return err
		}
	}

	pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	queries := db.New(pool)
	client, err := queries.CreateOAuthClient(ctx, db.CreateOAuthClientParams{
		ClientID:         clientID,
		ClientSecretHash: auth.HashToken(clientSecret),
		Name:             strings.TrimSpace(*name),
		RedirectUris:     redirectURIs,
		CreatedBy:        creator,
	})
	if err != nil {
		return fmt.Errorf("create oauth client: %w", err)
	}

	// Printed to stdout, not logged: the secret exists in plaintext exactly
	// here and nowhere else, and slog output routinely ends up in collectors.
	fmt.Printf("client_id:     %s\n", client.ClientID)
	fmt.Printf("client_secret: %s\n", clientSecret)
	fmt.Printf("name:          %s\n", client.Name)
	fmt.Printf("redirect_uris: %s\n", strings.Join(client.RedirectUris, " "))
	fmt.Println("\nThe secret is not recoverable. Store it now; `oauth_client rotate` replaces it in place.")
	return nil
}

func runList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	clients, err := db.New(pool).ListOAuthClients(ctx)
	if err != nil {
		return fmt.Errorf("list oauth clients: %w", err)
	}
	if len(clients) == 0 {
		fmt.Println("no oauth clients registered")
		return nil
	}
	for _, c := range clients {
		state := "enabled"
		if c.DisabledAt.Valid {
			state = "disabled"
		}
		fmt.Printf("%s  %s  [%s]  %s\n", c.ClientID, c.Name, state, strings.Join(c.RedirectUris, " "))
	}
	return nil
}

func runUpdate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	clientID := fs.String("client-id", "", "client_id to update")
	name := fs.String("name", "", "new human-readable client name")
	var redirectURIs redirectURIList
	fs.Var(&redirectURIs, "redirect-uri", "replacement redirect_uri list, repeatable; omitted keeps the current list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*clientID) == "" {
		return fmt.Errorf("--client-id is required")
	}
	if strings.TrimSpace(*name) == "" && len(redirectURIs) == 0 {
		return fmt.Errorf("nothing to update: pass --name and/or --redirect-uri")
	}

	pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	queries := db.New(pool)

	client, err := requireClientRow(ctx, queries, *clientID)
	if err != nil {
		return err
	}
	newName := client.Name
	if strings.TrimSpace(*name) != "" {
		newName = strings.TrimSpace(*name)
	}
	// Partial semantics on a full-replace query: the merged list is
	// validated as a whole so a partial edit can never smuggle in a
	// redirect_uri the authorize endpoint would never match.
	newURIs := client.RedirectUris
	if len(redirectURIs) > 0 {
		newURIs = redirectURIs
	}
	if err := oauth.ValidateRedirectURIs(newURIs); err != nil {
		return err
	}

	updated, err := queries.UpdateOAuthClient(ctx, db.UpdateOAuthClientParams{
		ID:           client.ID,
		Name:         newName,
		RedirectUris: newURIs,
	})
	if err != nil {
		return fmt.Errorf("update oauth client: %w", err)
	}
	fmt.Printf("updated %s\n  name:          %s\n  redirect_uris: %s\n", updated.ClientID, updated.Name, strings.Join(updated.RedirectUris, " "))
	return nil
}

func runSetDisabled(ctx context.Context, args []string, disable bool) error {
	verb := "enable"
	if disable {
		verb = "disable"
	}
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	clientID := fs.String("client-id", "", "client_id to "+verb)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*clientID) == "" {
		return fmt.Errorf("--client-id is required")
	}
	pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	queries := db.New(pool)

	client, err := requireClientRow(ctx, queries, *clientID)
	if err != nil {
		return err
	}
	var at pgtype.Timestamptz
	if disable {
		at = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	if _, err := queries.SetOAuthClientDisabled(ctx, db.SetOAuthClientDisabledParams{
		ID:         client.ID,
		DisabledAt: at,
	}); err != nil {
		return fmt.Errorf("%s oauth client: %w", verb, err)
	}
	if !disable {
		fmt.Printf("enabled %s\n", client.ClientID)
		return nil
	}
	// Parity with the management UI: disabling kills the client's live
	// grants. The UI path also drops the auth middleware's gate-cache
	// entries; this CLI has no Redis handle, so in-flight tokens converge
	// to rejected within the gate TTL instead of immediately.
	revoked, err := queries.RevokeOAuthGrantsByClient(ctx, client.ClientID)
	if err != nil {
		return fmt.Errorf("revoke grants of %s: %w", client.ClientID, err)
	}
	fmt.Printf("disabled %s (%d live grant(s) revoked; cached gate entries expire within the TTL window)\n", client.ClientID, len(revoked))
	return nil
}

func runRotate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("rotate", flag.ContinueOnError)
	clientID := fs.String("client-id", "", "client_id whose secret rotates")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*clientID) == "" {
		return fmt.Errorf("--client-id is required")
	}
	pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	queries := db.New(pool)

	client, err := requireClientRow(ctx, queries, *clientID)
	if err != nil {
		return err
	}
	secret, err := oauth.NewClientSecret()
	if err != nil {
		return fmt.Errorf("generate client_secret: %w", err)
	}
	updated, err := queries.RotateOAuthClientSecret(ctx, db.RotateOAuthClientSecretParams{
		ID:               client.ID,
		ClientSecretHash: auth.HashToken(secret),
	})
	if err != nil {
		return fmt.Errorf("rotate oauth client secret: %w", err)
	}
	fmt.Printf("client_id:     %s\n", updated.ClientID)
	fmt.Printf("client_secret: %s\n", secret)
	fmt.Println("\nThe previous secret no longer authenticates at the token endpoint. Access tokens already issued remain valid until expiry or grant revocation.")
	return nil
}

func runDelete(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	clientID := fs.String("client-id", "", "client_id to remove")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*clientID) == "" {
		return fmt.Errorf("--client-id is required")
	}
	pool, err := connect(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	queries := db.New(pool)

	client, err := requireClientRow(ctx, queries, *clientID)
	if err != nil {
		return err
	}
	// Parity with the management UI: grants are revoked (not deleted)
	// before the client row goes, so the audit history and the users' "my
	// authorizations" records survive the client.
	if _, err := queries.RevokeOAuthGrantsByClient(ctx, client.ClientID); err != nil {
		return fmt.Errorf("revoke grants of %s: %w", client.ClientID, err)
	}
	if err := queries.DeleteOAuthClientByID(ctx, client.ID); err != nil {
		return fmt.Errorf("delete oauth client: %w", err)
	}
	fmt.Printf("deleted %s\n", client.ClientID)
	return nil
}
