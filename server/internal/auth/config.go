package auth

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultJWTExpiration is the default JWT token lifetime.
	// 30 days is reasonable for a productivity tool — users shouldn't need to
	// re-authenticate every few days.
	DefaultJWTExpiration = 30 * 24 * time.Hour // 720h
)

var (
	jwtExpiration     time.Duration
	jwtExpirationOnce sync.Once
)

// JWTExpiration returns the configured JWT token lifetime.
// Reads from JWT_EXPIRATION env var (Go duration string, e.g. "720h", "168h").
// Falls back to DefaultJWTExpiration (30 days).
func JWTExpiration() time.Duration {
	jwtExpirationOnce.Do(func() {
		jwtExpiration = DefaultJWTExpiration
		if v := strings.TrimSpace(os.Getenv("JWT_EXPIRATION")); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				slog.Error("invalid JWT_EXPIRATION, using default", "value", v, "error", err, "default", DefaultJWTExpiration)
				return
			}
			if d <= 0 {
				slog.Error("JWT_EXPIRATION must be positive, using default", "value", v, "default", DefaultJWTExpiration)
				return
			}
			jwtExpiration = d
			slog.Info(fmt.Sprintf("JWT expiration set to %s", d))
		}
	})
	return jwtExpiration
}
