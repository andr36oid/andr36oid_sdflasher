package platform

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"
)

// Eject unmounts the card's volumes and writes out everything still cached, so
// the card can be pulled. A USB reader is then powered down too, as the
// desktop's own eject does; built-in readers (PCIe ones such as Realtek's) can't
// be, and that's fine: the card is safe to remove once it's unmounted and synced.
func Eject(d Drive) error {
	d, e := Revalidate(d)
	if e != nil {
		return e
	}
	_, e = exec.LookPath("udisksctl")
	udisks := e == nil
	for _, v := range d.Volumes {
		c := exec.Command("umount", v)
		if udisks {
			c = exec.Command("udisksctl", "unmount", "--no-user-interaction", "-b", v)
		}
		if b, e := c.CombinedOutput(); e != nil {
			return fmt.Errorf("cannot unmount %s: %s", v, strings.TrimSpace(string(b)))
		}
	}
	// the card's own buffers; a sync of everything can hang for minutes on a computer
	// with a slow network or USB disk, so only when the card can't be opened
	if f, e := os.Open(d.Path); e == nil {
		f.Sync()
		f.Close()
	} else {
		unix.Sync()
	}
	if udisks {
		exec.Command("udisksctl", "power-off", "--no-user-interaction", "-b", d.Path).Run()
	}
	return nil
}
