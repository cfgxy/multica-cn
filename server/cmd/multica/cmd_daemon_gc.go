package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/spf13/cobra"
)

// daemon gc (RUYI-594): the scan-only window onto the recycle guard. The
// daemon's periodic GC is the only thing that ever deletes, and it refuses
// L2 recycles itself; this command exists so an operator can ask "what would
// the guard say right now" and get the same evidence files the real recycles
// write, without a delete path anywhere in the call graph.
var daemonGcCmd = &cobra.Command{
	Use:   "gc --dry-run",
	Short: "Scan recycle candidates for unpushed work (dry-run only, deletes nothing)",
	Long: "Runs the recycle guard's scan over every task directory and bare repo cache under the\n" +
		"workspaces root and writes the same evidence files the daemon's recycle paths write.\n" +
		"Nothing is ever deleted: --dry-run is the only mode, and verdicts report what the guard\n" +
		"would do (pass / evidence / blocked), not what will happen now — a live daemon skips\n" +
		"active task directories this command cannot see.\n\n" +
		"Evidence lands in <workspaces-root>/.recycle-evidence/ and outlives the directories it\n" +
		"describes; the guard's sweeper (MULTICA_GC_GUARD_EVIDENCE_TTL, default 30d) is the only\n" +
		"deleter. Set MULTICA_GC_GUARD_ENABLED=false to restore pre-guard behaviour; the command\n" +
		"then scans nothing and says so.",
	RunE: runDaemonGcDryRun,
}

func init() {
	gf := daemonGcCmd.Flags()
	gf.Bool("dry-run", false, "Scan only — the only mode; deletions never happen here")
	gf.String("workspaces-root", "", "Override the workspaces root path (default: same as the daemon)")
	gf.String("output", "table", "Output format: table or json")
	_ = daemonGcCmd.MarkFlagRequired("dry-run")
	daemonCmd.AddCommand(daemonGcCmd)
}

func runDaemonGcDryRun(cmd *cobra.Command, _ []string) error {
	rootOverride, _ := cmd.Flags().GetString("workspaces-root")
	output, _ := cmd.Flags().GetString("output")
	profile := resolveProfile(cmd)

	root, err := resolveDiskUsageRoot(inDaemonManagedExecutionContext(), profile, rootOverride)
	if err != nil {
		return fmt.Errorf("resolve workspaces root: %w", err)
	}

	enabled := gcGuardEnvBool("MULTICA_GC_GUARD_ENABLED", true)
	execenv.ConfigureRecycleGuard(execenv.RecycleGuardConfig{
		Enabled:     enabled,
		EvidenceDir: filepath.Join(root, ".recycle-evidence"),
		EvidenceTTL: gcGuardEnvDuration("MULTICA_GC_GUARD_EVIDENCE_TTL", execenv.DefaultRecycleEvidenceTTL),
	})
	if !enabled {
		fmt.Println("recycle guard disabled (MULTICA_GC_GUARD_ENABLED=false); nothing scanned, nothing changed.")
		return nil
	}

	scans, err := daemon.RecycleGuardDryScan(root)
	if err != nil {
		return err
	}
	// Same sweeper the daemon runs: expired evidence goes, everything younger
	// than the TTL survives this and every other path.
	swept := execenv.SweepRecycleEvidence(execenv.CurrentRecycleGuardConfig().EvidenceTTL, time.Now())

	written := 0
	for _, s := range scans {
		if path, err := execenv.WriteRecycleEvidence(s); err != nil {
			fmt.Fprintf(os.Stderr, "evidence write failed for %s: %v\n", s.Target, err)
		} else if path != "" {
			written++
		}
	}

	blocked, evidence, passed := daemon.RecycleGuardDryScanSummary(scans)
	if output == "json" {
		return cli.PrintJSON(os.Stdout, map[string]any{
			"workspaces_root":  root,
			"scans":            scans,
			"blocked":          blocked,
			"evidence":         evidence,
			"passed":           passed,
			"evidence_written": written,
			"evidence_swept":   swept,
		})
	}

	fmt.Printf("recycle guard dry scan of %s: %d blocked, %d evidence, %d pass (evidence files written: %d, swept: %d)\n",
		root, blocked, evidence, passed, written, swept)
	for _, s := range scans {
		if s.Verdict == execenv.RecycleBlocked {
			fmt.Printf("guard=blocked\t%s\n", daemon.DescribeRecycleScan(s))
		}
	}
	for _, s := range scans {
		if s.Verdict == execenv.RecycleEvidence {
			fmt.Printf("guard=evidence\t%s\n", daemon.DescribeRecycleScan(s))
		}
	}
	for _, s := range scans {
		if s.Verdict != execenv.RecycleBlocked && s.Verdict != execenv.RecycleEvidence {
			fmt.Printf("guard=pass\t%s\n", daemon.DescribeRecycleScan(s))
		}
	}
	fmt.Println("dry run only — nothing was deleted; blocked recycles need a human (see the evidence files).")
	return nil
}

func gcGuardEnvBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "false", "0", "no", "off":
		return false
	}
	return true
}

func gcGuardEnvDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return fallback
}
