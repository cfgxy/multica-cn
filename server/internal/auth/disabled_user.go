package auth

import (
	"context"
	"errors"
)

// UserDisabledError is the wire-safe reason string returned to rejected
// clients. CLI and daemon clients match on it — change the wording only
// alongside every consumer.
const UserDisabledError = "account disabled"

// ErrUserDisabled is returned by the login path when the target user row is
// disabled (user.disabled_at set).
var ErrUserDisabled = errors.New(UserDisabledError)

// DisabledLookup reports whether a user account is disabled. It decouples
// the HTTP middlewares and the realtime WebSocket auth path from the DB
// layer: production wires a cache-first implementation over the persisted
// user.disabled_at column (see middleware.dbDisabledLookup); tests inject
// stubs. A nil implementation must be treated as "never disabled".
type DisabledLookup interface {
	IsDisabled(ctx context.Context, userID string) bool
}
