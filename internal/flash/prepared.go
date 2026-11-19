package flash

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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

// Guest is a game system release image (dArkOS, dArkOSRE or dArkOSen) the flasher
// puts on the card: the facts of the release's manifest.json and the unpacked image
// file.
type Guest struct {
	Path string `json:"path"`
	// Distribution is the system ("dArkOS", "dArkOSRE", "dArkOSen"); "" is dArkOSen.
	Distribution   string `json:"distribution,omitempty"`
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
			return p, errors.New("this card is too small for the game system")
		}
		p.Guest = Span{start, last}
		last = start - 1
	}
	free := last - preparedStart + 1
	if free <= 2*apps || (guestBytes > 0 && free*512 < MinPreparedAndroid) {
		if guestBytes > 0 {
			return p, errors.New("this card is too small for Android and the game system together")
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
		if e := GuestFits(im, g); e != nil {
			return e
		}
		if g.Size <= 0 || g.Size%512 != 0 || g.PartitionBytes < g.Size || len(g.SHA256) != 64 || g.Path == "" ||
			g.Name == "" || strings.ContainsAny(g.Name, "/\\") || g.DistributionName() == "" {
			return errors.New("invalid game system release")
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
	// "do not automount", as the console's own resize sets it (sgdisk attribute 63)
	p.Table.Parts[len(p.Table.Parts)-1].Flags = 1 << 63
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
		p.Guest, p.GuestDistribution = g, ReadsDistribution(im)
		p.Writes = append(p.Writes, Write{Range: Range{g.DistributionName(), parts.Guest.First * 512, g.Size}, Guest: true})
	}
	return nil
}

// BootFiles are the files that tell the console what the flasher did: the layout it
// made, and the game system release it put on the card (GuestFile, read by the
// console's erofs support tool: "partition-pin" in PortMaster/erofs-tools). The record
// names the system in "distribution" (RECORD-FORMAT in the subsystem reports); a
// console reads a record without it as dArkOSen. Consoles from before the field reject
// it, so for a release older than MinimumDistributionBuild it is left out, which only
// dArkOSen may be put on such a card with (GuestFits).
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
		distribution := g.DistributionName()
		if distribution == "" {
			return nil, errors.New("invalid game system release")
		}
		if !p.GuestDistribution {
			if distribution != "dArkOSen" {
				return nil, ErrGuestNeedsNewerRelease
			}
			distribution = ""
		}
		j, e := json.Marshal(struct {
			Format       int    `json:"format"`
			Distribution string `json:"distribution,omitempty"`
			Darkosen     string `json:"darkosen"`
			Revision     int    `json:"revision"`
			Minimum      string `json:"minimum_andr36oid"`
			Image        image  `json:"image"`
		}{1, distribution, g.Darkosen, g.Revision, g.Minimum, image{g.Name, g.Size, g.SHA256}})
		if e != nil {
			return nil, e
		}
		files[GuestFile] = append(j, '\n')
	}
	return files, nil
}

// guestDistributions are the systems a card can carry, as the console names them
// (it compares them ignoring case).
var guestDistributions = []string{"dArkOS", "dArkOSRE", "dArkOSen"}

// DistributionName is the system's name in its usual spelling: dArkOSen when the
// release doesn't say, "" for a name the console doesn't know.
func (g *Guest) DistributionName() string {
	if g.Distribution == "" {
		return "dArkOSen"
	}
	for _, d := range guestDistributions {
		if strings.EqualFold(d, g.Distribution) {
			return d
		}
	}
	return ""
}

func serial() uint32 {
	var b [4]byte
	rand.Read(b[:])
	return binary.LittleEndian.Uint32(b[:])
}

// MinimumDistributionBuild is the first andr36oid build (YYYYMMDD or YYYYMMDD.HHMMSS,
// as in minimum_andr36oid) whose console reads "distribution" in GuestFile and so can
// run dArkOS and dArkOSRE from the partition. Older consoles reject the field.
//
// It is candidate 11 (eng.kenny.20261119.021722, the tester build of 19 November
// 2026): its erofs-tools reader is the first that takes the field. For older builds
// (rc1-beta1 = candidate 9 among them) only dArkOSen, written without the field as
// before, is offered. Set to "" and no release counts as new enough.
const MinimumDistributionBuild = "20261119.021722"

// ErrGuestNeedsNewerRelease: the system can't go on a card with this andr36oid
// release, which is too old to run it.
var ErrGuestNeedsNewerRelease = errors.New("this game system needs a newer andr36oid release")

var buildStamp = regexp.MustCompile(`(\d{8})\.(\d{6})`)
var minimumStamp = regexp.MustCompile(`^(\d{8})(?:\.(\d{6}))?$`)

// buildAtLeast compares the image's build stamp with a minimum. The build stamp is
// the YYYYMMDD.HHMMSS in the image's build identity (andr36oid version and Android
// incremental, e.g. "...|eng.kenny.20261117.101500"); the minimum is YYYYMMDD
// (taken as YYYYMMDD.000000), YYYYMMDD.HHMMSS, or an incremental or identity with a
// YYYYMMDD.HHMMSS in it. Both are compared as the 14 digit
// strings date+time, which sort like the times they are. ok is false when either
// has no stamp.
func buildAtLeast(im *firmware.Image, minimum string) (newer, ok bool) {
	want := minimumStamp.FindStringSubmatch(minimum)
	if want == nil {
		// the console also takes an eng.NAME.YYYYMMDD.HHMMSS incremental, or an
		// identity with one in it, as the minimum (RECORD-FORMAT)
		want = buildStamp.FindStringSubmatch(minimum)
	}
	have := buildStamp.FindStringSubmatch(im.BuildIdentity)
	if want == nil || have == nil {
		return false, false
	}
	if want[2] == "" {
		want[2] = "000000"
	}
	return have[1]+have[2] >= want[1]+want[2], true
}

// ReadsDistribution says whether the console of this release reads the
// "distribution" field (MinimumDistributionBuild).
func ReadsDistribution(im *firmware.Image) bool {
	return readsDistribution(im, MinimumDistributionBuild)
}

func readsDistribution(im *firmware.Image, minimum string) bool {
	if minimum == "" {
		return false
	}
	newer, ok := buildAtLeast(im, minimum)
	return ok && newer
}

// GuestFits says whether a game system release may go on a card with this andr36oid
// release: the image has to say it runs a game system from a partition, be at least
// the build the release asks for (YYYYMMDD or YYYYMMDD.HHMMSS, "" for any), and for
// any system but dArkOSen read the "distribution" field (ReadsDistribution). The
// flasher offers a system only when this is nil; ErrGuestNeedsNewerRelease (wrapped)
// is the "release too old" answer.
func GuestFits(im *firmware.Image, g *Guest) error {
	if !im.Subsystem() {
		return errors.New("this release cannot run a game system from its own partition")
	}
	if g.DistributionName() != "dArkOSen" && !ReadsDistribution(im) {
		return ErrGuestNeedsNewerRelease
	}
	if g.Minimum == "" {
		return nil
	}
	newer, ok := buildAtLeast(im, g.Minimum)
	if !ok {
		return errors.New("this game system release needs a newer flasher or a newer andr36oid release")
	}
	if !newer {
		return fmt.Errorf("%w (andr36oid %s or newer)", ErrGuestNeedsNewerRelease, g.Minimum)
	}
	return nil
}

// PreparedSizes is what a prepared card gives Android (once the bundled apps are
// installed and their space is Android's) and the games partition, for display.
// prepared is false for a release the flasher doesn't partition the card for.
func PreparedSizes(im *firmware.Image, size int64, wantGiB int, noROMs bool, guestBytes int64) (android, games int64, prepared bool, err error) {
	apps, ok := canPrepare(im)
	if !ok {
		return 0, 0, false, nil
	}
	p, e := PreparedLayout(size, apps.Size(), wantGiB, noROMs, guestBytes)
	if e != nil {
		return 0, 0, true, e
	}
	return p.Android(), p.ROMs.Bytes(), true, nil
}

// MaxPreparedGiB is MaxUserdataGiB for a prepared card.
func MaxPreparedGiB(im *firmware.Image, size, guestBytes int64) int {
	for g := int(size / gib); g >= MinUserdataGiB; g-- {
		if _, games, ok, e := PreparedSizes(im, size, g, false, guestBytes); ok && e == nil && games > 0 {
			return g
		}
	}
	return 0
}
