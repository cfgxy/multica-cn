// Command sqlcgen supplies sqlc the same numeric migration order as the runner.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/migrations"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	files, err := migrations.Files("up")
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "multica-sqlc-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	schema := filepath.Join(tmp, "schema.sql")
	f, err := os.Create(schema)
	if err != nil {
		return err
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			_ = f.Close()
			return err
		}
		if _, err := fmt.Fprintf(f, "\n-- migration: %s\n%s\n", filepath.Base(path), data); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	queries, err := filepath.Rel(tmp, filepath.Join(root, "pkg/db/queries"))
	if err != nil {
		return err
	}
	output, err := filepath.Rel(tmp, filepath.Join(root, "pkg/db/generated"))
	if err != nil {
		return err
	}
	config := map[string]any{
		"version": "2",
		"sql": []any{map[string]any{
			"engine": "postgresql", "queries": queries, "schema": "schema.sql",
			"gen": map[string]any{"go": map[string]any{
				"package": "db", "out": output,
				"sql_package": "pgx/v5", "emit_json_tags": true, "emit_empty_slices": true,
			}},
		}},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	configPath := filepath.Join(tmp, "sqlc.json")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		return err
	}
	cmd := exec.Command("go", "run", "github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1", "generate", "-f", configPath)
	if installed, err := exec.LookPath("sqlc"); err == nil {
		if version, err := exec.Command(installed, "version").Output(); err == nil && strings.TrimSpace(string(version)) == "v1.31.1" {
			cmd = exec.Command(installed, "generate", "-f", configPath)
		}
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
