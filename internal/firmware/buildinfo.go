package firmware

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/andr36oid/andr36oid-sdflasher/internal/extfs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
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
// format line means the file is ignored and the partitions are read instead.
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

// releaseFacts checks the Treble mount setup and returns the build properties the
// flasher uses: from andr36oid-build.txt on BOOT when the image has one, else from the
// ext4 system and vendor partitions (releases before 2026-10-18).
func releaseFacts(f io.ReaderAt, t layout.Table, boot filesystem.FileSystem) ([]byte, error) {
	if b, e := ReadFile(boot, "/"+BuildInfoFile, 64<<10); e == nil {
		if m, ok := ParseBuildInfo(b); ok {
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
	}
	vendor, _ := t.Find("vendor")
	vfs, e := extfs.Open(f, vendor.Offset(), vendor.Size())
	if e != nil {
		return nil, fmt.Errorf("vendor filesystem: %w", e)
	}
	fstab, e := vfs.ReadFile("/etc/fstab.rk30board", 128<<10)
	if e != nil || !bytes.Contains(fstab, []byte("by-name/vendor")) || !bytes.Contains(fstab, []byte("first_stage_mount")) {
		return nil, errors.New("vendor does not contain the Treble mount configuration")
	}
	vintf, e := vfs.ReadFile("/etc/vintf/manifest.xml", 4<<20)
	if e != nil || !bytes.Contains(vintf, []byte("manifest")) {
		return nil, errors.New("Treble vendor manifest missing")
	}
	sys, _ := t.Find("system")
	sfs, e := extfs.Open(f, sys.Offset(), sys.Size())
	if e != nil {
		return nil, fmt.Errorf("system filesystem: %w", e)
	}
	var props []byte
	for _, name := range []string{"/system/build.prop", "/build.prop", "/system/etc/prop.default", "/system/product/build.prop", "/product/build.prop"} {
		b, _ := sfs.ReadFile(name, 1<<20)
		props = append(props, b...)
		props = append(props, '\n')
	}
	return props, nil
}
