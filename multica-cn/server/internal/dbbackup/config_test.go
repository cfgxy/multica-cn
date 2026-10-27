package dbbackup

import (
	"testing"
	"time"
)

func TestConfigFromEnvDefaults(t *testing.T) {
	cfg := ConfigFromEnv(func(string) string { return "" }, "postgres://user:pass@localhost:5432/multica")

	if !cfg.Enabled {
		t.Fatalf("Enabled = false, want true by default")
	}
	if cfg.Dir != DefaultDir {
		t.Fatalf("Dir = %q, want %q", cfg.Dir, DefaultDir)
	}
	if cfg.Interval != DefaultInterval {
		t.Fatalf("Interval = %s, want %s", cfg.Interval, DefaultInterval)
	}
	if cfg.RetentionDays != DefaultRetentionDays {
		t.Fatalf("RetentionDays = %d, want %d", cfg.RetentionDays, DefaultRetentionDays)
	}
	if cfg.PGDumpPath != DefaultPGDumpPath {
		t.Fatalf("PGDumpPath = %q, want %q", cfg.PGDumpPath, DefaultPGDumpPath)
	}
	if cfg.Timeout != DefaultTimeout {
		t.Fatalf("Timeout = %s, want %s", cfg.Timeout, DefaultTimeout)
	}
	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/multica" {
		t.Fatalf("DatabaseURL = %q, want the value passed by the caller", cfg.DatabaseURL)
	}
}

func TestConfigFromEnvOverrides(t *testing.T) {
	vals := map[string]string{
		EnabledEnv:         "false",
		DirEnv:             "/var/backups/multica",
		IntervalEnv:        "6h",
		RetentionEnv:       "3",
		PGDumpEnv:          "/usr/lib/postgresql/17/bin/pg_dump",
		TimeoutEnv:         "30m",
	}
	cfg := ConfigFromEnv(func(key string) string { return vals[key] }, "postgres://db")

	if cfg.Enabled {
		t.Fatalf("Enabled = true, want false")
	}
	if cfg.Dir != "/var/backups/multica" {
		t.Fatalf("Dir = %q, want /var/backups/multica", cfg.Dir)
	}
	if cfg.Interval != 6*time.Hour {
		t.Fatalf("Interval = %s, want 6h", cfg.Interval)
	}
	if cfg.RetentionDays != 3 {
		t.Fatalf("RetentionDays = %d, want 3", cfg.RetentionDays)
	}
	if cfg.PGDumpPath != "/usr/lib/postgresql/17/bin/pg_dump" {
		t.Fatalf("PGDumpPath = %q, want the overridden path", cfg.PGDumpPath)
	}
	if cfg.Timeout != 30*time.Minute {
		t.Fatalf("Timeout = %s, want 30m", cfg.Timeout)
	}
}

func TestConfigFromEnvInvalidValuesFallBackToDefaults(t *testing.T) {
	cases := []struct {
		name string
		vals map[string]string
	}{
		{"bad interval", map[string]string{IntervalEnv: "banana"}},
		{"zero interval", map[string]string{IntervalEnv: "0"}},
		{"negative interval", map[string]string{IntervalEnv: "-1h"}},
		{"zero retention", map[string]string{RetentionEnv: "0"}},
		{"negative retention", map[string]string{RetentionEnv: "-7"}},
		{"non-numeric retention", map[string]string{RetentionEnv: "one-week"}},
		{"bad timeout", map[string]string{TimeoutEnv: "soon"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ConfigFromEnv(func(key string) string { return tc.vals[key] }, "")

			if !cfg.Enabled {
				t.Fatalf("Enabled = false, an invalid unrelated value must not disable backups")
			}
			if cfg.Interval != DefaultInterval {
				t.Fatalf("Interval = %s, want default %s", cfg.Interval, DefaultInterval)
			}
			if cfg.RetentionDays != DefaultRetentionDays {
				t.Fatalf("RetentionDays = %d, want default %d", cfg.RetentionDays, DefaultRetentionDays)
			}
			if cfg.Timeout != DefaultTimeout {
				t.Fatalf("Timeout = %s, want default %s", cfg.Timeout, DefaultTimeout)
			}
		})
	}
}

func TestConfigFromEnvEnabledParsing(t *testing.T) {
	enabled := []string{"1", "true", "TRUE", "True", "t", "T"}
	for _, v := range enabled {
		cfg := ConfigFromEnv(envMap(EnabledEnv, v), "")
		if !cfg.Enabled {
			t.Fatalf("EnabledEnv=%q: Enabled = false, want true", v)
		}
	}
	disabled := []string{"0", "false", "FALSE", "False", "f", "F"}
	for _, v := range disabled {
		cfg := ConfigFromEnv(envMap(EnabledEnv, v), "")
		if cfg.Enabled {
			t.Fatalf("EnabledEnv=%q: Enabled = true, want false", v)
		}
	}
}

func envMap(key, value string) func(string) string {
	vals := map[string]string{key: value}
	return func(k string) string { return vals[k] }
}
