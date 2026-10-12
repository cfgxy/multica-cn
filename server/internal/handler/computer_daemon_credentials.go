package handler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/computer"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Called only after PAM authentication, under the Linux account operation lock.
func (h *Handler) ensureComputerDaemonCredentials(ctx context.Context, uid string, b computerBinding, remote computer.SSHRemote, serverURL string) error {
	token, err := remote.ReadDaemonToken(b.Username)
	if err != nil {
		return err
	}
	if token != "" {
		pat, err := h.Queries.GetPersonalAccessTokenByHash(ctx, auth.HashToken(token))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && uuidToString(pat.UserID) == uid {
			return remote.WriteDaemonConfig(b.Username, serverURL, token)
		}
	}
	token, err = auth.GeneratePATToken()
	if err != nil {
		return err
	}
	pat, err := h.Queries.CreatePersonalAccessToken(ctx, db.CreatePersonalAccessTokenParams{
		UserID: parseUUID(uid), Name: "Linux User " + b.Username + " (" + b.ID + ")",
		TokenHash: auth.HashToken(token), TokenPrefix: token[:12],
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(PATRenewExtension), Valid: true},
	})
	if err != nil {
		return err
	}
	if err := remote.WriteDaemonConfig(b.Username, serverURL, token); err != nil {
		// Revoke failed deliveries even if the SSH operation's context expired.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		hash, revokeErr := h.Queries.RevokePersonalAccessToken(cleanup, db.RevokePersonalAccessTokenParams{ID: pat.ID, UserID: pat.UserID})
		if revokeErr != nil {
			slog.Error("failed to revoke undelivered daemon token", "binding_id", b.ID, "token_id", uuidToString(pat.ID))
		} else if h.PATCache != nil {
			h.PATCache.Invalidate(cleanup, hash)
		}
		return err
	}
	// Retain the delivered token if daemon startup subsequently fails, so retry
	// can reuse it. Never revoke a caller-supplied token.
	return nil
}
