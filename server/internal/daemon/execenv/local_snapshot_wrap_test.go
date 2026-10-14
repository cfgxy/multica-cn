//go:build linux

package execenv

import (
	"os"
	"syscall"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// TestBuildSnapshotWrapSpecSupportsLauncherRebuild pins the writer/reader
// contract across the supervised launcher boundary. The launcher keeps only
// the worker's stored argv, so the spec JSON the daemon encodes into argv
// must carry everything agent.snapshotWrapFromArgv needs to rebuild the
// namespace ID maps — it fails closed (launcher exit 126) on a spec without
// host_uid/host_gid, which is exactly how a real snapshot task died before
// this contract was enforced here.
func TestBuildSnapshotWrapSpecSupportsLauncherRebuild(t *testing.T) {
	wrap, err := BuildSnapshotWrap("/fake/multica", &SnapshotRunSpec{
		TaskID: "task-1",
		Units:  []SnapshotUnitSpec{{Target: "/tmp/t", Alias: "/tmp/a", Upper: "/tmp/u", Work: "/tmp/w"}},
		Cwd:    "/tmp/t",
	})
	if err != nil {
		t.Fatal(err)
	}
	argv := []string{agent.SnapshotWorkerSubcommand, wrap.SpecJSON, agent.SnapshotWorkerSep, "/bin/true"}
	attr, err := agent.SnapshotSysProcAttrFor(argv)
	if err != nil {
		t.Fatalf("launcher must rebuild clone flags from a BuildSnapshotWrap spec: %v", err)
	}
	if attr == nil {
		t.Fatal("wrapped argv must produce snapshot clone attributes, got nil")
	}
	if attr.Cloneflags != syscall.CLONE_NEWUSER|syscall.CLONE_NEWNS {
		t.Fatalf("clone flags = %v, want CLONE_NEWUSER|CLONE_NEWNS", attr.Cloneflags)
	}
	if len(attr.UidMappings) != 1 || attr.UidMappings[0].ContainerID != 0 || attr.UidMappings[0].HostID != os.Getuid() {
		t.Fatalf("uid map = %+v, want single map 0 -> %d", attr.UidMappings, os.Getuid())
	}
	if len(attr.GidMappings) != 1 || attr.GidMappings[0].ContainerID != 0 || attr.GidMappings[0].HostID != os.Getgid() {
		t.Fatalf("gid map = %+v, want single map 0 -> %d", attr.GidMappings, os.Getgid())
	}
}
