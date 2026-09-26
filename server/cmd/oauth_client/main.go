// oauth_client registers, lists and removes the pre-registered OAuth clients
// the MCP authorization server authenticates at its token endpoint.
//
// The flow deliberately has no Dynamic Client Registration (RFC 7591) — see
// docs/adr/001-mcp-oauth-behind-nextjs-proxy.md §3.6 — so without this command
// the oauth_clients table has no writer and no client can ever be issued a
// code. An operator creates a row here and pastes the printed client_id and
// client_secret into the consumer's advanced OAuth settings.
//
// The secret is printed once and stored only as a SHA-256 hash, the same shape
// personal access tokens use. A lost secret is rotated by creating a new client
// and deleting the old one, never by reading the row back.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// randomCredential returns 64 hex chars of CSPRNG output, the same 32-byte
// strength the authorization codes use.
func randomCredential() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

const usage = `usage:
  oauth_client create --name <name> --redirect-uri <url> [--redirect-uri <url>...] [--created-by <user-uuid>]
  oauth_client list
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

// ValidateRedirectURIs rejects what the authorization endpoint could never
// match anyway. redirectURIAllowed compares registered entries byte for byte,
// so a relative or fragment-carrying entry is dead weight that only surfaces as
// an opaque "redirect_uri is not registered" at authorize time.
func ValidateRedirectURIs(uris []string) error {
	if len(uris) == 0 {
		return fmt.Errorf("at least one --redirect-uri is required")
	}
	for _, raw := range uris {
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("invalid redirect_uri %q: %w", raw, err)
		}
		if !u.IsAbs() {
			return fmt.Errorf("redirect_uri %q must be absolute", raw)
		}
		if u.Scheme != "https" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
			return fmt.Errorf("redirect_uri %q must use https (localhost may use http)", raw)
		}
		if u.Fragment != "" || strings.Contains(raw, "#") {
			return fmt.Errorf("redirect_uri %q must not carry a fragment", raw)
		}
	}
	return nil
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
	if err := ValidateRedirectURIs(redirectURIs); err != nil {
		return err
	}

	clientID, err := randomCredential()
	if err != nil {
		return fmt.Errorf("generate client_id: %w", err)
	}
	clientSecret, err := randomCredential()
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
	fmt.Println("\nThe secret is not recoverable. Store it now; rotate by creating a new client and deleting this one.")
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
		fmt.Printf("%s  %s  %s\n", c.ClientID, c.Name, strings.Join(c.RedirectUris, " "))
	}
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

	if err := db.New(pool).DeleteOAuthClient(ctx, strings.TrimSpace(*clientID)); err != nil {
		return fmt.Errorf("delete oauth client: %w", err)
	}
	fmt.Printf("deleted %s\n", strings.TrimSpace(*clientID))
	return nil
}
