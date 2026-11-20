package platform

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
)

func RunHelper(exe, job string) error {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	cmd := quote(exe) + " job " + quote(job)
	c := exec.Command("/usr/bin/osascript", "-e", "do shell script "+strconv.Quote(cmd)+" with administrator privileges")
	var stderr bytes.Buffer
	c.Stderr = &stderr
	// a cancelled password dialog: "User canceled. (-128)"
	return elevationError(c.Run(), stderr.String(), nil, "(-128)", "user canceled")
}
