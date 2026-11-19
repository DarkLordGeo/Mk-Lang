// Why: a test child over its timeout may have started processes of its own,
// and killing the child alone would leave them running.
// What: prepareChild and killTree for Unix hosts.
// How: the child runs in a process group of its own, and a child over its
// timeout is killed with the whole group.

//go:build !windows

package driver

import (
	"os/exec"
	"syscall"
)

// prepareChild puts the child in a new process group.
func prepareChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree kills cmd's process group.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
