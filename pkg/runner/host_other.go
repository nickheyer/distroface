//go:build !unix

package runner

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// Liveness cannot be probed here, assume it is gone
func processAlive(pid int) bool {
	return false
}
