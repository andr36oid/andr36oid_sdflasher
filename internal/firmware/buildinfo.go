package firmware

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/diskfs/go-diskfs/filesystem"
)

// BuildInfoFile is on BOOT in images since 2026-10-18 (device/gameconsole/r36s/mkimg.sh
// documents the format): the facts the image check needs, so system and vendor, EROFS
// in those images, are never read. Only the image needs it: PrepareBoot leaves it off
// the card.
const BuildInfoFile = "andr36oid-build.txt"

// BuildOnlyFiles are on the image's BOOT for the flasher alone and are removed from the
// BOOT written to the card. Nothing else is ever removed this way.
var BuildOnlyFiles = []string{BuildInfoFile}

// ParseBuildInfo reads key=value lines. Only format=1 is understood: another or no
// format line means the image is not compatible.
func ParseBuildInfo(b []byte) (map[string]string, bool) {
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if k, v, ok := strings.Cut(line, "="); ok && k != "" && !strings.HasPrefix(k, "#") {
			m[k] = v
		}
	}
	return m, m["format"] == "1"
}

// ErrNoBuildInfo is an image without andr36oid-build.txt on BOOT: made before
// 2026-10-18, or not an andr36oid release at all. This flasher doesn't read the system and
// vendor partitions, so it can't check or install such an image.
var ErrNoBuildInfo = errors.New("this image is not compatible with this flasher: its BOOT partition has no " +
	BuildInfoFile + ". Choose an andr36oid release from 2026-10-18 or later")

// releaseFacts checks the Treble mount setup and returns the build properties the
// flasher uses, all from andr36oid-build.txt on BOOT.
func releaseFacts(boot filesystem.FileSystem) ([]byte, error) {
	b, e := ReadFile(boot, "/"+BuildInfoFile, 64<<10)
	if e != nil {
		return nil, ErrNoBuildInfo
	}
	m, ok := ParseBuildInfo(b)
	if !ok {
		return nil, ErrNoBuildInfo
	}
	if m["vendor_first_stage_mount"] != "1" {
		return nil, errors.New("vendor does not contain the Treble mount configuration")
	}
	if m["vendor_vintf"] != "1" {
		return nil, errors.New("Treble vendor manifest missing")
	}
	var props bytes.Buffer
	for _, k := range []string{"ro.treble.enabled", "ro.andr36oid.version"} {
		if v, ok := m[k]; ok {
			fmt.Fprintf(&props, "%s=%s\n", k, v)
		}
	}
	return props.Bytes(), nil
}
