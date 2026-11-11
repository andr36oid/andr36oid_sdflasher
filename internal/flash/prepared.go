package flash

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/andr36oid/andr36oid-sdflasher/internal/exfat"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
)

// A prepared card is partitioned by the flasher the way the console's first start
// would leave it (firstresize/andr36oid-firstresize.sh, plan_userdata and apps_move, in
// device/gameconsole/common): userdata, the bundled apps right behind it, the games
// partition formatted, and dArkOSen in its own partition at the end of the card when
// the user wants it. The first start then has nothing to move, so it is short, and
// games can be copied to the card straight after flashing. Only releases that say
// layout=prepared-1 in andr36oid-build.txt get one. Keep the arithmetic in step with
// the console's.

const (
	mib        = int64(2048) // sectors
	gibSectors = int64(2097152)
	// MinPreparedAndroid is what Android must get on a card that also holds dArkOSen.
	MinPreparedAndroid = 8 * gib
	GuestPartition     = "DARKOS"
	LayoutFile         = "andr36oid-layout.txt"
	GuestFile          = "andr36oid-darkosen.json"
)

// Guest is a dArkOSen release image the flasher puts on the card: the facts of the
// release's manifest.json and the unpacked image file.
type Guest struct {
	Path           string `json:"path"`
	Darkosen       string `json:"darkosen"`
	Revision       int    `json:"revision"`
	Minimum        string `json:"minimum_andr36oid"`
	Name           string `json:"name"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
	PartitionBytes int64  `json:"partition_bytes"`
}

// Options are the choices of an installation beyond Build's own arguments.
type Options struct {
	Guest *Guest
}

// Span is a run of sectors, both ends included; the zero Span is "not there".
type Span struct{ First, Last int64 }

func (s Span) Bytes() int64 {
	if s.Last < s.First || s.First == 0 {
		return 0
	}
	return (s.Last - s.First + 1) * 512
}

// userdata's first sector in releases with the aligned layout (a variable for the tests'
// small images)
var preparedStart = int64(5701632)

// PreparedParts is where everything behind system goes on a prepared card.
type PreparedParts struct {
	Userdata, Apps, ROMs, Guest Span
}

// Android is what Android has once its first start gave it the bundled apps' space.
func (p PreparedParts) Android() int64 { return p.Userdata.Bytes() + p.Apps.Bytes() }

// PreparedLayout places the partitions on a card of size bytes. appsBytes is the size of
// the image's bundled apps partition, guestBytes the size of the dArkOSen partition (0:
// none). wantGiB and noROMs mean what they mean for UserdataLayout; the rules for
// Android's share apply to what dArkOSen leaves.
func PreparedLayout(size, appsBytes int64, wantGiB int, noROMs bool, guestBytes int64) (PreparedParts, error) {
	var p PreparedParts
	last := size/512 - 34
	apps := (appsBytes + 511) / 512
	if guestBytes > 0 {
		start := (last + 1 - (guestBytes+511)/512) / mib * mib
		if start <= preparedStart {
			return p, errors.New("this card is too small for dArkOSen")
		}
		p.Guest = Span{start, last}
		last = start - 1
	}
	free := last - preparedStart + 1
	if free <= 2*apps || (guestBytes > 0 && free*512 < MinPreparedAndroid) {
		if guestBytes > 0 {
			return p, errors.New("this card is too small for Android and dArkOSen together")
		}
		return p, errors.New("the card is too small")
	}
	end := last
	if !noROMs {
		want := int64(wantGiB) * gibSectors
		if wantGiB <= 0 {
			want = min(free/2, 64*gibSectors)
		}
		want = max(want, MinUserdataGiB*gibSectors)
		end = (preparedStart+want)/mib*mib - 1
		if last-end < gibSectors {
			end = last
		}
	}
	appsStart := (end + 1 - apps) / mib * mib
	if appsStart <= preparedStart+apps {
		return p, errors.New("the card is too small")
	}
	p.Userdata = Span{preparedStart, appsStart - 1}
	p.Apps = Span{appsStart, end}
	if end < last {
		p.ROMs = Span{end + 1, last}
	}
	return p, nil
}

// canPrepare: the image says so and has the bundled apps where userdata goes.
func canPrepare(im *firmware.Image) (layout.Partition, bool) {
	apps, ok := im.Table.Find(AutoAPK)
	return apps, ok && im.Prepared() && int64(apps.Start) == preparedStart
}

// prepare fills an install plan for a prepared card: p.Table has the image's partitions
// without the bundled apps.
func (p *Plan) prepare(im *firmware.Image, apps layout.Partition, wantGiB int, noROMs bool, g *Guest) error {
	var guestBytes int64
	if g != nil {
		if !im.Subsystem() {
			return errors.New("this release cannot run dArkOSen from its own partition")
		}
		if g.Size <= 0 || g.Size%512 != 0 || g.PartitionBytes < g.Size || len(g.SHA256) != 64 || g.Path == "" ||
			g.Name == "" || strings.ContainsAny(g.Name, "/\\") {
			return errors.New("invalid dArkOSen release")
		}
		guestBytes = g.PartitionBytes
	}
	parts, e := PreparedLayout(p.Size, apps.Size(), wantGiB, noROMs, guestBytes)
	if e != nil {
		return e
	}
	if !noROMs && wantGiB > 0 && parts.ROMs.Bytes() == 0 {
		return errors.New("this card has no room for a separate games partition with that much Android storage; choose less, or all storage for Android")
	}
	p.Prepared, p.NoROMs = true, parts.ROMs.Bytes() == 0
	p.UserdataGiB = 0
	add := func(number int, name string, s Span, kind [16]byte) {
		p.Table.Parts = append(p.Table.Parts, layout.Partition{Number: number, Name: name, Start: uint64(s.First), End: uint64(s.Last), Type: kind, ID: layout.GUID()})
	}
	add(6, "userdata", parts.Userdata, layout.LinuxType)
	add(13, AutoAPK, parts.Apps, apps.Type)
	// Everything of the image up to the bundled apps, then the apps at their new place
	p.Writes = append(p.Writes,
		Write{Range: Range{"Release image", 34 * 512, apps.Offset() - 34*512}, SourceOffset: 34 * 512},
		Write{Range: Range{"Bundled apps", parts.Apps.First * 512, apps.Size()}, SourceOffset: apps.Offset()},
		// Android formats a userdata whose start is empty
		Write{Range: Range{"Android storage", parts.Userdata.First * 512, 1 << 20}, Zero: true})
	if parts.ROMs.Bytes() > 0 {
		add(7, "EASYROMS", parts.ROMs, apps.Type)
		chunks, e := exfat.Format(parts.ROMs.Bytes(), parts.ROMs.First*512, "EASYROMS", serial())
		if e != nil {
			return e
		}
		for _, c := range chunks {
			// The few bytes that say something as data, the empty rest as zeros
			used := len(c.Data)
			for used > 0 && c.Data[used-1] == 0 {
				used--
			}
			used = (used + 4095) / 4096 * 4096
			used = min(used, len(c.Data))
			off := parts.ROMs.First*512 + c.Offset
			if used > 0 {
				p.Writes = append(p.Writes, Write{Range: Range{"Games partition", off, int64(used)}, Data: c.Data[:used]})
			}
			if rest := int64(len(c.Data) - used); rest > 0 {
				p.Writes = append(p.Writes, Write{Range: Range{"Games partition", off + int64(used), rest}, Zero: true})
			}
		}
	}
	if g != nil {
		add(14, GuestPartition, parts.Guest, layout.LinuxType)
		p.Guest = g
		p.Writes = append(p.Writes, Write{Range: Range{"dArkOSen", parts.Guest.First * 512, g.Size}, Guest: true})
	}
	return nil
}

// BootFiles are the files that tell the console what the flasher did: the layout it
// made, and the dArkOSen release it put on the card.
func (p *Plan) BootFiles() (map[string][]byte, error) {
	if !p.Prepared {
		return nil, nil
	}
	var b strings.Builder
	b.WriteString("prepared-1\n")
	for _, name := range []string{"userdata", AutoAPK, "EASYROMS", GuestPartition} {
		if q, ok := p.Table.Find(name); ok {
			fmt.Fprintf(&b, "%s %d %d\n", name, q.Start, q.End)
		}
	}
	files := map[string][]byte{LayoutFile: []byte(b.String())}
	if g := p.Guest; g != nil {
		type image struct {
			Name   string `json:"name"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		}
		j, e := json.Marshal(struct {
			Format   int    `json:"format"`
			Darkosen string `json:"darkosen"`
			Revision int    `json:"revision"`
			Minimum  string `json:"minimum_andr36oid"`
			Image    image  `json:"image"`
		}{1, g.Darkosen, g.Revision, g.Minimum, image{g.Name, g.Size, g.SHA256}})
		if e != nil {
			return nil, e
		}
		files[GuestFile] = append(j, '\n')
	}
	return files, nil
}

func serial() uint32 {
	var b [4]byte
	rand.Read(b[:])
	return binary.LittleEndian.Uint32(b[:])
}
