//go:build windows

package execenv

import "fmt"

func repairTaskContextMarkerFile(markerPath string, repair func([]byte) ([]byte, error)) error {
	return fmt.Errorf("repair task context marker %s: secure descriptor-relative replacement is unavailable on Windows", markerPath)
}
