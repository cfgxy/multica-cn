// Package dbbackup implements the server-side scheduled database dump
// backups: a periodic pg_dump -Fc run into a configured directory with
// rolling retention (RUYI-237).
//
// The job is deliberately self-contained and infrastructure-only: it shells
// out to pg_dump, names each archive by wall-clock timestamp, and deletes
// archives older than the retention window. Restore procedures live in the
// package README.
package dbbackup

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Environment knobs. All optional; every invalid value falls back to its
// default with a warning rather than blocking server startup.
const (
	EnabledEnv   = "MULTICA_BACKUP_ENABLED"
	DirEnv       = "MULTICA_BACKUP_DIR"
	IntervalEnv  = "MULTICA_BACKUP_INTERVAL"
	RetentionEnv = "MULTICA_BACKUP_RETENTION_DAYS"
	PGDumpEnv    = "MULTICA_BACKUP_PG_DUMP"
	TimeoutEnv   = "MULTICA_BACKUP_TIMEOUT"
)

const (
	// DefaultDir is relative to the server process working directory.
	DefaultDir = "backups"
	// DefaultInterval is the daily cadence.
	DefaultInterval = 24 * time.Hour
	// DefaultRetentionDays keeps one week of dumps, per RUYI-237.
	DefaultRetentionDays = 7
	// DefaultPGDumpPath resolves via PATH unless overridden.
	DefaultPGDumpPath = "pg_dump"
	// DefaultTimeout bounds a single dump so a stuck pg_dump cannot wedge
	// the schedule; the next round still fires on time.
	DefaultTimeout = 2 * time.Hour
)

// Config carries everything the backup job needs. DatabaseURL is the same
// DSN the server itself uses, handed in by main rather than re-read from
// the environment so the fallback resolution lives in one place.
type Config struct {
	Enabled       bool
	Dir           string
	Interval      time.Duration
	RetentionDays int
	PGDumpPath    string
	DatabaseURL   string
	Timeout       time.Duration
}

// ConfigFromEnv builds the backup configuration from environment lookups.
// getenv is injectable for tests. Invalid values log a warning and fall
// back to the defaults: a malformed backup knob must never keep the server
// from booting, but it must be visible in the log.
func ConfigFromEnv(getenv func(string) string, databaseURL string) Config {
	cfg := Config{
		Enabled:       true,
		Dir:           DefaultDir,
		Interval:      DefaultInterval,
		RetentionDays: DefaultRetentionDays,
		PGDumpPath:    DefaultPGDumpPath,
		DatabaseURL:   databaseURL,
		Timeout:       DefaultTimeout,
	}
	if raw := getenv(DirEnv); raw != "" {
		cfg.Dir = raw
	}
	if raw := getenv(PGDumpEnv); raw != "" {
		cfg.PGDumpPath = raw
	}
	cfg.Interval = envDuration(getenv, IntervalEnv, DefaultInterval)
	cfg.Timeout = envDuration(getenv, TimeoutEnv, DefaultTimeout)
	cfg.RetentionDays = envRetentionDays(getenv, RetentionEnv, DefaultRetentionDays)
	cfg.Enabled = envBool(getenv, EnabledEnv, true)
	return cfg
}

func envDuration(getenv func(string) string, name string, def time.Duration) time.Duration {
	raw := getenv(name)
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v <= 0 {
		slog.Warn("invalid env var, using default", "name", name, "value", raw, "default", def.String(), "error", err)
		return def
	}
	return v
}

func envRetentionDays(getenv func(string) string, name string, def int) int {
	raw := getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		slog.Warn("invalid env var, using default", "name", name, "value", raw, "default", def)
		return def
	}
	return v
}

func envBool(getenv func(string) string, name string, def bool) bool {
	raw := getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		slog.Warn("invalid env var, using default", "name", name, "value", raw, "default", def, "error", err)
		return def
	}
	return v
}

// ensureDir creates the backup directory with owner-only permissions;
// archives carry the full contents of the database.
func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0o700)
}
