package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestRuntimeExecutionSource(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	_, binding := operationBinding(t)
	runtime := dbfx.Insert(t, "agent_runtime", testutil.Cols{"workspace_id": testWorkspaceID, "daemon_id": binding, "name": "Claude (host)", "provider": "claude", "runtime_mode": "local", "owner_id": testUserID})
	read := func() AgentRuntimeResponse {
		t.Helper()
		var runtimes []AgentRuntimeResponse
		testutil.Call(t, testHandler.ListAgentRuntimes, computerTestRequest(http.MethodGet, "/api/runtimes", nil)).Want(200).JSON(&runtimes)
		for _, rt := range runtimes {
			if rt.ID == runtime {
				return rt
			}
		}
		t.Fatal("runtime missing")
		return AgentRuntimeResponse{}
	}
	source := read().ExecutionSource
	if source == nil || source.LinuxUser != "alice" || source.Host != "fake.invalid" || source.BindingID != binding || !source.Preferred {
		t.Fatalf("wrong execution source: %+v", source)
	}
	// Sharing an owner and workspace does not imply sharing the OS account.
	unmanaged := dbfx.Insert(t, "agent_runtime", testutil.Cols{"workspace_id": testWorkspaceID, "daemon_id": "unmanaged-daemon", "name": "Claude (host)", "provider": "claude", "runtime_mode": "local", "owner_id": testUserID})
	responses := []AgentRuntimeResponse{{ID: unmanaged}}
	if err := testHandler.enrichRuntimeSources(context.Background(), testWorkspaceID, responses); err != nil {
		t.Fatal(err)
	}
	if responses[0].ExecutionSource != nil {
		t.Fatal("unmanaged runtime attributed to a managed account")
	}
	otherWorkspace := dbfx.Workspace(t, "Other runtime workspace", "runtime-source-other")
	dbfx.Exec(t, `UPDATE computer_binding SET workspace_id=$1 WHERE id=$2`, otherWorkspace, binding)
	source = read().ExecutionSource
	if source == nil || source.Preferred || source.LinuxUser != "alice" {
		t.Fatalf("cross-workspace source lost or preferred: %+v", source)
	}
	dbfx.Exec(t, `UPDATE computer_binding SET workspace_id=$1,archived_at=now() WHERE id=$2`, testWorkspaceID, binding)
	if source = read().ExecutionSource; source == nil || source.Preferred {
		t.Fatal("archived binding selected as default")
	}
	dbfx.Exec(t, `UPDATE computer_binding SET archived_at=NULL WHERE id=$1`, binding)
	// An untrusted daemon ID alone must not attribute another owner's OS account.
	dbfx.Exec(t, `UPDATE agent_runtime SET owner_id=NULL WHERE id=$1`, runtime)
	if read().ExecutionSource != nil {
		t.Fatal("runtime inherited another owner's binding")
	}
	dbfx.Exec(t, `UPDATE agent_runtime SET owner_id=$1 WHERE id=$2`, testUserID, runtime)
	responses = []AgentRuntimeResponse{{ID: runtime}}
	if err := testHandler.enrichRuntimeSources(context.Background(), otherWorkspace, responses); err != nil {
		t.Fatal(err)
	}
	if responses[0].ExecutionSource != nil {
		t.Fatal("runtime from another workspace was enriched")
	}
}
