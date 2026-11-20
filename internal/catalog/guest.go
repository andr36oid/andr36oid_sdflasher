package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"github.com/xi2/xz"
)

// A game system ("guest") is a Linux distribution the console runs from its own
// partition at the end of the card. Each one is published as an EROFS image in its
// own repository, all with the same release layout: manifest.json, the packed
// image cut into .erofs.img.xz.NNN parts, and SHA256SUMS. The repositories' workflows
// convert each upstream release and verify every file.
//
// Which release is "the latest" of a source: the newest release on the first page
// of the list (GitHub lists the newest first) that is not a draft, whose tag has the
// form the source uses (<MMDDYYYY>-erofs<revision>, or <MMDDYYYY>-<device>-erofs<revision>
// for a source built per device), and that is a full release. A full release says
// the image works with andr36oid; pre-releases are still being tested and are not
// offered, unless the source sets Prereleases, and then only when it has no full
// release. No source sets it today.

// GuestSource is one repository of game system releases.
type GuestSource struct {
	// ID is the name used in options and in the manifest's "distribution".
	ID string
	// Display is the name the user sees.
	Display    string
	Repository string
	// Device is the tag's device part for sources built per device ("" for none).
	Device string
	// Prereleases lets a pre-release be offered when the source has no full release.
	Prereleases bool
}

const (
	// DefaultGuest is the system that is preselected.
	DefaultGuest = "dArkOS"
	// NoGuest is the choice to put no system on the card.
	NoGuest = "none"
)

// GuestSources are the systems the flasher can put on a card, in the order they are
// offered. dArkOS is built per device; RG351MP is the build for the R36S family.
var GuestSources = []GuestSource{
	{ID: "dArkOS", Display: "dArkOS", Repository: "andr36oid/dArkOS-erofs", Device: "RG351MP"},
	{ID: "dArkOSRE", Display: "dArkOSRE", Repository: "andr36oid/dArkOSRE-erofs"},
	{ID: "dArkOSen", Display: "dArkOSen", Repository: "andr36oid/dArkOSen-erofs"},
}

// FindGuestSource looks a source up by its ID, ignoring case. ok is false for an
// unknown ID and for NoGuest.
func FindGuestSource(id string) (GuestSource, bool) {
	for _, s := range GuestSources {
		if strings.EqualFold(s.ID, strings.TrimSpace(id)) {
			return s, true
		}
	}
	return GuestSource{}, false
}

// ParseGuestChoice reads a system choice: a source ID (any case), "none" or "" for
// none. ok is false for anything else.
func ParseGuestChoice(v string) (id string, ok bool) {
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, NoGuest) {
		return NoGuest, true
	}
	if s, found := FindGuestSource(v); found {
		return s.ID, true
	}
	return "", false
}

var guestTag = regexp.MustCompile(`^(\d{8})(?:-([A-Za-z0-9]+))?-erofs(\d+)$`)

// tagFits says whether a release tag has the form of src: the device part has to be
// src's device, or absent for a source without one.
func (src GuestSource) tagFits(tag string) bool {
	m := guestTag.FindStringSubmatch(tag)
	if m == nil {
		return false
	}
	if src.Device == "" {
		return m[2] == ""
	}
	return strings.EqualFold(m[2], src.Device)
}

// pickGuest is the release of the list that LatestGuest reads, nil for none.
func pickGuest(src GuestSource, list []release) *release {
	var pre *release
	for i := range list {
		r := &list[i]
		if r.Draft || !src.tagFits(r.Tag) {
			continue
		}
		if !r.Pre {
			return r
		}
		if pre == nil {
			pre = r
		}
	}
	if src.Prereleases {
		return pre
	}
	return nil
}

const guestManifest = "manifest.json"

// ErrGuestNeedsNewerFlasher: the system's newest release is in a form this flasher
// doesn't know yet.
var ErrGuestNeedsNewerFlasher = errors.New("this system's release needs a newer flasher")

// GuestPart is one file of the packed image.
type GuestPart struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// GuestRelease is manifest.json of a release (erofs/manifest.py in the repository
// documents it), with the release's download links.
type GuestRelease struct {
	Format int `json:"format"`
	// Distribution is the system, one of GuestSources' IDs; a manifest without it is
	// dArkOSen's (its releases came first).
	Distribution string `json:"distribution"`
	// Darkosen is the system's version (MMDDYYYY), under the name older flashers read.
	Darkosen string `json:"darkosen"`
	Revision int    `json:"revision"`
	Image    struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"image"`
	Download struct {
		Compression string      `json:"compression"`
		Size        int64       `json:"size"`
		SHA256      string      `json:"sha256"`
		Parts       []GuestPart `json:"parts"`
	} `json:"download"`
	PartitionBytes int64       `json:"partition_bytes"`
	Minimum        string      `json:"minimum_andr36oid"`
	Tag            string      `json:"-"`
	Source         GuestSource `json:"-"`
	assets         []Asset
}

func sha(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}

var (
	recordVersion   = regexp.MustCompile(`^[0-9]{8}$`)
	recordImageName = regexp.MustCompile(`^[A-Za-z0-9_.+-]{1,74}\.img$`)
)

func plainName(s string) bool {
	return s != "" && len(s) <= 128 && s == filepath.Base(s) && !strings.ContainsAny(s, "/\\\x00") && s != "." && s != ".."
}

// parseGuest checks a manifest: everything the flasher acts on has to be there and sane.
// A manifest without "distribution" is dArkOSen's; the name is put in the form of
// GuestSources' IDs.
func parseGuest(b []byte) (*GuestRelease, error) {
	var g GuestRelease
	if e := json.Unmarshal(b, &g); e != nil {
		return nil, e
	}
	if g.Distribution == "" {
		g.Distribution = "dArkOSen"
	}
	src, known := FindGuestSource(g.Distribution)
	if g.Format != 1 || g.Download.Compression != "xz" || !known {
		return nil, ErrGuestNeedsNewerFlasher
	}
	g.Distribution = src.ID
	bad := fmt.Errorf("invalid %s release", src.Display)
	// the console's partition reader rejects a BOOT record (the flasher copies these
	// fields into it) whose version isn't 8 digits, whose revision doesn't fit 32 bits,
	// whose image name isn't a short plain .img name or whose image is under 4 KiB
	// (RECORD-FORMAT in the subsystem reports)
	if !recordVersion.MatchString(g.Darkosen) || g.Revision < 1 || int64(g.Revision) > 4294967295 || !recordImageName.MatchString(g.Image.Name) || !sha(g.Image.SHA256) ||
		g.Image.Size < 4096 || g.Image.Size%512 != 0 || g.Image.Size > 64<<30 || g.PartitionBytes < g.Image.Size || g.PartitionBytes > 128<<30 ||
		len(g.Download.Parts) == 0 || len(g.Download.Parts) > 64 || !sha(g.Download.SHA256) {
		return nil, bad
	}
	var total int64
	seen := map[string]bool{}
	for _, p := range g.Download.Parts {
		if !plainName(p.Name) || seen[p.Name] || p.Size <= 0 || !sha(p.SHA256) {
			return nil, bad
		}
		seen[p.Name] = true
		total += p.Size
	}
	if total != g.Download.Size {
		return nil, bad
	}
	return &g, nil
}

// name is the system's name for messages.
func (g *GuestRelease) name() string {
	if g.Source.Display != "" {
		return g.Source.Display
	}
	if g.Distribution != "" {
		return g.Distribution
	}
	return "game system"
}

// bind ties the manifest's parts to the release's files: same name, size and checksum
// as GitHub reports for them.
func (g *GuestRelease) bind(assets []Asset) error {
	g.assets = nil
	for _, p := range g.Download.Parts {
		found := false
		for _, a := range assets {
			if a.Name == p.Name {
				if a.Size != p.Size || strings.TrimPrefix(a.Digest, "sha256:") != p.SHA256 {
					return fmt.Errorf("the %s release's files don't match its description", g.name())
				}
				g.assets, found = append(g.assets, a), true
			}
		}
		if !found {
			return fmt.Errorf("the %s release is missing files", g.name())
		}
	}
	return nil
}

// DownloadBytes is what DownloadGuest fetches.
func (g *GuestRelease) DownloadBytes() int64 { return g.Download.Size }

// NeedBytes is the most DownloadGuest has on disk at once, while it unpacks: all
// parts and the image (0 when the image is there from before).
func (g *GuestRelease) NeedBytes(dir string) int64 {
	if st, e := os.Stat(filepath.Join(dir, g.Image.SHA256+"-"+g.Image.Name)); e == nil && st.Size() == g.Image.Size {
		return 0
	}
	return g.Download.Size + g.Image.Size
}

// guestLookupTimeout bounds LatestGuest: the apps hold the write back while the
// preselected system is still being looked up, and the download client's own timeout
// is half an hour (a stalled connection would otherwise block the write that long).
var guestLookupTimeout = 30 * time.Second

// LatestGuest returns the latest release of src (see the top of this file), nil when
// there is none.
func LatestGuest(ctx context.Context, src GuestSource) (*GuestRelease, error) {
	if src.Repository == "" {
		return nil, errors.New("unknown game system")
	}
	ctx, cancel := context.WithTimeout(ctx, guestLookupTimeout)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=30", src.Repository), nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "andr36oid-sdflasher")
	rel, e := releaseList(req)
	if e != nil {
		return nil, e
	}
	r := pickGuest(src, rel)
	if r == nil {
		return nil, nil
	}
	for _, a := range r.Assets {
		if a.Name != guestManifest || a.Size <= 0 || a.Size > 1<<20 || !validReleaseURL(a.URL, src.Repository) {
			continue
		}
		b, e := fetchSmall(ctx, a)
		if e != nil {
			return nil, e
		}
		return guestFromRelease(src, r, b)
	}
	return nil, fmt.Errorf("the %s release %s has no %s", src.Display, r.Tag, guestManifest)
}

// guestFromRelease reads the manifest b of release r of src.
func guestFromRelease(src GuestSource, r *release, b []byte) (*GuestRelease, error) {
	g, e := parseGuest(b)
	if e != nil {
		return nil, e
	}
	if g.Distribution != src.ID {
		return nil, fmt.Errorf("the %s release %s describes %s", src.Display, r.Tag, g.Distribution)
	}
	g.Source, g.Tag = src, r.Tag
	if e = g.bind(r.Assets); e != nil {
		return nil, e
	}
	return g, nil
}

func fetchSmall(ctx context.Context, a Asset) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	resp, e := client().Do(req.WithContext(ctx))
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, statusError(resp)
	}
	if e := webPage(resp); e != nil {
		return nil, e
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, a.Size+1))
	if e != nil {
		return nil, e
	}
	sum := sha256.Sum256(b)
	if int64(len(b)) < a.Size {
		return nil, i18n.Detailed(ErrCutOff.New(), fmt.Errorf("%s: %d of %d bytes", a.Name, len(b), a.Size))
	}
	if int64(len(b)) != a.Size || "sha256:"+hex.EncodeToString(sum[:]) != a.Digest {
		return nil, i18n.Detailed(ErrDamagedDownload.New(), errors.New("download checksum mismatch: "+a.Name))
	}
	return b, nil
}

// DownloadGuest fetches the release into dir and returns the unpacked image, checked
// against the manifest. An image already there from an earlier run is used again.
// progress counts the packed bytes, first downloading, then unpacking.
func DownloadGuest(ctx context.Context, g *GuestRelease, dir string, progress func(phase string, done, total int64)) (string, error) {
	final := filepath.Join(dir, g.Image.SHA256+"-"+g.Image.Name)
	if st, e := os.Stat(final); e == nil && st.Size() == g.Image.Size {
		return final, nil
	}
	if len(g.assets) != len(g.Download.Parts) {
		return "", fmt.Errorf("the %s release has no downloads", g.name())
	}
	// the parts and the image are there together while it unpacks
	if e := spaceFor(dir, g.NeedBytes(dir)); e != nil {
		return "", e
	}
	var parts []string
	defer func() {
		for _, p := range parts {
			os.Remove(p)
		}
	}()
	var before int64
	for _, a := range g.assets {
		p, e := downloadRelease(ctx, a, g.Source.Repository, dir, func(n, _ int64) {
			if progress != nil {
				progress("download", before+n, g.Download.Size)
			}
		})
		if e != nil {
			return "", e
		}
		parts = append(parts, p)
		before += a.Size
	}
	return unpackGuest(ctx, g, parts, dir, progress)
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
	n   *int64
}

func (c ctxReader) Read(b []byte) (int, error) {
	if e := c.ctx.Err(); e != nil {
		return 0, e
	}
	n, e := c.r.Read(b)
	*c.n += int64(n)
	return n, e
}

// unpackGuest joins the parts, unpacks them and keeps the image only if its size and
// checksum are the manifest's.
func unpackGuest(ctx context.Context, g *GuestRelease, parts []string, dir string, progress func(string, int64, int64)) (string, error) {
	var readers []io.Reader
	for _, p := range parts {
		f, e := os.Open(p)
		if e != nil {
			return "", e
		}
		defer f.Close()
		readers = append(readers, f)
	}
	var packed int64
	z, e := xz.NewReader(ctxReader{ctx, io.MultiReader(readers...), &packed}, 0)
	if e != nil {
		return "", e
	}
	out, e := os.CreateTemp(dir, "darkosen-*.part")
	if e != nil {
		return "", e
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	defer out.Close()
	h := sha256.New()
	buf := make([]byte, 4<<20)
	var n int64
	for {
		read, re := z.Read(buf)
		if read > 0 {
			if n += int64(read); n > g.Image.Size {
				return "", i18n.Detailed(ErrDamagedDownload.New(), fmt.Errorf("the %s download is damaged", g.name()))
			}
			h.Write(buf[:read])
			if _, e = out.Write(buf[:read]); e != nil {
				return "", firmware.SpaceError(dir, g.Image.Size-n+int64(read), e)
			}
			if progress != nil {
				progress("unpack", min(packed, g.Download.Size), g.Download.Size)
			}
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", i18n.Detailed(ErrDamagedDownload.New(), fmt.Errorf("the %s download is damaged", g.name()))
		}
	}
	if n != g.Image.Size || hex.EncodeToString(h.Sum(nil)) != g.Image.SHA256 {
		return "", i18n.Detailed(ErrDamagedDownload.New(), fmt.Errorf("the %s download is damaged", g.name()))
	}
	if e = out.Sync(); e != nil {
		return "", e
	}
	if e = out.Close(); e != nil {
		return "", e
	}
	final := filepath.Join(dir, g.Image.SHA256+"-"+g.Image.Name)
	if e = os.Rename(tmp, final); e != nil {
		return "", e
	}
	return final, nil
}
