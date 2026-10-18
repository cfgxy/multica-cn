package agent

import (
	"encoding/json"
	"testing"
)

// Tests for the snapshotWrapFromArgv reader contract: a stored worker argv
// must yield the launch brief only when it is well-formed AND carries both
// host IDs — anything else fails closed (nil), because the caller must
// refuse to spawn rather than run the agent unwrapped.
func TestSnapshotWrapFromArgv(t *testing.T) {
	specWithIDs := func(extra string) string {
		return `{"task_id":"t1","host_uid":1000,"host_gid":1000` + extra + `}`
	}
	cases := []struct {
		name string
		args []string
		want *SnapshotWrap
	}{
		{name: "not a worker argv", args: []string{"/bin/sh", "-c", "echo hi"}, want: nil},
		{name: "too short", args: []string{SnapshotWorkerSubcommand, "{}"}, want: nil},
		{name: "missing separator at index 2", args: []string{SnapshotWorkerSubcommand, "{}", "--x", "agent"}, want: nil},
		{name: "malformed spec json", args: []string{SnapshotWorkerSubcommand, "{not json", SnapshotWorkerSep, "agent"}, want: nil},
		{name: "missing host_uid", args: []string{SnapshotWorkerSubcommand, `{"host_gid":1000}`, SnapshotWorkerSep, "agent"}, want: nil},
		{name: "missing host_gid", args: []string{SnapshotWorkerSubcommand, `{"host_uid":1000}`, SnapshotWorkerSep, "agent"}, want: nil},
		{
			name: "ids present",
			args: []string{SnapshotWorkerSubcommand, specWithIDs(`,"units":[]`), SnapshotWorkerSep, "agent", "--flag"},
			want: &SnapshotWrap{HostUID: 1000, HostGID: 1000},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := snapshotWrapFromArgv(tc.args)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("snapshotWrapFromArgv = %+v, want nil (fail closed)", got)
				}
				return
			}
			if got == nil {
				t.Fatal("snapshotWrapFromArgv = nil, want recovered brief")
			}
			if got.HostUID != tc.want.HostUID || got.HostGID != tc.want.HostGID {
				t.Fatalf("snapshotWrapFromArgv = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// The encoded SnapshotRunSpec (execenv side) must decode back through the
// reader's probe shape: whatever the daemon marshals, the launcher must be
// able to read. Pins the JSON key names on both sides of the argv contract.
func TestSnapshotWrapHostIDKeysRoundTrip(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"host_uid": 42, "host_gid": 43})
	if err != nil {
		t.Fatal(err)
	}
	wrap := snapshotWrapFromArgv([]string{SnapshotWorkerSubcommand, string(raw), SnapshotWorkerSep, "agent"})
	if wrap == nil || wrap.HostUID != 42 || wrap.HostGID != 43 {
		t.Fatalf("reader must accept snake_case host_uid/host_gid keys, got %+v", wrap)
	}
}
