//go:build !linux && !darwin

package verification

import "os/exec"

func configureProcessGroup(*exec.Cmd) bool { return false }
func terminateProcessGroup(*exec.Cmd)      {}
