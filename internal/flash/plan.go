package flash

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"io"
	"os"
	"strings"
)

type Range struct {
	Name   string `json:"name"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
}

func (r Range) End() int64 { return r.Offset + r.Length }

type Write struct {
	Range
	SourceOffset int64  `json:"source_offset"`
	Boot         bool   `json:"boot"`
	Uboot        bool   `json:"uboot_copies,omitempty"`
	System       bool   `json:"system,omitempty"`
	Data         []byte `json:"data,omitempty"`
	// Zero writes zeros; Guest writes from the plan's game system image.
	Zero  bool `json:"zero,omitempty"`
	Guest bool `json:"guest,omitempty"`
}
type Plan struct {
	Mode           string       `json:"mode"`
	Size           int64        `json:"size"`
	Writes         []Write      `json:"writes"`
	Protected      []Range      `json:"protected"`
	Table          layout.Table `json:"table"`
	OldFingerprint string       `json:"old_fingerprint"`
	Profile        string       `json:"profile"`
	NoROMs         bool         `json:"no_roms"`
	// UserdataGiB is the Android size the resizer should use; 0 is automatic.
	UserdataGiB int `json:"userdata_gib,omitempty"`
	// SystemImage is a prepared file written to system instead of the release's own.
	SystemImage string `json:"system_image,omitempty"`
	// Keep are the user's files on the old BOOT that an update writes again.
	Keep map[string][]byte `json:"keep,omitempty"`
	// Prepared: the flasher makes userdata, the bundled apps and the games partition
	// itself (prepared.go). Guest is the game system release that gets its own
	// partition; GuestDistribution says the release's console reads "distribution"
	// in GuestFile (ReadsDistribution).
	// Probes are the few places an installation writes markers to before anything
	// else, to see that the card holds what it claims (capacity.go).
	Probes            []Range `json:"probes,omitempty"`
	Prepared          bool    `json:"prepared,omitempty"`
	Guest             *Guest  `json:"guest,omitempty"`
	GuestDistribution bool    `json:"guest_distribution,omitempty"`
}
type Card struct {
	Table       layout.Table `json:"table"`
	Installed   bool         `json:"installed"`
	Treble      bool         `json:"treble"`
	Profile     string       `json:"profile"`
	NoROMs      bool         `json:"no_roms"`
	Fingerprint string       `json:"fingerprint"`
	// DataCard: the data card of an eMMC install (andr36oid-datacard.txt on BOOT). It
	// can't start; an update makes it bootable again.
	DataCard bool   `json:"data_card,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// Kept are the firmware.KeptFiles found on its BOOT.
	Kept map[string][]byte `json:"kept,omitempty"`
	// Guest is the game system in the card's own partition, nil for none. An update
	// keeps it as it is.
	Guest *CardGuest `json:"guest,omitempty"`
}

// CardGuest is what a card's record (GuestFile) says of its game system.
type CardGuest struct {
	// Distribution is the system's name ("dArkOS", "dArkOSRE", "dArkOSen"), "" when
	// the card has the partition but no readable record of it.
	Distribution string `json:"distribution,omitempty"`
	// Version is the release (MMDDYYYY), "" when not known.
	Version string `json:"version,omitempty"`
}

// cardGuest reads the record of a card that has the game system partition.
func cardGuest(record []byte) *CardGuest {
	g := &CardGuest{}
	var r struct {
		Distribution *string `json:"distribution"`
		Darkosen     string  `json:"darkosen"`
	}
	if len(record) == 0 || json.Unmarshal(record, &r) != nil {
		return g
	}
	// a record without the field is dArkOSen's (RECORD-FORMAT)
	name := "dArkOSen"
	if r.Distribution != nil {
		name = (&Guest{Distribution: *r.Distribution}).DistributionName()
		if *r.Distribution == "" {
			name = ""
		}
	}
	g.Distribution, g.Version = name, r.Darkosen
	return g
}

func Snapshot(r io.ReaderAt, size int64) (string, error) {
	h := sha256.New()
	for _, off := range []int64{0, size - 34*512} {
		b := make([]byte, 34*512)
		if _, e := r.ReadAt(b, off); e != nil {
			return "", e
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DataCardFile on BOOT marks the data card of an eMMC install (the eMMC installer writes
// it). Such a card may lack its boot.ini; an update writes a fresh one.
const DataCardFile = "andr36oid-datacard.txt"

func InspectCard(f io.ReaderAt, size int64) (Card, error) {
	c := Card{}
	var e error
	c.Fingerprint, e = Snapshot(f, size)
	if e != nil {
		return c, e
	}
	t, e := layout.Read(f, size)
	if e != nil {
		c.Reason = e.Error()
		return c, nil
	}
	c.Table = t
	boot, e := firmware.OpenBoot(f, t, true)
	if e != nil {
		c.Reason = "BOOT filesystem was not recognized"
		return c, nil
	}
	if _, e := firmware.ReadFile(boot, "/"+DataCardFile, 4096); e == nil {
		c.DataCard = true
	}
	script, e := firmware.ReadFile(boot, "/boot.ini", 128<<10)
	if !c.DataCard && (e != nil || !bytes.Contains(script, []byte("odroidgoa-uboot-config"))) {
		c.Reason = "This is not a recognized andr36oid installation"
		return c, nil
	}
	root, e := boot.ReadDir(".")
	if e != nil {
		return c, e
	}
	var active []byte
	for _, n := range root {
		if strings.HasSuffix(n.Name(), "-android.dtb") {
			b, e := firmware.ReadFile(boot, "/"+n.Name(), 4<<20)
			if e == nil {
				active = b
				break
			}
		}
	}
	if len(active) == 0 {
		c.Reason = "The existing Android device tree was not found"
		return c, nil
	}
	if !t.GPT {
		for i := range t.Parts {
			switch t.Parts[i].Number {
			case 1:
				t.Parts[i].Name = "BOOT"
			case 2:
				t.Parts[i].Name = "system"
			case 3:
				t.Parts[i].Name = "userdata"
			default:
				c.Reason = "Unrecognized legacy MBR layout"
				return c, nil
			}
		}
	}
	data, ok := t.Find("userdata")
	if !ok {
		c.Reason = "This card has not finished its first boot; use a fresh installation"
		return c, nil
	}
	magic := make([]byte, 2048)
	if _, e = f.ReadAt(magic, data.Offset()); e != nil {
		return c, e
	}
	if !((magic[1080] == 0x53 && magic[1081] == 0xef) || bytes.Equal(magic[1024:1028], []byte{0x10, 0x20, 0xf5, 0xf2})) {
		c.Reason = "The userdata filesystem was not recognized"
		return c, nil
	}
	profiles, _ := firmware.Profiles(boot)
	sum := sha256.Sum256(active)
	for _, p := range profiles {
		if p.Hash == hex.EncodeToString(sum[:]) {
			c.Profile = p.ID
			break
		}
	}

	if c.Profile == "" {
		if saved, e := firmware.ReadFile(boot, "/andr36oid-profile.txt", 4096); e == nil {
			id := strings.TrimSpace(string(saved))
			for _, p := range profiles {
				if p.ID == id {
					c.Profile = id
					break
				}
			}
		}
	}
	for _, name := range firmware.KeptFiles {
		if b, e := firmware.ReadFile(boot, "/"+name, 64<<10); e == nil {
			if c.Kept == nil {
				c.Kept = map[string][]byte{}
			}
			c.Kept[name] = b
		}
	}
	for _, dir := range firmware.KeptDirs {
		list, e := boot.ReadDir(dir)
		if e != nil {
			continue
		}
		for _, ent := range list {
			if ent.IsDir() {
				continue
			}
			name := dir + "/" + ent.Name()
			if b, e := firmware.ReadFile(boot, "/"+name, firmware.KeptDirFileLimit); e == nil {
				if c.Kept == nil {
					c.Kept = map[string][]byte{}
				}
				c.Kept[name] = b
			}
		}
	}
	if _, ok := t.Find(GuestPartition); ok {
		c.Guest = cardGuest(c.Kept[GuestFile])
	}
	_, roms := t.Find("EASYROMS")
	c.NoROMs = !roms
	c.Table = t
	c.Installed = true
	_, c.Treble = t.Find("vendor")
	return c, nil
}

// MinCardBytes is the smallest card a new installation takes: every "16 GB" card
// (15.5 to 16 GB real), not an 8 GB one. The image with its AUTOAPK partition is
// about 5 GB and Android needs room on top for the apps the setup installs.
var MinCardBytes int64 = 14e9

// TooSmall is the error for a card under MinCardBytes, nil for a big enough one.
func TooSmall(size int64) error {
	if size >= MinCardBytes {
		return nil
	}
	return fmt.Errorf("This card is too small. andr36oid needs a card of at least 16 GB; this one has %.1f GB.", float64(size)/1e9)
}

// AutoAPK is the partition with the bundled apps: in the image right after system,
// where userdata goes. The console's first start moves it behind userdata and a
// later start gives its space to userdata.
const AutoAPK = "AUTOAPK"

func Build(im *firmware.Image, card Card, size int64, mode, profile string, noROMs bool, userdataGiB int) (*Plan, error) {
	return BuildWith(im, card, size, mode, profile, noROMs, userdataGiB, Options{})
}

// BuildWith is Build with the choices only some releases offer.
func BuildWith(im *firmware.Image, card Card, size int64, mode, profile string, noROMs bool, userdataGiB int, opts Options) (*Plan, error) {
	if mode != "install" && mode != "update" {
		return nil, errors.New("invalid operation")
	}
	if size < im.Size {
		return nil, errors.New("the card is smaller than the release image")
	}
	found := false
	for _, p := range im.Profiles {
		if p.ID == profile {
			found = true
		}
	}
	if !found {
		return nil, errors.New("choose a device profile shipped with this image")
	}
	if userdataGiB < 0 || userdataGiB > 1<<20 {
		return nil, errors.New("invalid Android storage size")
	}
	if noROMs || mode != "install" {
		userdataGiB = 0
	}
	p := &Plan{Mode: mode, Size: size, Profile: profile, NoROMs: noROMs, UserdataGiB: userdataGiB, OldFingerprint: card.Fingerprint}
	p.Table = im.Table
	p.Table.Parts = nil
	apps, prepared := canPrepare(im)
	prepared = prepared && mode == "install"
	if opts.Guest != nil && !prepared {
		return nil, errors.New("a game system can only be put on the card with a fresh installation of a release that supports it")
	}
	for _, q := range im.Table.Parts {
		// an update keeps the card's userdata where the image has AUTOAPK; a prepared
		// card gets the apps behind userdata
		if (mode == "update" || prepared) && q.Name == AutoAPK {
			continue
		}
		p.Table.Parts = append(p.Table.Parts, q)
	}
	p.Table.Size = size
	p.Table.ID = layout.GUID()
	if mode == "install" {
		if e := TooSmall(size); e != nil {
			return nil, e
		}
		// Automatic on a small card is fine: the resizer gives Android the whole card.
		// A size the user chose must leave the games partition they expect.
		if prepared {
			if e := p.prepare(im, apps, userdataGiB, noROMs, opts.Guest); e != nil {
				return nil, e
			}
		} else {
			if _, roms := UserdataLayout(size, userdataGiB, noROMs); !noROMs && userdataGiB > 0 && roms == 0 {
				return nil, errors.New("this card has no room for a separate games partition with that much Android storage; choose less, or all storage for Android")
			}
			p.Writes = append(p.Writes, Write{Range: Range{"Release image", 34 * 512, im.Size - 67*512}, SourceOffset: 34 * 512})
		}
	} else {
		if !card.Installed {
			return nil, fmt.Errorf("cannot update this card: %s", card.Reason)
		}
		p.NoROMs = card.NoROMs
		p.Keep = card.Kept
		if card.Table.GPT {
			p.Table.ID = card.Table.ID
		}
		for _, q := range card.Table.Parts {
			switch q.Name {
			case "userdata", "EASYROMS", AutoAPK:
				switch q.Name {
				case "userdata":
					q.Number = 6
				case "EASYROMS":
					q.Number = 7
				default:
					// moved behind userdata by the first start, until a later
					// start gives its space to userdata
					q.Number = 13
				}
				p.Table.Parts = append(p.Table.Parts, q)
				p.Protected = append(p.Protected, Range{q.Name, q.Offset(), q.Size()})
			case GuestPartition:
				// the game system stays exactly where it is, with its number and
				// identity (the console's record of it names them; RECORD-FORMAT)
				p.Table.Parts = append(p.Table.Parts, q)
				p.Protected = append(p.Protected, Range{q.Name, q.Offset(), q.Size()})
			case "BOOT", "system", "vendor", "metadata", "misc", "cache", "idbloader", "uboot", "trust":
			default:
				return nil, fmt.Errorf("unknown partition %q; refusing to overwrite this layout", q.Name)
			}
		}
		if guest, ok := card.Table.Find(GuestPartition); ok {
			for _, q := range p.Table.Parts {
				if q.Name != GuestPartition && q.Start <= guest.End && guest.Start <= q.End {
					return nil, ErrGuestInTheWay.New()
				}
			}
		}
		for i, q := range p.Table.Parts {
			if q.Name == "userdata" || q.Name == "EASYROMS" || q.Name == AutoAPK || q.Name == GuestPartition {
				continue
			}
			old, exists := card.Table.Find(q.Name)
			if exists {
				p.Table.Parts[i].ID = old.ID
			}
			if exists && (q.Name == "metadata" || q.Name == "misc") {
				if q.Start != old.Start || q.End != old.End {
					return nil, fmt.Errorf("%s would need to move", q.Name)
				}
				p.Protected = append(p.Protected, Range{q.Name, q.Offset(), q.Size()})
				continue
			}
			classic := im.Facts["bootloader_chain"] == "classic"
			if classic && (q.Name == "idbloader" || q.Name == "trust") {
				if !exists || q.Start != old.Start || q.End != old.End {
					return nil, fmt.Errorf("classic update cannot move %s", q.Name)
				}
				// A computer UPDATE retains the old behaviour: refresh the
				// image's vendor pieces on this recognized installation.
			}
			p.Writes = append(p.Writes, Write{Range: Range{q.Name, q.Offset(), q.Size()}, SourceOffset: q.Offset(), Boot: q.Name == "BOOT", Uboot: classic && q.Name == "uboot"})
		}
	}
	if mode == "update" && im.Facts["bootloader_chain"] == "classic" {
		// Vendor first stage and trust come before U-Boot proper. This is a
		// computer-side operation on a recognized card, with a recovery backup.
		writes := make([]Write, 0, len(p.Writes))
		for _, name := range []string{"idbloader", "trust", "uboot"} {
			for _, w := range p.Writes {
				if w.Name == name {
					writes = append(writes, w)
				}
			}
		}
		for _, w := range p.Writes {
			if w.Name != "idbloader" && w.Name != "trust" && w.Name != "uboot" {
				writes = append(writes, w)
			}
		}
		p.Writes = writes
	}
	// Windows only gets BOOT, EASYROMS and AUTOAPK (people may add their own APKs
	// before the first start); the rest would each pop up "format disk?"
	for i := range p.Table.Parts {
		if n := p.Table.Parts[i].Name; n != "BOOT" && n != "EASYROMS" && n != AutoAPK && !(mode == "update" && n == GuestPartition) {
			p.Table.Parts[i].Flags |= layout.NoDriveLetter
		}
	}
	if e := p.Table.Validate(true); e != nil {
		return nil, fmt.Errorf("cannot preserve this layout: %w", e)
	}
	head, tail, e := p.Table.Encode()
	if e != nil {
		return nil, e
	}
	if mode == "install" {
		p.Probes = capacityProbes(size)
	}
	// Commit the backup table before the primary table, after all partition payloads.
	p.Writes = append(p.Writes, Write{Range: Range{"Backup partition table", size - int64(len(tail)), int64(len(tail))}, Data: tail}, Write{Range: Range{"Partition table", 0, int64(len(head))}, Data: head})
	if e = p.Validate(); e != nil {
		return nil, e
	}
	return p, nil
}

// UseSystemImage writes a file from firmware.PrepareSystem to the system partition instead
// of the release's system. Everything else in the plan stays as it is.
func (p *Plan) UseSystemImage(path string) error {
	part, ok := p.Table.Find("system")
	if !ok {
		return errors.New("no system partition")
	}
	st, e := os.Stat(path)
	if e != nil {
		return e
	}
	if st.Size() != part.Size() {
		return errors.New("the prepared system image does not match the system partition")
	}
	sys := Write{Range: Range{"System image", part.Offset(), part.Size()}, System: true}
	var out []Write
	for _, w := range p.Writes {
		switch {
		case w.Data != nil || w.Boot || w.Zero || w.Guest || w.End() <= sys.Offset || w.Offset >= sys.End():
			out = append(out, w)
		case w.Offset == sys.Offset && w.Length == sys.Length:
			out = append(out, sys)
		case w.Offset < sys.Offset && w.End() > sys.End():
			// The whole release image: write it around the system partition.
			before, after := w, w
			before.Length = sys.Offset - w.Offset
			after.Offset, after.Length = sys.End(), w.End()-sys.End()
			after.SourceOffset = w.SourceOffset + (sys.End() - w.Offset)
			out = append(out, before, sys, after)
		default:
			return fmt.Errorf("%s partly overlaps the system partition", w.Name)
		}
	}
	found := false
	for _, w := range out {
		found = found || w.System
	}
	if !found {
		return errors.New("this plan does not write the system partition")
	}
	p.Writes, p.SystemImage = out, path
	return p.Validate()
}
func (p *Plan) Validate() error {
	if p.Size < 68*512 {
		return errors.New("invalid target size")
	}
	for _, w := range p.Writes {
		if w.Uboot && (p.Mode != "update" || w.Offset != firmware.BootloaderOffset || w.Length != firmware.BootloaderSize || w.SourceOffset != w.Offset || w.Boot || w.System || w.Zero || w.Guest || w.Data != nil) {
			return errors.New("invalid classic U-Boot copy write")
		}
		if w.Offset < 0 || w.Length <= 0 || w.Offset > p.Size || w.Length > p.Size-w.Offset {
			return errors.New("write outside target")
		}
		if w.Length%512 != 0 || w.Offset%512 != 0 {
			return errors.New("unaligned write")
		}
		for _, keep := range p.Protected {
			if w.Offset < keep.End() && keep.Offset < w.End() {
				return fmt.Errorf("write to %s would damage %s", w.Name, keep.Name)
			}
		}
	}
	for _, r := range p.Probes {
		if p.Mode != "install" || r.Offset < 0 || r.Length <= 0 || r.Offset > p.Size || r.Length > p.Size-r.Offset || r.Offset%512 != 0 || r.Length%512 != 0 {
			return errors.New("invalid card size check")
		}
		for _, keep := range p.Protected {
			if r.Offset < keep.End() && keep.Offset < r.End() {
				return fmt.Errorf("the card size check would damage %s", keep.Name)
			}
		}
	}
	return nil
}
