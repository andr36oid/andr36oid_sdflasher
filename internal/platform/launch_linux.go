package platform

import (
	"bytes"
	"os"
	"os/exec"
)

func hostCommand(name string, args ...string) *exec.Cmd {
	if os.Getenv("FLATPAK_ID") != "" {
		return exec.Command("flatpak-spawn", append([]string{"--host", name}, args...)...)
	}
	return exec.Command(name, args...)
}
func RunHelper(exe, job string) error {
	if os.Geteuid() == 0 {
		return exec.Command(exe, "job", job).Run()
	}
	// pkexec ends with 126 when the password dialog was dismissed and 127 when
	// access was not given (the helper ends with 0 or 1)
	c := hostCommand("pkexec", exe, "job", job)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	return elevationError(c.Run(), stderr.String(), []int{126, 127}, "not authorized", "dismissed")
}
