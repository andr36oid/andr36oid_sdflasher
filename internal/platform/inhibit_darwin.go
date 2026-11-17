package platform

import (
	"os"
	"os/exec"
	"strconv"
	"sync"
)

// InhibitSleep (see inhibit_linux.go) runs caffeinate -i for as long as it holds;
// -w ends caffeinate with this process too, should it never be released.
func InhibitSleep(why string) func() {
	cmd := exec.Command("/usr/bin/caffeinate", "-i", "-w", strconv.Itoa(os.Getpid()))
	if cmd.Start() != nil {
		return func() {}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			cmd.Process.Kill()
			cmd.Wait()
		})
	}
}
