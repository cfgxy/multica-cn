//go:build windows

package agent

import (
	"os/exec"
	"syscall"
	"testing"
)

// TestHideAgentWindowSetsCreateNewConsole guards against a regression where
// hideAgentWindow reverts to CREATE_NO_WINDOW. CREATE_NO_WINDOW strips the
// console entirely, which forces Windows to allocate a new visible console
// per grandchild that doesn't itself pass CREATE_NO_WINDOW — the popup
// storm reported in #1521.
func TestHideAgentWindowSetsCreateNewConsole(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "echo", "hi")
	hideAgentWindow(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr should be initialized")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Error("HideWindow should be true")
	}
	if cmd.SysProcAttr.CreationFlags&createNewConsole == 0 {
		t.Errorf("CreationFlags should include CREATE_NEW_CONSOLE (0x%x), got 0x%x",
			createNewConsole, cmd.SysProcAttr.CreationFlags)
	}
	const createNoWindow = 0x08000000
	if cmd.SysProcAttr.CreationFlags&createNoWindow != 0 {
		t.Errorf("CreationFlags must NOT include CREATE_NO_WINDOW (0x%x), got 0x%x — "+
			"see #1521 for why this causes grandchild popups",
			createNoWindow, cmd.SysProcAttr.CreationFlags)
	}
}

// TestHideAgentWindowPreservesExistingSysProcAttr ensures hideAgentWindow
// does not overwrite fields set by callers — a regression caught in PR #1474.
func TestHideAgentWindowPreservesExistingSysProcAttr(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "echo", "hi")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
	hideAgentWindow(cmd)

	if cmd.SysProcAttr.CreationFlags&0x00000200 == 0 {
		t.Error("existing CreationFlags bits should be preserved")
	}
	if cmd.SysProcAttr.CreationFlags&createNewConsole == 0 {
		t.Error("CREATE_NEW_CONSOLE should be OR'd into existing flags")
	}
}
