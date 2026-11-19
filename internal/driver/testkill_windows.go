// Why: a test child over its timeout may have started processes of its own,
// and killing the child alone would leave them running.
// What: prepareChild and killTree for Windows.
// How: taskkill /T /F ends the child's process tree: the child and every
// process it started that is still running. A job object per child would
// need SetInformationJobObject with a struct pointer, so the unsafe package,
// which the compiler does not import.

package driver

import (
	"os/exec"
	"strconv"
	"syscall"
)

// prepareChild starts the child in a new process group, so a console
// Ctrl+C meant for mk is not also delivered to it.
func prepareChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killTree kills cmd's process and its descendants.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}
