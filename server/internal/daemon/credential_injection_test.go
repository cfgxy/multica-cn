package daemon

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestVerifyTaskCredentialInjection(t *testing.T) {
	valid := map[string]string{
		"MULTICA_TOKEN":        "mat_78aa02deadbeef",
		"MULTICA_SERVER_URL":   "http://localhost:8080",
		"MULTICA_DAEMON_PORT":  "9478",
		cli.TaskConfigRootEnv:  "/home/guxy/multica_workspaces/ws/task-1/multica-config",
		"MULTICA_WORKSPACE_ID": "42f61469-c332-41f3-a2b4-018667473993",
		"MULTICA_AGENT_ID":     "43324e0c-014a-4866-bc17-de6f94850fd5",
		"MULTICA_TASK_ID":      "01a08744-38c1-712a-8670-02d65192ff58",
	}
	if err := verifyTaskCredentialInjection(valid); err != nil {
		t.Fatalf("complete injection must pass, got: %v", err)
	}

	cases := []struct {
		name string
		mut  func(env map[string]string)
		want string // substring of the expected error
	}{
		{"empty token", func(e map[string]string) { e["MULTICA_TOKEN"] = "" }, "MULTICA_TOKEN"},
		{"non-task-scoped token", func(e map[string]string) { e["MULTICA_TOKEN"] = "mc_owner-token" }, "MULTICA_TOKEN"},
		{"missing server url", func(e map[string]string) { delete(e, "MULTICA_SERVER_URL") }, "MULTICA_SERVER_URL"},
		{"missing daemon port", func(e map[string]string) { delete(e, "MULTICA_DAEMON_PORT") }, "MULTICA_DAEMON_PORT"},
		{"missing task config root", func(e map[string]string) { delete(e, cli.TaskConfigRootEnv) }, "MULTICA_TASK_CONFIG_ROOT"},
		{"missing workspace id", func(e map[string]string) { delete(e, "MULTICA_WORKSPACE_ID") }, "MULTICA_WORKSPACE_ID"},
		{"missing agent id", func(e map[string]string) { delete(e, "MULTICA_AGENT_ID") }, "MULTICA_AGENT_ID"},
		{"missing task id", func(e map[string]string) { delete(e, "MULTICA_TASK_ID") }, "MULTICA_TASK_ID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := make(map[string]string, len(valid))
			for k, v := range valid {
				env[k] = v
			}
			tc.mut(env)
			err := verifyTaskCredentialInjection(env)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), "task credential injection incomplete") ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}
