package platform

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// A cancelled or refused administrator prompt is told apart from a helper that
// failed: by the exit code (pkexec 126/127, UAC 1223) or the words of the prompt
// (macOS "User canceled. (-128)").
func TestElevationRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	run := func(code string) error { return exec.Command("sh", "-c", "exit "+code).Run() }
	for _, c := range []struct {
		e       error
		stderr  string
		codes   []int
		words   []string
		refused bool
	}{
		{run("126"), "", []int{126, 127}, nil, true},
		{run("127"), "Error executing command as another user: Not authorized", []int{126, 127}, []string{"not authorized"}, true},
		{run("1"), "", []int{126, 127}, nil, false},
		{run("1"), "execution error: User canceled. (-128)", nil, []string{"(-128)", "user canceled"}, true},
		{run("1"), "the card is gone", nil, []string{"(-128)"}, false},
	} {
		got := elevationError(c.e, c.stderr, c.codes, c.words...)
		if errors.Is(got, ErrElevationRefused) != c.refused {
			t.Errorf("%v %q: %v", c.e, c.stderr, got)
		}
		if c.stderr != "" && !strings.Contains(got.Error(), c.stderr) {
			t.Errorf("stderr lost: %v", got)
		}
	}
	if elevationError(nil, "anything", []int{126}) != nil {
		t.Fatal("success is no error")
	}
	if s := uacScript("'C:\\a b\\h.exe'", "'job \"x\"'"); !strings.Contains(s, "-ErrorAction Stop") || !strings.Contains(s, "1223") || !strings.Contains(s, "exit $p.ExitCode") {
		t.Fatal(s)
	}
}
