package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestComputerDaemonTokenLifecycle(t *testing.T) {
	for _, scenario := range []string{"missing", "valid", "expired", "revoked", "foreign", "delivery_failure", "reuse_delivery_failure", "cancelled_delivery"} {
		t.Run(scenario, func(t *testing.T) {
			_, binding := operationBinding(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			token := ""
			if scenario == "valid" || scenario == "expired" || scenario == "revoked" || scenario == "foreign" || scenario == "reuse_delivery_failure" {
				var err error
				token, err = auth.GeneratePATToken()
				if err != nil {
					t.Fatal(err)
				}
				owner := testUserID
				if scenario == "foreign" {
					owner = dbfx.User(t, "Other owner", binding+"@test.invalid")
				}
				expiry := time.Now().Add(time.Hour)
				if scenario == "expired" {
					expiry = time.Now().Add(-time.Hour)
				}
				dbfx.Insert(t, "personal_access_token", testutil.Cols{"user_id": owner, "name": "test supplied token", "token_hash": auth.HashToken(token), "token_prefix": token[:12], "expires_at": expiry, "revoked": scenario == "revoked"})
			}
			originalToken := token
			name := "Linux User alice (" + binding + ")"
			t.Cleanup(func() {
				_, _ = testPool.Exec(context.Background(), `DELETE FROM personal_access_token WHERE name=$1`, name)
			})
			var delivered string
			remote := computer.SSHRemote{Host: "fake", Port: 22, User: "operator", KeyPath: "fake", DaemonID: binding, WorkspaceID: testWorkspaceID,
				RunCmd: credentialTestRunner(func(argv []string, stdin string) (string, error) {
					if strings.Contains(strings.Join(argv, " "), "print(json.dumps(token))") {
						data, _ := json.Marshal(token)
						return string(data), nil
					}
					var config struct {
						Token       string `json:"token"`
						WorkspaceID string `json:"workspace_id"`
					}
					if err := json.Unmarshal([]byte(stdin), &config); err != nil {
						t.Fatal("invalid config payload")
					}
					if config.Token == "" || config.WorkspaceID != testWorkspaceID {
						t.Fatal("missing daemon configuration")
					}
					delivered = config.Token
					if strings.Contains(strings.Join(argv, " "), delivered) {
						t.Fatal("token in command arguments")
					}
					if scenario == "cancelled_delivery" {
						cancel()
						return "", context.Canceled
					}
					if scenario == "delivery_failure" || scenario == "reuse_delivery_failure" {
						return "", errors.New("write failed")
					}
					return "", nil
				}),
			}
			err := testHandler.ensureComputerDaemonCredentials(ctx, testUserID, computerBinding{ID: binding, Username: "alice"}, remote, "https://multica.invalid")
			failed := scenario == "delivery_failure" || scenario == "reuse_delivery_failure" || scenario == "cancelled_delivery"
			if (err != nil) != failed {
				t.Fatalf("unexpected completion: %v", err)
			}
			if delivered == "" {
				t.Fatal("token not delivered")
			}
			reused := scenario == "valid" || scenario == "reuse_delivery_failure"
			if (delivered == token) != reused {
				t.Fatal("incorrect reuse decision")
			}
			var owner string
			var revoked bool
			var expiry time.Time
			if err := testPool.QueryRow(context.Background(), `SELECT user_id::text,revoked,expires_at FROM personal_access_token WHERE token_hash=$1`, auth.HashToken(delivered)).Scan(&owner, &revoked, &expiry); err != nil {
				t.Fatal(err)
			}
			if owner != testUserID || revoked != (failed && !reused) {
				t.Fatal("incorrect token ownership or cleanup")
			}
			if !reused && (time.Until(expiry) < 89*24*time.Hour || time.Until(expiry) > PATRenewExtension) {
				t.Fatal("incorrect expiry")
			}
			if originalToken != "" {
				var originalRevoked bool
				if err := testPool.QueryRow(context.Background(), `SELECT revoked FROM personal_access_token WHERE token_hash=$1`, auth.HashToken(originalToken)).Scan(&originalRevoked); err != nil {
					t.Fatal(err)
				}
				if originalRevoked != (scenario == "revoked") {
					t.Fatal("changed a supplied token")
				}
			}
			// Retry after delivery/startup failure must reuse a valid delivered token.
			if !failed {
				token = delivered
				if err := testHandler.ensureComputerDaemonCredentials(ctx, testUserID, computerBinding{ID: binding, Username: "alice"}, remote, "https://multica.invalid"); err != nil {
					t.Fatal(err)
				}
				if delivered != token {
					t.Fatal("retry minted another token")
				}
			}
			var count int
			if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM personal_access_token WHERE name=$1`, name).Scan(&count); err != nil {
				t.Fatal(err)
			}
			expected := 1
			if reused {
				expected = 0
			}
			if count != expected {
				t.Fatalf("created %d tokens, want %d", count, expected)
			}
		})
	}
}

func TestComputerDaemonTokenReadFailureDoesNotIssue(t *testing.T) {
	_, binding := operationBinding(t)
	name := "Linux User alice (" + binding + ")"
	remote := computer.SSHRemote{Host: "fake", Port: 22, User: "operator", KeyPath: "fake", DaemonID: binding,
		RunCmd: credentialTestRunner(func([]string, string) (string, error) { return "", errors.New("unreadable configuration") }),
	}
	err := testHandler.ensureComputerDaemonCredentials(context.Background(), testUserID, computerBinding{ID: binding, Username: "alice"}, remote, "https://multica.invalid")
	if err == nil {
		t.Fatal("read failure ignored")
	}
	var id string
	err = testPool.QueryRow(context.Background(), `SELECT id::text FROM personal_access_token WHERE name=$1`, name).Scan(&id)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("issued a token after failed read")
	}
}
