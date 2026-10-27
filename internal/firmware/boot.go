package firmware

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// UBootDTBNames are the fixed names U-Boot loads its screen setup from, whatever the
// console: the old ArkOS-style U-Boot and the U-Boot on the eMMC of some clones read
// rg351*-kernel.dtb, the andr36oid U-Boot (github.com/andr36oid/u-boot) reads
// rg351*-uboot.dtb, each picking the variant by its board id.
var UBootDTBNames = []string{
	"rg351mp-kernel.dtb", "rg351p-kernel.dtb", "rg351v-kernel.dtb",
	"rg351mp-uboot.dtb", "rg351p-uboot.dtb", "rg351v-uboot.dtb",
}

// KeptFiles are the user's own settings on BOOT, carried over on an update: Kernel
// Tweaks (andr36oid's Settings > System > Kernel Tweaks) saves the kernel command line
// additions there, the refresh rates the panel may use among them. The data card
// marker of an eMMC install stays too: the card's next start offers to update the eMMC.
var KeptFiles = []string{"kernel-tweaks.txt", "andr36oid-datacard.txt"}

// KeptDirs are folders on BOOT whose files are the user's too: andr36oid's hotfixes
// (.fix.zip), carried over on an update as "<folder>/<file>" in BootOptions.Keep. Files
// right in the folder only, each up to KeptDirFileLimit.
var KeptDirs = []string{"hotfix"}

const KeptDirFileLimit = 64 << 20

const CustomWarning = "Custom DTBs must be built specifically for andr36oid. DTBs from other custom firmwares are unlikely to work."

func PrepareBoot(im *Image, profile string, noROMs, updating bool, custom, dir string, opts ...BootOptions) (string, error) {
	var userdataGiB int
	var keep map[string][]byte
	if len(opts) > 0 {
		userdataGiB = opts[0].UserdataGiB
		keep = opts[0].Keep
	}
	src, e := os.Open(im.Path)
	if e != nil {
		return "", e
	}
	defer src.Close()
	p, _ := im.Table.Find("BOOT")
	out, e := os.CreateTemp(dir, "boot-*.fat")
	if e != nil {
		return "", e
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			os.Remove(out.Name())
		}
	}()
	if _, e = io.Copy(out, io.NewSectionReader(src, p.Offset(), p.Size())); e != nil {
		return "", e
	}
	fs, e := fat32.Read(file.New(out, false), p.Size(), 0, 512)
	if e != nil {
		return "", e
	}
	var conv *Conversion
	if custom != "" && len(opts) > 0 && opts[0].ConvertDTB {
		input, e := readCustom(custom)
		if e != nil {
			return "", e
		}
		if conv, e = convertWith(fs, im, input); e != nil {
			return "", e
		}
		if conv.Profile != "" {
			profile = conv.Profile
		}
	}
	var selected *Profile
	for i := range im.Profiles {
		if im.Profiles[i].ID == profile {
			selected = &im.Profiles[i]
		}
	}
	if selected == nil {
		return "", errors.New("profile missing from image")
	}
	write := func(name string, b []byte) error {
		return writeBootFile(fs, name, b)
	}
	script, e := ReadFile(fs, "/boot.ini-android", 128<<10)
	if e != nil {
		return "", e
	}
	for _, n := range selected.BootFiles {
		if path.Base(n) == "boot.ini-android" {
			script, e = ReadFile(fs, n, 128<<10)
			if e != nil {
				return "", e
			}
		}
	}
	if !bytes.Contains(script, []byte("ramdisk.uimg")) {
		return "", errors.New("selected profile has no Treble boot script")
	}
	// "load mmc 1:1 ${fdt_addr_r} x.dtb", or since 2026-10-13 "if load ... x.dtb; then"
	re := regexp.MustCompile(`(?m)^\s*(?:if\s+)?load\s+mmc\s+\S+\s+\S+\s+([^\s;]+\.dtb)\s*(?:;\s*then)?\s*$`)
	m := re.FindSubmatch(script)
	if len(m) != 2 {
		return "", errors.New("cannot determine Android DTB destination")
	}
	target := string(m[1])
	if strings.Contains(target, "/") || strings.Contains(target, "..") {
		return "", errors.New("invalid DTB destination")
	}
	dtb, e := ReadFile(fs, selected.Kernel, 4<<20)
	if e != nil {
		return "", e
	}
	if conv != nil {
		dtb = conv.Kernel
	} else if custom != "" {
		if dtb, e = readCustom(custom); e != nil {
			return "", e
		}
	}
	if e = write(target, dtb); e != nil {
		return "", e
	}
	for _, n := range selected.BootFiles {
		b, e := ReadFile(fs, n, 16<<20)
		if e != nil {
			return "", e
		}
		if e = write(path.Base(n), b); e != nil {
			return "", e
		}
		if strings.HasSuffix(n, ".dtb") {
			for _, alias := range UBootDTBNames {
				if e = write(alias, b); e != nil {
					return "", e
				}
			}
		}
	}
	if conv != nil {
		if e = writeConversion(fs, write, conv, selected, custom); e != nil {
			return "", e
		}
	}
	// The installation resizer formats userdata. It must never run on an update.
	if updating {
		if e = write("boot.ini", script); e != nil {
			return "", e
		}
	}
	if noROMs {
		if e = write(".noroms", nil); e != nil {
			return "", e
		}
	} else {
		if _, e = ReadFile(fs, "/.noroms", 64); e == nil {
			if e = fs.Remove(".noroms"); e != nil {
				return "", e
			}
		}
	}
	if !updating {
		if userdataGiB > 0 && !noROMs {
			if e = write("userdata-size.txt", []byte(strconv.Itoa(userdataGiB)+"\n")); e != nil {
				return "", e
			}
		} else if _, e = ReadFile(fs, "/userdata-size.txt", 64); e == nil {
			if e = fs.Remove("userdata-size.txt"); e != nil {
				return "", e
			}
		}
	}
	if updating {
		for _, name := range KeptFiles {
			if b, ok := keep[name]; ok {
				if e = write(name, b); e != nil {
					return "", e
				}
			}
		}
		for _, dir := range KeptDirs {
			names := make([]string, 0)
			for name := range keep {
				if strings.HasPrefix(name, dir+"/") {
					names = append(names, name)
				}
			}
			if len(names) == 0 {
				continue
			}
			// the image may ship the folder already (with its README)
			if _, e = fs.ReadDir(dir); e != nil {
				if e = fs.Mkdir(dir); e != nil {
					return "", e
				}
			}
			sort.Strings(names)
			for _, name := range names {
				if e = write(name, keep[name]); e != nil {
					return "", e
				}
			}
		}
	}
	if e = write("andr36oid-profile.txt", []byte(profile+"\n")); e != nil {
		return "", e
	}
	if e = out.Sync(); e != nil {
		return "", e
	}
	check, e := ReadFile(fs, "/"+target, 4<<20)
	if e != nil || !bytes.Equal(check, dtb) {
		return "", fmt.Errorf("prepared DTB did not verify")
	}
	if updating {
		b, e := ReadFile(fs, "/boot.ini", 128<<10)
		if e != nil || !bytes.Equal(b, script) {
			return "", errors.New("prepared Android boot script did not verify")
		}
	}
	ok = true
	return out.Name(), nil
}
