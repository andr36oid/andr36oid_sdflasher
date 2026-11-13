package firmware

import (
	"bytes"
	"context"
	"crypto/rc4"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
)

var bootOptionID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type BootOptionsRecord struct {
	Chain      string            `json:"chain"`
	Bootloader string            `json:"bootloader"`
	Sleep      string            `json:"sleep"`
	Assets     map[string]string `json:"assets"`
	Tags       map[string]string `json:"tags,omitempty"`
	Time       string            `json:"time"`
}
type BootSlot struct {
	Offset int64
	Size   int64
	Name   string
}
type BootPlan struct {
	Slots  []BootSlot
	Data   [][]byte
	Record BootOptionsRecord
}

// Slot locations always come from the inspected GPT. The first-stage area is
// bounded by the U-Boot start; GPT sectors 0..63 are never part of that area.
func bootSlots(t layout.Table) (map[string]BootSlot, error) {
	out := map[string]BootSlot{}
	for _, name := range []string{"idbloader", "uboot", "trust"} {
		p, ok := t.Find(name)
		if !ok {
			return nil, fmt.Errorf("card has no %s slot", name)
		}
		out[name] = BootSlot{p.Offset(), p.Size(), name}
	}
	idb, u, tr := out["idbloader"], out["uboot"], out["trust"]
	boot, bootOK := t.Find("BOOT")
	if idb.Offset != 64*layout.Sector || u.Offset != BootloaderOffset || u.Size != BootloaderSize || idb.Offset+idb.Size > u.Offset || tr.Size <= 0 || tr.Size > 16<<20 || !bootOK || tr.Offset != u.Offset+u.Size || tr.Offset+tr.Size > boot.Offset() {
		return nil, errors.New("unsupported boot chain slot layout")
	}
	for _, p := range t.Parts {
		for _, s := range out {
			if p.Name != s.Name && p.Offset() < s.Offset+s.Size && p.Offset()+p.Size() > s.Offset {
				return nil, errors.New("boot slots overlap another partition")
			}
		}
	}
	return out, nil
}
func CheckIDB(b []byte) error {
	if len(b) < 2048 || len(b)%512 != 0 {
		return errors.New("invalid first-stage loader size")
	}
	h := make([]byte, 512)
	c, _ := rc4.NewCipher([]byte{124, 78, 3, 4, 85, 5, 9, 7, 45, 44, 123, 56, 23, 13, 23, 17})
	c.XORKeyStream(h, b[:512])
	start := int(binary.LittleEndian.Uint16(h[12:])) * 512
	init := int(binary.LittleEndian.Uint16(h[506:])) * 512
	total := int(binary.LittleEndian.Uint16(h[508:])) * 512
	if binary.LittleEndian.Uint32(h) != 0x0ff0aa55 || binary.LittleEndian.Uint32(h[8:]) != 1 || start < 512 || start+4 > len(b) || string(b[start:start+4]) != "RK33" || init <= 0 || total < init || start+total > len(b) {
		return errors.New("invalid RK33/rksd first-stage header")
	}
	return nil
}
func CheckTrust(b []byte) error {
	// BL3X header/component bounds follow tools/rockchip/trust_merger.h.
	if len(b) < 2048 || len(b)%512 != 0 || string(b[:4]) != "BL3X" {
		return errors.New("invalid sleep firmware header")
	}
	size := binary.LittleEndian.Uint32(b[12:])
	count := int(size >> 16)
	sign := int(size&0xffff) * 4
	if count < 1 || count > 16 || 800+count*48 > 2048 || sign < 800+count*48 || sign+256+count*16 > 2048 {
		return errors.New("invalid sleep firmware component table")
	}
	for n := 0; n < count; n++ {
		o := sign + 256 + n*16
		start := uint64(binary.LittleEndian.Uint32(b[o+4:])) * 512
		size := uint64(binary.LittleEndian.Uint32(b[o+8:])) * 512
		if start < 2048 || size == 0 || start+size > uint64(len(b)) {
			return errors.New("sleep firmware component outside image")
		}
	}
	return nil
}
func CheckBootFIT(b []byte) error {
	if len(b) < 40 || len(b) > int(BootloaderSize) || binary.BigEndian.Uint32(b) != 0xd00dfeed {
		return errors.New("invalid FIT header")
	}
	total := uint64(binary.BigEndian.Uint32(b[4:]))
	s := uint64(binary.BigEndian.Uint32(b[8:]))
	str := uint64(binary.BigEndian.Uint32(b[12:]))
	strSize := uint64(binary.BigEndian.Uint32(b[32:]))
	sSize := uint64(binary.BigEndian.Uint32(b[36:]))
	if total < 40 || total > uint64(len(b)) || s < 40 || s+sSize > total || str+strSize > total || binary.BigEndian.Uint32(b[20:]) < 16 {
		return errors.New("invalid FIT bounds")
	}
	// Parse the flattened tree, including externally stored payloads. Structural
	// bounds alone would accept a truncated release with a valid FDT prefix.
	end := int(s + sSize)
	pos := int(s)
	depth := 0
	images := false
	configs := false
	payloads := 0
	dataPos, dataSize := uint64(0), uint64(0)
	for pos+4 <= end {
		token := binary.BigEndian.Uint32(b[pos:])
		pos += 4
		switch token {
		case 1:
			n := bytes.IndexByte(b[pos:end], 0)
			if n < 0 {
				return errors.New("invalid FIT node")
			}
			name := string(b[pos : pos+n])
			if name == "images" {
				images = true
			}
			if name == "configurations" {
				configs = true
			}
			pos = (pos + n + 1 + 3) &^ 3
			depth++
		case 2:
			depth--
			if depth < 0 {
				return errors.New("invalid FIT nesting")
			}
			if dataSize > 0 {
				if dataPos+dataSize > uint64(len(b)) {
					return errors.New("FIT payload outside image")
				}
				payloads++
				dataSize = 0
				dataPos = 0
			}
		case 3:
			if pos+8 > end {
				return errors.New("invalid FIT property")
			}
			n := uint64(binary.BigEndian.Uint32(b[pos:]))
			no := uint64(binary.BigEndian.Uint32(b[pos+4:]))
			pos += 8
			if uint64(pos)+n > uint64(end) || no >= strSize {
				return errors.New("invalid FIT property bounds")
			}
			names := b[str+no : str+strSize]
			z := bytes.IndexByte(names, 0)
			if z < 0 {
				return errors.New("invalid FIT property name")
			}
			name := string(names[:z])
			v := b[pos : uint64(pos)+n]
			switch name {
			case "data":
				if n > 0 {
					payloads++
				}
			case "data-position":
				if n != 4 {
					return errors.New("invalid FIT position")
				}
				dataPos = uint64(binary.BigEndian.Uint32(v))
			case "data-offset":
				if n != 4 {
					return errors.New("invalid FIT offset")
				}
				dataPos = (total + 3) &^ 3
				dataPos += uint64(binary.BigEndian.Uint32(v))
			case "data-size":
				if n != 4 {
					return errors.New("invalid FIT size")
				}
				dataSize = uint64(binary.BigEndian.Uint32(v))
			}
			pos = (pos + int(n) + 3) &^ 3
		case 4:
		case 9:
			if depth == 0 && images && configs && payloads > 0 {
				return nil
			}
			return errors.New("incomplete FIT tree")
		default:
			return errors.New("invalid FIT token")
		}
	}
	return errors.New("unterminated FIT tree")
}
func bootCheck(name string, b []byte) error {
	switch name {
	case "idbloader":
		return CheckIDB(b)
	case "trust":
		return CheckTrust(b)
	case "uboot":
		if len(b) >= 4 && binary.BigEndian.Uint32(b) == 0xd00dfeed {
			return CheckBootFIT(b)
		}
		return CheckBootloaderContainer(b)
	}
	return errors.New("unknown boot slot")
}
func readImageBootSlot(im *Image, s BootSlot) ([]byte, error) {
	f, e := os.Open(im.Path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b := make([]byte, s.Size)
	if _, e = f.ReadAt(b, s.Offset); e != nil {
		return nil, e
	}
	return b, nil
}

func ResolveBootOptions(ctx context.Context, c BootloaderChoice, im *Image, t BootTarget, dir string) ([]byte, *BootloaderOutcome, error) {
	if c.Chain == "" && c.Sleep == "" && t.CurrentChain != "mainline" && (t.CurrentSleep == "" || t.CurrentSleep == "image") {
		issues, e := CheckBootOptions(c, t, BootRules())
		if e != nil {
			return nil, nil, e
		}
		b, o, e := ResolveBootloader(ctx, c, im, dir)
		if o != nil {
			o.Issues = issues
		}
		return b, o, e
	}
	var cat *BootCatalog
	if c.SleepID() != "image" {
		var e error
		cat, e = FetchBootCatalog(ctx)
		if e != nil {
			return nil, nil, e
		}
	}
	issues, e := CheckBootOptions(c, t, CatalogBootRules(cat))
	if e != nil {
		return nil, nil, e
	}
	out := &BootloaderOutcome{Issues: issues}
	// A standard-chain restore must include all three image slots. Fresh writes
	// already copy those slots, so default fresh flashing uses the old path above.
	if c.ChainID() == "standard" && im == nil && (t.CurrentChain == "mainline" || c.LoaderID() == "image" || c.Sleep == "image") {
		return nil, nil, errors.New(BootloaderNeedImage)
	}
	plan := &BootPlan{Record: BootOptionsRecord{Chain: c.ChainID(), Bootloader: c.LoaderID(), Sleep: c.SleepID(), Assets: map[string]string{}, Tags: map[string]string{}, Time: setupNow().UTC().Format(time.RFC3339Nano)}}
	// Resolve sizes from the source image where available; existing-card target
	// geometry is checked again before any byte is written.
	var slots map[string]BootSlot
	if im != nil {
		slots, e = bootSlots(im.Table)
		if e != nil {
			return nil, nil, e
		}
	}
	add := func(name string, b []byte, tag string) error {
		if e := bootCheck(name, b); e != nil {
			return e
		}
		s := BootSlot{Name: name}
		if slots != nil {
			s = slots[name]
			if int64(len(b)) > s.Size {
				return errors.New("firmware exceeds image slot")
			}
		}
		plan.Slots = append(plan.Slots, s)
		plan.Data = append(plan.Data, b)
		h := sha256.Sum256(b)
		plan.Record.Assets[name] = hex.EncodeToString(h[:])
		if tag != "" {
			plan.Record.Tags[name] = tag
		}
		return nil
	}
	if c.ChainID() == "mainline" {
		a, e := releaseBootAssets(ctx, NewBootloaderRepo, map[string]int64{"idbloader.img": BootloaderOffset - 64*layout.Sector, "u-boot.itb": BootloaderSize})
		if e != nil {
			return nil, nil, e
		}
		for _, v := range []struct {
			name, asset string
			check       func([]byte) error
		}{{"uboot", "u-boot.itb", CheckBootFIT}, {"idbloader", "idbloader.img", CheckIDB}} {
			b, e := downloadBootAsset(ctx, a[v.asset], dir, v.check)
			if e != nil {
				return nil, nil, e
			}
			if e = add(v.name, b, a[v.asset].tag); e != nil {
				return nil, nil, e
			}
		}
	} else {
		if im != nil && (t.CurrentChain == "mainline" || c.Chain != "" || c.Sleep == "image" || (t.CurrentSleep != "" && t.CurrentSleep != "image")) {
			for _, name := range []string{"idbloader", "trust"} {
				b, e := readImageBootSlot(im, slots[name])
				if e != nil {
					return nil, nil, e
				}
				if e = add(name, b, ""); e != nil {
					return nil, nil, e
				}
			}
		}
		b, o, e := ResolveBootloader(ctx, c, im, dir)
		if e != nil {
			return nil, nil, e
		}
		if b == nil {
			return nil, o, nil
		}
		if o.Warning != "" {
			return nil, nil, errors.New(o.Warning)
		}
		out.Record = o.Record
		if e = add("uboot", b, ""); e != nil {
			return nil, nil, e
		}
		if c.SleepID() != "image" {
			var entry *BootFirmwareEntry
			for i := range cat.Entries {
				if cat.Entries[i].ID == c.SleepID() {
					entry = &cat.Entries[i]
				}
			}
			if entry == nil {
				return nil, nil, errors.New("sleep firmware is not available")
			}
			max := int64(16 << 20)
			if slots != nil {
				max = slots["trust"].Size
			}
			a, e := releaseBootAssetsAt(ctx, entry.Repository, entry.Tag, map[string]int64{entry.Asset: max})
			if e != nil {
				return nil, nil, e
			}
			b, e := downloadBootAsset(ctx, a[entry.Asset], dir, CheckTrust)
			if e != nil {
				return nil, nil, e
			}
			// Replace any image-restore trust entry, so it is written only once.
			for i, s := range plan.Slots {
				if s.Name == "trust" {
					plan.Slots = append(plan.Slots[:i], plan.Slots[i+1:]...)
					plan.Data = append(plan.Data[:i], plan.Data[i+1:]...)
					break
				}
			}
			if e = add("trust", b, a[entry.Asset].tag); e != nil {
				return nil, nil, e
			}
		}
	}
	out.plan = plan
	out.Options = &plan.Record
	// Non-nil is the existing pipeline's indication that an override is ready.
	return []byte{1}, out, nil
}
func ApplyResolvedBootOptions(d HotfixDisk, size int64, data []byte, out *BootloaderOutcome, expected, dir string) error {
	if out.plan == nil {
		return ApplyBootloader(d, size, data, *out.Record, expected, dir)
	}
	st, e := stageSetup(d, size, expected, dir)
	if e != nil {
		return e
	}
	defer st.Close()
	entries, e := st.boot.ReadDir(".")
	if e != nil {
		return e
	}
	for _, v := range entries {
		if strings.EqualFold(v.Name(), "andr36oid-datacard.txt") {
			return errors.New(BootloaderOnlySD)
		}
	}
	slots, e := bootSlots(st.tab)
	if e != nil {
		return e
	}
	for i, s := range out.plan.Slots {
		target := slots[s.Name]
		if int64(len(out.plan.Data[i])) > target.Size {
			return errors.New("firmware exceeds card slot")
		}
		if e = bootCheck(s.Name, out.plan.Data[i]); e != nil {
			return e
		}
		h := sha256.Sum256(out.plan.Data[i])
		if hex.EncodeToString(h[:]) != out.plan.Record.Assets[s.Name] {
			return errors.New("boot options payload changed")
		}
	}
	setup, e := ReadCardSetup(st.boot)
	if e != nil {
		return e
	}
	if setup.BootOptions != nil {
		setup.BootOptionsHistory = append(setup.BootOptionsHistory, *setup.BootOptions)
	}
	if len(setup.BootOptionsHistory) > 5 {
		setup.BootOptionsHistory = setup.BootOptionsHistory[len(setup.BootOptionsHistory)-5:]
	}
	setup.BootOptions = &out.plan.Record
	if out.Record != nil {
		if setup.Bootloader != nil {
			setup.BootloaderHistory = append(setup.BootloaderHistory, *setup.Bootloader)
		}
		if len(setup.BootloaderHistory) > 5 {
			setup.BootloaderHistory = setup.BootloaderHistory[len(setup.BootloaderHistory)-5:]
		}
		setup.Bootloader = out.Record
	}
	if e = st.Save(setup); e != nil {
		return e
	}
	// Mainline: install and sync its FIT before changing the first-stage loader.
	// Standard restore: restore trust/loader before switching its first stage.
	order := []string{"trust", "uboot", "idbloader"}
	for _, name := range order {
		for i, s := range out.plan.Slots {
			if s.Name != name {
				continue
			}
			target := slots[name]
			b := make([]byte, target.Size)
			copy(b, out.plan.Data[i])
			if name == "uboot" && CheckBootloaderContainer(b) == nil {
				e = WriteBootloaderSlot(d, b, nil)
			} else {
				e = writeBootSlot(d, target, b)
			}
			if e != nil {
				return e
			}
		}
	}
	return st.Commit()
}
func writeBootSlot(d HotfixDisk, s BootSlot, b []byte) error {
	if int64(len(b)) != s.Size {
		return errors.New("boot slot size differs")
	}
	n, e := d.WriteAt(b, s.Offset)
	if e != nil {
		return e
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	if e = d.Sync(); e != nil {
		return e
	}
	got := make([]byte, len(b))
	if _, e = d.ReadAt(got, s.Offset); e != nil {
		return e
	}
	if !bytes.Equal(got, b) {
		return errors.New("boot firmware readback differs")
	}
	return nil
}
func CardBootTarget(d io.ReaderAt, size int64, im *Image) (BootTarget, error) {
	_, tab, e := CardBuildIdentity(d, size)
	if e != nil {
		return BootTarget{}, e
	}
	b, e := OpenBoot(d, tab, true)
	if e != nil {
		return BootTarget{}, e
	}
	s, e := ReadCardSetup(b)
	if e != nil {
		return BootTarget{}, e
	}
	t := ImageBootTarget(im, s.Profile)
	t.KernelLine = s.KernelLine
	if s.BootOptions != nil {
		t.CurrentChain = s.BootOptions.Chain
		t.CurrentSleep = s.BootOptions.Sleep
	}
	if im == nil && s.Profile != "" {
		raw, e := ReadFile(b, "/"+ProfileList, 1<<20)
		if e == nil {
			if p, ok := parseProfileList(raw)[s.Profile]; ok {
				t.Console = p.Console
			}
		}
	}

	if slot, ok := tab.Find("uboot"); ok {
		var magic [4]byte
		if _, e := d.ReadAt(magic[:], slot.Offset()); e != nil {
			return t, e
		}
		if binary.BigEndian.Uint32(magic[:]) == 0xd00dfeed {
			t.CurrentChain = "mainline"
		}
	}
	return t, nil
}
