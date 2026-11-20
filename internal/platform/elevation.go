package platform

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrElevationRefused: the administrator prompt (pkexec, UAC, the macOS password
// dialog) was cancelled or did not give access, so the helper never ran.
var ErrElevationRefused = errors.New("administrator access was not given")

// elevationError is e, the error of the command that asked for administrator
// rights, marked ErrElevationRefused when its exit code or its words say the
// prompt was cancelled or refused. codes are the command's exit codes for that;
// words are matched in its error output (lower case).
func elevationError(e error, stderr string, codes []int, words ...string) error {
	if e == nil {
		return nil
	}
	refused := false
	var exit *exec.ExitError
	if errors.As(e, &exit) {
		for _, c := range codes {
			if exit.ExitCode() == c {
				refused = true
			}
		}
	}
	lower := strings.ToLower(stderr)
	for _, w := range words {
		if strings.Contains(lower, w) {
			refused = true
		}
	}
	detail := e
	if s := strings.TrimSpace(stderr); s != "" {
		detail = fmt.Errorf("%w: %s", e, s)
	}
	if refused {
		return fmt.Errorf("%w (%w)", ErrElevationRefused, detail)
	}
	return detail
}
