//go:build !linux

package execenv

import (
	"fmt"
	"os"
)

// RunSnapshotWorker is the non-linux fail-closed stub: snapshot isolation
// requires Linux user namespaces and kernel overlayfs. The daemon never
// advertises the capability on this platform, so reaching here is a wiring
// bug — refuse loudly instead of running anything.
func RunSnapshotWorker([]string) int {
	fmt.Fprintln(os.Stderr, "snapshot-worker: snapshot isolation requires Linux")
	return 126
}

// RunSnapshotProbe is the non-linux stub: always fails, keeping the
// capability advertisement off on this platform by construction.
func RunSnapshotProbe([]string) int {
	fmt.Fprintln(os.Stderr, "snapshot-probe: snapshot isolation requires Linux")
	return 1
}

// ProbeSnapshotSupport is the non-linux stub: the capability is off by
// construction, with a reason the daemon logs at startup.
func ProbeSnapshotSupport(_, _ string) (bool, string) {
	return false, "snapshot isolation requires Linux user namespaces"
}
