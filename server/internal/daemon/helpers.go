package daemon

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func durationFromEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	d, err := parseFlexDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q: %w", key, value, err)
	}
	return d, nil
}

// dayUnit matches an integer followed by `d` (days), optionally mixed with
// other Go duration components, e.g. "5d", "1d12h", "2d30m".
var dayUnit = regexp.MustCompile(`(\d+)d`)

// parseFlexDuration accepts the standard Go time.ParseDuration syntax plus a
// `d` (day) suffix, which the stdlib rejects. "5d" → 120h, "1d12h" → 36h.
func parseFlexDuration(value string) (time.Duration, error) {
	expanded := dayUnit.ReplaceAllStringFunc(value, func(match string) string {
		n, _ := strconv.Atoi(match[:len(match)-1])
		return fmt.Sprintf("%dh", n*24)
	})
	return time.ParseDuration(expanded)
}

func intFromEnv(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q: %w", key, value, err)
	}
	return n, nil
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
