package firmware

import (
	"io"
	"strings"

	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
)

// FirstBootFile is written to BOOT by the console once its first setup has really
// finished (the bundled apps installed, and dArkOSen set up when the flasher put it
// on the card): done=1 and build=<the build identity it finished on>. Images never
// ship it. Hotfixes only go on a card that has it: before that the console's first
// start still moves and installs things, and a hotfix could get in its way.
const FirstBootFile = "andr36oid-firstboot.txt"

// FirstBoot is how far a card's first setup is, as far as the card can tell.
type FirstBoot struct {
	// Done: set up and ready for hotfixes.
	Done bool `json:"done"`
	// Started: the console has run from the card at least once.
	Started bool `json:"started"`
}

// CardFirstBoot reads a card's first-setup state for the build on it. Consoles that
// write no record (releases before the prepared layout) are done when the bundled
// apps partition is gone: the console only gives its space to Android after the apps
// are installed.
func CardFirstBoot(d io.ReaderAt, tab layout.Table, identity string) FirstBoot {
	var f FirstBoot
	_, data := tab.Find("userdata")
	_, apps := tab.Find("AUTOAPK")
	boot, e := OpenBoot(d, tab, true)
	if e != nil {
		return f
	}
	records := false
	if b, e := ReadFile(boot, "/"+BuildInfoFile, 64<<10); e == nil {
		if m, ok := ParseBuildInfo(b); ok && m["layout"] != "" {
			records = true
		}
	}
	if b, e := ReadFile(boot, "/"+FirstBootFile, 4096); e == nil {
		m := map[string]string{}
		for _, line := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
				m[k] = v
			}
		}
		f.Started = true
		f.Done = m["done"] == "1" && identity != "" && m["build"] == identity
		return f
	}
	if records {
		// a prepared card has userdata from the start; its start is empty until
		// Android formats it on the first start
		if data {
			if p, ok := tab.Find("userdata"); ok {
				head := make([]byte, 4096)
				if _, e := d.ReadAt(head, p.Offset()); e == nil {
					for _, c := range head {
						f.Started = f.Started || c != 0
					}
				}
			}
		}
		return f
	}
	f.Started = data
	f.Done = data && !apps
	return f
}
