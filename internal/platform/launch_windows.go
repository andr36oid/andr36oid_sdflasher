package platform

import (
	"os/exec"
	"strings"
)

func RunHelper(exe, job string) error {
	// Already admin (ElevateAtStart): start the helper directly, no second prompt
	if elevated() {
		c := exec.Command(exe, "job", job)
		hide(c)
		return c.Run()
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	args := "job " + `"` + job + `"`
	// A cancelled UAC prompt ends Start-Process with a Win32 error 1223
	// (ERROR_CANCELLED); the script ends with that code then, 1 for any other error.
	c := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", uacScript(quote(exe), quote(args)))
	hide(c)
	return elevationError(c.Run(), "", []int{1223})
}
