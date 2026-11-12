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
// in those images, are never read. It stays on the card so hotfixes can be matched
// to the exact installed build.
const BuildInfoFile = "andr36oid-build.txt"

// BuildInfoV2File replaces it from the hybrid release on (format=2): BOOT keeps its
// pictures once per screen resolution there, which flashers before this one cannot
// install. They look only for the old name and so turn such a release down.
const BuildInfoV2File = "andr36oid-build-v2.txt"

// readBuildInfo returns the release's own description, the newer file first. Each
// name carries its own format only: format=2 under the old name is still refused.
func readBuildInfo(boot filesystem.FileSystem) ([]byte, error) {
	name, format := BuildInfoV2File, "2"
	b, e := ReadFile(boot, "/"+name, 64<<10)
	if e != nil {
		name, format = BuildInfoFile, "1"
		if b, e = ReadFile(boot, "/"+name, 64<<10); e != nil {
			return nil, e
		}
	}
	if m, _ := ParseBuildInfo(b); m["format"] != format {
		return nil, errors.New(name + " has another format than " + format)
	}
	return b, nil
}

// BuildIdentity returns the exact identity used by the on-device hotfix applier.
// Missing or incomplete build facts must never match an index entry.
func BuildIdentity(boot filesystem.FileSystem) string {
	b, e := readBuildInfo(boot)
	if e != nil {
		return ""
	}
	m, ok := ParseBuildInfo(b)
	if !ok || m["ro.andr36oid.version"] == "" || m["ro.product.build.version.incremental"] == "" {
		return ""
	}
	return m["ro.andr36oid.version"] + "|" + m["ro.product.build.version.incremental"]
}

// ParseBuildInfo reads key=value lines. Formats 1 and 2 are understood: another or no
// format line means the image is not compatible.
func ParseBuildInfo(b []byte) (map[string]string, bool) {
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if k, v, ok := strings.Cut(line, "="); ok && k != "" && !strings.HasPrefix(k, "#") {
			m[k] = v
		}
	}
	return m, m["format"] == "1" || m["format"] == "2"
}

// FactKeys are the lines of andr36oid-build.txt kept in Image.Facts.
var FactKeys = []string{"layout", "subsystem", "kernel_line", "gpu_variant", "f2fs_compression"}

// ErrNoBuildInfo is an image without andr36oid-build.txt on BOOT: made before
// 2026-10-18, or not an andr36oid release at all. This flasher doesn't read the system and
// vendor partitions, so it can't check or install such an image.
var ErrNoBuildInfo = errors.New("this image is not compatible with this flasher: its BOOT partition has no " +
	BuildInfoFile + ". Choose an andr36oid release from 2026-10-22 or later")

// releaseFacts checks the Treble mount setup and returns the build properties the
// flasher uses, all from andr36oid-build.txt on BOOT.
func releaseFacts(boot filesystem.FileSystem) ([]byte, error) {
	b, e := readBuildInfo(boot)
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
	for _, k := range append([]string{"ro.treble.enabled", "ro.andr36oid.version"}, FactKeys...) {
		if v, ok := m[k]; ok {
			fmt.Fprintf(&props, "%s=%s\n", k, v)
		}
	}
	return props.Bytes(), nil
}
