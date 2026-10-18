// clausemigration-audit — the legacy-migration drift report CLI (ADR 007
// 拆单 5 / RUYI-764). It compares a clause-migration manifest (JSON, the
// clausemigration.Manifest schema) against one or more carrier text files
// and prints a JSON drift report: per-entry exact / drifted / missing /
// ambiguous, plus an overall verdict. Read-only over plain files — no
// database, no credentials; the production pass runs it against exported
// carrier text after Owner authorization.
//
// Usage:
//
//	clausemigration-audit -manifest pilot.json -carrier workspace=context.md [-carrier project=instructions.md ...]
//
// Exit code 0 when every entry is exact (drift-clean), 1 otherwise; a usage
// or manifest error exits 2.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/internal/clausemigration"
)

func main() {
	manifestPath := flag.String("manifest", "", "migration manifest JSON (clausemigration.Manifest schema)")
	carrierFlags := &multiFlag{usage: "carrier text file as scope=path, e.g. workspace=context.md (repeatable)"}
	flag.Var(carrierFlags, "carrier", carrierFlags.usage)
	flag.Parse()

	if *manifestPath == "" || len(carrierFlags.values) == 0 {
		fmt.Fprintln(os.Stderr, "usage: clausemigration-audit -manifest <file.json> -carrier <scope>=<file> [-carrier ...]")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		fail(2, "read manifest: %v", err)
	}
	manifest, err := clausemigration.LoadManifest(raw)
	if err != nil {
		fail(2, "%v", err)
	}
	carriers := map[string]string{}
	for _, kv := range carrierFlags.values {
		scope, path, found := strings.Cut(kv, "=")
		if !found || scope == "" || path == "" {
			fail(2, "invalid -carrier %q: want scope=path", kv)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fail(2, "read carrier %s: %v", kv, err)
		}
		carriers[scope] = string(data)
	}

	findings := clausemigration.CheckDrift(manifest, carriers)
	clean := clausemigration.DriftClean(findings)
	out, err := json.MarshalIndent(map[string]any{
		"manifest_source": manifest.Source,
		"drift_clean":     clean,
		"findings":        findings,
	}, "", "  ")
	if err != nil {
		fail(2, "encode report: %v", err)
	}
	fmt.Println(string(out))
	if !clean {
		os.Exit(1)
	}
}

func fail(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "clausemigration-audit: "+format+"\n", args...)
	os.Exit(code)
}

// multiFlag collects repeated string flags.
type multiFlag struct {
	name   string
	usage  string
	values []string
}

func (m multiFlag) String() string { return strings.Join(m.values, ", ") }
func (m multiFlag) Get() any       { return m.values }
func (m *multiFlag) Set(v string) error {
	m.values = append(m.values, v)
	return nil
}
