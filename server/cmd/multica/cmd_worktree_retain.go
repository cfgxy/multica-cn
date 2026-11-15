package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func newWorktreeRetainCommand() *cobra.Command {
	var root, owner, reason, version, evidence, until string
	var release bool
	cmd := &cobra.Command{
		Use:   "retain",
		Short: "Retain the current task's managed environment for an expiring dependency",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if root == "" {
				return fmt.Errorf("--env-root is required")
			}
			var expiry time.Time
			if !release {
				var err error
				expiry, err = time.Parse(time.RFC3339, until)
				if err != nil {
					return fmt.Errorf("--until must be a future RFC3339 timestamp")
				}
			}
			result, err := execenv.SetEnvironmentRetention(root, os.Getenv("MULTICA_TASK_ID"), os.Getenv("MULTICA_WORKSPACE_ID"), execenv.EnvironmentRetention{
				Owner: owner, Reason: reason, Version: version, Evidence: evidence, Until: expiry,
			}, release, time.Now())
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		},
	}
	cmd.Flags().StringVar(&root, "env-root", "", "existing daemon-managed environment root")
	cmd.Flags().StringVar(&owner, "owner", "", "responsible owner of the next dependency")
	cmd.Flags().StringVar(&reason, "reason", "", "unfinished authorized work requiring this environment")
	cmd.Flags().StringVar(&version, "version", "", "full commit SHA or artifact SHA256")
	cmd.Flags().StringVar(&evidence, "evidence", "", "accessible evidence and rebuild inputs")
	cmd.Flags().StringVar(&until, "until", "", "expiry as an RFC3339 timestamp")
	cmd.Flags().BoolVar(&release, "release", false, "release the hold; does not immediately delete resources")
	return cmd
}

func init() { worktreeCmd.AddCommand(newWorktreeRetainCommand()) }
