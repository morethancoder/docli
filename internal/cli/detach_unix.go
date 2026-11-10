//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// detach starts the process in its own session so it outlives the command
// and never receives the terminal's signals.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
