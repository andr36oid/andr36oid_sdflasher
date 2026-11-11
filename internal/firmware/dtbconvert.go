package firmware

import (
	"errors"
	"os"
	"path"
	"strings"

	"github.com/andr36oid/andr36oid-sdflasher/internal/dtbconv"
	"github.com/diskfs/go-diskfs/filesystem"
)

// BootOptions are extra choices for PrepareBoot.
type BootOptions struct {
	// ConvertDTB treats the custom DTB as an ArkOS, dArkOS or stock EmuELEC
	// DTB of the user's console and converts it (internal/dtbconv).
	ConvertDTB bool
	// UserdataGiB is written to userdata-size.txt for the first-boot resizer (0: automatic).
	UserdataGiB int
	// Keep are files from the card's old BOOT written again on an update (KeptFiles).
	Keep map[string][]byte
	// Prepared: the flasher partitions the card itself (flash.Plan.Prepared), so nothing
	// is left for the first-boot resizer and it gets no size wish.
	Prepared bool
	// Files are written to the top of BOOT as they are (flash.Plan.BootFiles).
	Files map[string][]byte
}

// BackupDir is the BOOT folder that keeps the user's original DTB, the
// DTBs the conversion replaced and the conversion report.
const BackupDir = "andr36oid-dtb-backup"

// Conversion is a converted console DTB plus the device profile whose boot
// files it goes with.
type Conversion struct {
	*dtbconv.Result
	Profile string // device profile of the board family, e.g. "Experimental/EE-G80-Panel8"
	Input   []byte // the user's original file
}

func readCustom(custom string) ([]byte, error) {
	st, e := os.Stat(custom)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Size() > 4<<20 {
		return nil, errors.New("custom DTB is too large or not a regular file")
	}
	b, e := os.ReadFile(custom)
	if e != nil {
		return nil, e
	}
	if !validDTB(b) {
		return nil, errors.New("custom file is not a valid flattened device tree")
	}
	return b, nil
}

func findProfile(im *Image, id string) *Profile {
	for i := range im.Profiles {
		if im.Profiles[i].ID == id {
			return &im.Profiles[i]
		}
	}
	return nil
}

// imageBases takes the base DTBs from the image when it has them, so the
// result matches the kernel being written; else the built-in copies.
func imageBases(fs filesystem.FileSystem, im *Image) dtbconv.Bases {
	b := dtbconv.EmbeddedBases()
	read := func(name string) []byte {
		if d, e := ReadFile(fs, name, 4<<20); e == nil && validDTB(d) {
			return d
		}
		return nil
	}
	for fam, id := range dtbconv.BaseProfiles {
		p := findProfile(im, id)
		if p == nil {
			continue
		}
		d := read(p.Kernel)
		if d == nil {
			continue
		}
		switch fam {
		case dtbconv.Genuine:
			b.Genuine = d
			for _, n := range p.BootFiles {
				if strings.HasSuffix(n, ".dtb") {
					if u := read(n); u != nil {
						b.UBoot = u
					}
					break
				}
			}
		case dtbconv.EE:
			b.EE = d
		case dtbconv.Soy:
			b.Soy = d
		}
	}
	return b
}

func convertWith(fs filesystem.FileSystem, im *Image, input []byte) (*Conversion, error) {
	r, e := dtbconv.Convert(input, imageBases(fs, im))
	if e != nil {
		return nil, e
	}
	c := &Conversion{Result: r, Input: input}
	if p := findProfile(im, dtbconv.BaseProfiles[r.Family]); p != nil {
		c.Profile = p.ID
	}
	return c, nil
}

// ConvertCustom converts the user's console DTB against the image, without
// writing anything. The UI shows the result before the write starts.
func ConvertCustom(im *Image, custom string) (*Conversion, error) {
	input, e := readCustom(custom)
	if e != nil {
		return nil, e
	}
	f, e := os.Open(im.Path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	fs, e := OpenBoot(f, im.Table, true)
	if e != nil {
		return nil, e
	}
	return convertWith(fs, im, input)
}

// writeConversion writes the boot logo DTBs and the backup folder after the
// profile's own files, so the converted panel wins.
func writeConversion(fs filesystem.FileSystem, write func(string, []byte) error, c *Conversion, selected *Profile, custom string) error {
	if _, e := fs.ReadDir(BackupDir); e != nil {
		if e := fs.Mkdir(BackupDir); e != nil {
			return e
		}
	}
	name := path.Base(strings.ReplaceAll(custom, "\\", "/"))
	if !strings.HasSuffix(strings.ToLower(name), ".dtb") || len(name) > 120 {
		name = "original.dtb"
	}
	backup := map[string][]byte{"original-" + name: c.Input, "conversion-report.txt": []byte(c.Report())}
	if b, e := ReadFile(fs, selected.Kernel, 4<<20); e == nil {
		backup["replaced-"+path.Base(selected.Kernel)] = b
	}
	for _, n := range selected.BootFiles {
		if strings.HasSuffix(n, ".dtb") {
			if b, e := ReadFile(fs, n, 4<<20); e == nil {
				backup["replaced-"+path.Base(n)] = b
			}
		}
	}
	for n, b := range backup {
		if e := write(BackupDir+"/"+n, b); e != nil {
			return e
		}
	}
	if c.UBoot != nil {
		for _, alias := range UBootDTBNames {
			if e := write(alias, c.UBoot); e != nil {
				return e
			}
		}
	}
	return nil
}
