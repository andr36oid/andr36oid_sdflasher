// Package mobile exposes the shared flashing engine to the native Android app.
package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

type Disk interface {
	Capacity() int64
	Identity() string
	Read(offset, length int64) ([]byte, error)
	Write(offset int64, data []byte) error
	Flush() error
}
type Observer interface{ Progress(event string) }

type usbDisk struct {
	disk Disk
	size int64
}

func adapt(d Disk) (*usbDisk, error) {
	if d == nil {
		return nil, errors.New("connect a USB card reader")
	}
	size := d.Capacity()
	if size < 1<<30 || size%512 != 0 {
		return nil, errors.New("unsupported card capacity")
	}
	return &usbDisk{disk: d, size: size}, nil
}
func (d *usbDisk) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off > d.size || int64(len(p)) > d.size-off || off%512 != 0 || len(p)%512 != 0 {
		return 0, errors.New("invalid USB read range")
	}
	b, e := d.disk.Read(off, int64(len(p)))
	if e != nil {
		return 0, e
	}
	if len(b) != len(p) {
		return 0, io.ErrUnexpectedEOF
	}
	return copy(p, b), nil
}
func (d *usbDisk) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off > d.size || int64(len(p)) > d.size-off || off%512 != 0 || len(p)%512 != 0 {
		return 0, errors.New("invalid USB write range")
	}
	if e := d.disk.Write(off, p); e != nil {
		return 0, e
	}
	return len(p), nil
}
func (d *usbDisk) Sync() error     { return d.disk.Flush() }
func encode(v any) (string, error) { b, e := json.Marshal(v); return string(b), e }
func CheckAppUpdate(current string) (string, error) {
	update, err := catalog.CheckAppUpdate(context.Background(), current)
	if err != nil || update == nil {
		return "", err
	}
	return encode(update)
}
func emit(observer Observer, p flash.Progress) {
	if observer != nil {
		s, _ := encode(p)
		observer.Progress(s)
	}
}
func progress(observer Observer, phase string) func(int64, int64) {
	return func(n, total int64) { emit(observer, flash.Progress{Phase: phase, Done: n, Total: total}) }
}

type Session struct {
	mu             sync.Mutex
	dir            string
	image          *firmware.Image
	hotfixOffer    *firmware.HotfixOffer
	hotfixIdentity string
	hotfixURL      string // empty uses the published index
	// guest is the dArkOSen release offered for image, guestPath its unpacked
	// image once DownloadGuest fetched it; both go with the loaded release.
	guest     *catalog.GuestRelease
	guestFor  string
	guestPath string
	// cancel stops the write in progress; guarded by cancelMu, not mu, so the
	// app can call Cancel while Write holds mu.
	cancelMu sync.Mutex
	cancel   context.CancelFunc
}

// Stopped is the message of the error Write returns when Cancel ended it. The
// app matches on it to show "stopped" rather than a failure.
const Stopped = "stopped by the user"

// Cancel stops a Write that is running. The write returns with an error whose
// message starts with Stopped; the card is left as it is at that moment (an
// install must be written again from the start, an update can be restored from
// its recovery backup). No effect when nothing is being written.
func (s *Session) Cancel() {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// writeContext hands out the context a Write runs under and arms Cancel for it.
func (s *Session) writeContext() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelMu.Lock()
	s.cancel = cancel
	s.cancelMu.Unlock()
	return ctx, func() {
		s.cancelMu.Lock()
		s.cancel = nil
		s.cancelMu.Unlock()
		cancel()
	}
}

func NewSession(dir string) (*Session, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("an absolute application storage directory is required")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	// Clear staging scratch from an earlier run so storage doesn't keep filling.
	firmware.CleanWorkDir(dir)
	return &Session{dir: dir}, nil
}
func (s *Session) Load(path string, observer Observer) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Drop the previous selection's staged image before staging the next one.
	if s.image != nil {
		firmware.CleanStagedImage(s.dir, s.image.Path)
	}
	s.image = nil
	phase := "checking"
	if ext := filepath.Ext(path); strings.EqualFold(ext, ".zip") || strings.EqualFold(ext, ".xz") || strings.EqualFold(ext, ".gz") {
		phase = "extracting"
	}
	im, e := firmware.Load(context.Background(), path, s.dir, progress(observer, phase))
	if e != nil {
		return "", e
	}
	s.image = im
	return encode(im)
}
func Releases() (string, error) {
	a, e := catalog.List(context.Background())
	if e != nil {
		return "", e
	}
	return encode(a)
}
func (s *Session) Download(asset string, observer Observer) (string, error) {
	var a catalog.Asset
	if e := json.Unmarshal([]byte(asset), &a); e != nil {
		return "", e
	}
	path, e := catalog.Download(context.Background(), a, s.dir, progress(observer, "downloading"))
	if e != nil {
		return "", e
	}
	return s.Load(path, observer)
}
func InspectDisk(d Disk) (string, error) {
	dev, e := adapt(d)
	if e != nil {
		return "", e
	}
	card, e := flash.InspectCard(dev, dev.size)
	if e != nil {
		return "", e
	}
	return encode(card)
}

type options struct {
	Mode           string `json:"mode"`
	Profile        string `json:"profile"`
	NoROMs         bool   `json:"no_roms"`
	UserdataGiB    int    `json:"userdata_gib"`
	Fingerprint    string `json:"fingerprint"`
	Identity       string `json:"identity"`
	Custom         string `json:"custom"`
	CustomAccepted bool   `json:"custom_accepted"`
	ConvertDTB     bool   `json:"convert_dtb"`
	System         string `json:"system"`
	SystemAccepted bool   `json:"system_accepted"`
	// Guest puts the dArkOSen release of GuestOffer on a new card, in its own
	// partition. DownloadGuest must have fetched it.
	Guest bool `json:"guest"`
}

type guestOffer struct {
	Darkosen       string `json:"darkosen"`
	DownloadBytes  int64  `json:"download_bytes"`
	PartitionBytes int64  `json:"partition_bytes"`
	Downloaded     bool   `json:"downloaded"`
}

// offer is the dArkOSen release that fits the loaded release, nil for none. It
// forgets the one looked up or downloaded for another release.
func (s *Session) offer() *catalog.GuestRelease {
	if s.image == nil || s.guestFor != s.image.SHA256 {
		s.guest, s.guestPath = nil, ""
		return nil
	}
	return s.guest
}

// GuestOffer says which dArkOSen release can go on a new card with the loaded
// release, as JSON, or "" when there is none: an older Android release, or none
// published that this one can run. The app shows the choice only then.
func (s *Session) GuestOffer() (string, error) {
	s.mu.Lock()
	im := s.image
	s.mu.Unlock()
	if im == nil || !im.Subsystem() {
		return "", nil
	}
	g, e := catalog.LatestGuest(context.Background())
	if e != nil {
		return "", e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.image != im || g == nil || flash.GuestFits(im, &flash.Guest{Minimum: g.Minimum}) != nil {
		return "", nil
	}
	if s.guestFor != im.SHA256 || s.guest == nil || s.guest.Image.SHA256 != g.Image.SHA256 {
		s.guest, s.guestFor, s.guestPath = g, im.SHA256, ""
	}
	return encode(guestOffer{g.Darkosen, g.DownloadBytes(), g.PartitionBytes, s.guestPath != ""})
}

// DownloadGuest fetches and unpacks the dArkOSen release of GuestOffer; the
// events are "downloading" and then "unpack". Write with "guest" needs it.
func (s *Session) DownloadGuest(observer Observer) error {
	s.mu.Lock()
	g, im := s.offer(), s.image
	s.mu.Unlock()
	if g == nil {
		return errors.New("no dArkOSen release for this Android release")
	}
	path, e := catalog.DownloadGuest(context.Background(), g, s.dir, func(phase string, done, total int64) {
		if phase != "unpack" {
			phase = "downloading"
		}
		progress(observer, phase)(done, total)
	})
	if e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.image == im && s.guest == g {
		s.guestPath = path
	}
	return nil
}

// guestChoice is the dArkOSen image for a plan that asks for one.
func (s *Session) guestChoice(opts options) (*flash.Guest, error) {
	return s.guestForOptions(opts, false)
}

// A preview describes the partition before the erase confirmation/download.
// Write always rebuilds its plan with preview=false and the verified download.
func (s *Session) guestForOptions(opts options, preview bool) (*flash.Guest, error) {
	if !opts.Guest {
		return nil, nil
	}
	g := s.offer()
	if g == nil || (s.guestPath == "" && !preview) {
		return nil, errors.New("download dArkOSen first")
	}
	if opts.Mode != "install" {
		return nil, errors.New("dArkOSen only goes on a newly written card")
	}
	path := s.guestPath
	if path == "" {
		// Metadata only: Plan never opens this path. It is not saved in the
		// session and cannot satisfy Write's strict download requirement.
		path = filepath.Join(s.dir, "guest-preview")
	}
	return &flash.Guest{Path: path, Darkosen: g.Darkosen, Revision: g.Revision, Minimum: g.Minimum, Name: g.Image.Name, Size: g.Image.Size, SHA256: g.Image.SHA256, PartitionBytes: g.PartitionBytes}, nil
}

func (s *Session) plan(d *usbDisk, input string, preview bool) (*flash.Plan, options, error) {
	var opts options
	if e := json.Unmarshal([]byte(input), &opts); e != nil {
		return nil, opts, e
	}
	if s.image == nil {
		return nil, opts, errors.New("choose a Treble release first")
	}
	if opts.Identity == "" || opts.Identity != d.disk.Identity() {
		return nil, opts, errors.New("the selected card reader changed")
	}
	card, e := flash.InspectCard(d, d.size)
	if e != nil {
		return nil, opts, e
	}
	if opts.Fingerprint != card.Fingerprint {
		return nil, opts, errors.New("card changed after inspection; select it again")
	}
	if opts.Custom != "" && !opts.CustomAccepted {
		return nil, opts, errors.New(firmware.CustomWarning)
	}
	if opts.ConvertDTB && opts.Custom != "" {
		conv, e := firmware.ConvertCustom(s.image, opts.Custom)
		if e != nil {
			return nil, opts, e
		}
		if conv.Profile != "" {
			opts.Profile = conv.Profile
		}
	}
	if opts.System != "" && !opts.SystemAccepted {
		return nil, opts, errors.New(firmware.GSIWarning)
	}
	guest, e := s.guestForOptions(opts, preview)
	if e != nil {
		return nil, opts, e
	}
	p, e := flash.BuildWith(s.image, card, d.size, opts.Mode, opts.Profile, opts.NoROMs, opts.UserdataGiB, flash.Options{Guest: guest})
	return p, opts, e
}
func (s *Session) Plan(d Disk, input string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev, e := adapt(d)
	if e != nil {
		return "", e
	}
	p, _, e := s.plan(dev, input, true)
	if e != nil {
		return "", e
	}
	return encode(p)
}

type anchor struct {
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
	SHA256 string `json:"sha256"`
}
type record struct {
	Anchors   []anchor `json:"anchors,omitempty"`
	Directory string   `json:"directory"`
	Identity  string   `json:"identity"`
	Size      int64    `json:"size"`
	Created   string   `json:"created"`
	Version   string   `json:"version"`
	Profile   string   `json:"profile"`
}

func (s *Session) Write(d Disk, input string, observer Observer) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev, e := adapt(d)
	if e != nil {
		return "", e
	}
	p, opts, e := s.plan(dev, input, false)
	if e != nil {
		return "", e
	}
	dir, e := os.MkdirTemp(s.dir, "operation-")
	if e != nil {
		return "", e
	}
	rec := record{Directory: dir, Identity: d.Identity(), Size: dev.size, Created: time.Now().UTC().Format(time.RFC3339), Version: s.image.Version, Profile: opts.Profile}
	// Sample both ends of each preserved partition, including its filesystem
	// identity. Reusing the same USB reader is not enough to identify a card.
	for _, region := range p.Protected {
		n := min(region.Length, 64<<10)
		for _, off := range []int64{region.Offset, region.Offset + region.Length - n} {
			hash, err := firmware.Hash(context.Background(), io.NewSectionReader(dev, off, n), n, nil)
			if err != nil {
				return "", err
			}
			rec.Anchors = append(rec.Anchors, anchor{Offset: off, Length: n, SHA256: hash})
		}
	}
	raw, _ := json.Marshal(rec)
	metadata, e := os.OpenFile(filepath.Join(dir, "operation.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	_, e = metadata.Write(raw)
	if e == nil {
		e = metadata.Sync()
	}
	closeErr := metadata.Close()
	if e != nil {
		return "", e
	}
	if closeErr != nil {
		return "", closeErr
	}
	directory, e := os.Open(dir)
	if e != nil {
		return "", e
	}
	e = directory.Sync()
	directory.Close()
	if e != nil {
		return "", e
	}
	if opts.System != "" {
		sys, e := firmware.PrepareSystem(context.Background(), s.image, opts.System, dir)
		if e != nil {
			return "", e
		}
		defer os.Remove(sys)
		if e = p.UseSystemImage(sys); e != nil {
			return "", e
		}
	}
	files, e := p.BootFiles()
	if e != nil {
		return "", e
	}
	boot, e := firmware.PrepareBoot(s.image, opts.Profile, p.NoROMs, opts.Mode == "update", opts.Custom, dir, firmware.BootOptions{ConvertDTB: opts.ConvertDTB, UserdataGiB: p.UserdataGiB, Keep: p.Keep, Prepared: p.Prepared, Files: files})
	if e != nil {
		return "", e
	}
	defer os.Remove(boot)
	backup := filepath.Join(dir, "recovery")
	ctx, done := s.writeContext()
	defer done()
	if e = flash.Execute(ctx, dev, s.image, p, boot, backup, func(p flash.Progress) { emit(observer, p) }); e != nil {
		if ctx.Err() != nil {
			e = errors.New(Stopped)
		}
		return "", fmt.Errorf("%w\nRecovery: %s", e, dir)
	}
	return encode(rec)
}
func (s *Session) Recoveries() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dirs, e := filepath.Glob(filepath.Join(s.dir, "operation-*"))
	if e != nil {
		return "", e
	}
	result := []record{}
	for _, dir := range dirs {
		if _, e := os.Stat(filepath.Join(dir, "recovery", "recovery.json")); e != nil {
			continue
		}
		b, e := os.ReadFile(filepath.Join(dir, "operation.json"))
		if e != nil {
			continue
		}
		var r record
		if json.Unmarshal(b, &r) == nil {
			r.Directory = dir
			result = append(result, r)
		}
	}
	return encode(result)
}
func (s *Session) Restore(d Disk, dir string, observer Observer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if filepath.Dir(filepath.Clean(dir)) != s.dir {
		return errors.New("choose a saved recovery backup")
	}
	dev, e := adapt(d)
	if e != nil {
		return e
	}
	b, e := os.ReadFile(filepath.Join(dir, "operation.json"))
	if e != nil {
		return e
	}
	var r record
	if e = json.Unmarshal(b, &r); e != nil {
		return e
	}
	if r.Identity != d.Identity() || r.Size != dev.size {
		return errors.New("this backup belongs to a different card reader or card size")
	}
	if len(r.Anchors) == 0 {
		return errors.New("recovery backup has no saved card identity")
	}
	for _, sample := range r.Anchors {
		if sample.Offset < 0 || sample.Length <= 0 || sample.Length > 64<<10 || sample.Offset > dev.size-sample.Length {
			return errors.New("invalid saved card identity")
		}
		hash, err := firmware.Hash(context.Background(), io.NewSectionReader(dev, sample.Offset, sample.Length), sample.Length, nil)
		if err != nil {
			return err
		}
		if hash != sample.SHA256 {
			return errors.New("the preserved partitions do not match this recovery backup; select the original card")
		}
	}
	emit(observer, flash.Progress{Phase: "restoring"})
	return flash.RestoreBackup(context.Background(), dev, filepath.Join(dir, "recovery"), dev.size)
}
func CustomWarning() string { return firmware.CustomWarning }
func GSIWarning() string    { return firmware.GSIWarning }

// StorageLayout tells the app how a new card of size bytes is split when Android gets
// wantGiB (0: the recommended size), as JSON in whole GiB: android, games (0 when all goes
// to Android), max (the largest choice that leaves a games partition, 0 for none).
func StorageLayout(size int64, wantGiB int) string {
	android, games := flash.UserdataLayout(size, wantGiB, false)
	b, _ := json.Marshal(map[string]int{"android": flash.RoundGiB(android), "games": flash.RoundGiB(games), "max": flash.MaxUserdataGiB(size), "min": flash.MinUserdataGiB})
	return string(b)
}

// StorageLayoutForImage uses the same prepared-card arithmetic as the writer.
// It consumes the immutable Load JSON rather than locking a Session which may
// be writing a card; UI rendering must never wait for that operation's mutex.
func StorageLayoutForImage(image string, size int64, wantGiB int, noROMs bool, guestBytes int64) (string, error) {
	var im firmware.Image
	if err := json.Unmarshal([]byte(image), &im); err != nil {
		return "", err
	}
	if guestBytes < 0 || (guestBytes > 0 && !im.Subsystem()) {
		return "", errors.New("this release cannot run dArkOSen from its own partition")
	}
	android, games, prepared, err := flash.PreparedSizes(&im, size, wantGiB, noROMs, guestBytes)
	if err != nil {
		return "", err
	}
	max := flash.MaxUserdataGiB(size)
	if prepared {
		max = flash.MaxPreparedGiB(&im, size, guestBytes)
	} else {
		if guestBytes > 0 {
			return "", errors.New("this release cannot prepare a dArkOSen partition")
		}
		android, games = flash.UserdataLayout(size, wantGiB, noROMs)
	}
	return encode(map[string]int{"android": flash.RoundGiB(android), "games": flash.RoundGiB(games), "max": max, "min": flash.MinUserdataGiB})
}

type conversion struct {
	Family   string   `json:"family"`
	Profile  string   `json:"profile"`
	Model    string   `json:"model"`
	Width    uint32   `json:"width"`
	Height   uint32   `json:"height"`
	Warnings []string `json:"warnings"`
	Report   string   `json:"report"`
}

// ConvertDTB checks the console's own (ArkOS, dArkOS or stock EmuELEC) DTB
// at path against the chosen release and says what the conversion would do.
// A refusal comes back as an error with a plain-language reason.
func (s *Session) ConvertDTB(path string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.image == nil {
		return "", errors.New("choose a Treble release first")
	}
	c, e := firmware.ConvertCustom(s.image, path)
	if e != nil {
		return "", e
	}
	return encode(conversion{Family: c.Family.Label(), Profile: c.Profile, Model: c.Model, Width: c.Width, Height: c.Height, Warnings: append([]string{}, c.Warnings...), Report: c.Report()})
}

// TooOldImage is the build date (2006-01-02) of an image file name from before
// September 2026, which this flasher refuses without opening it; "" otherwise.
func TooOldImage(name string) string {
	date, _ := firmware.TooOld(name)
	return date
}
func Translate(locale, key string) string { return i18n.Text(locale, key) }

// MinCardBytes is the smallest card a new installation takes (flash.MinCardBytes).
func MinCardBytes() int64                { return flash.MinCardBytes }
func ResolveLocale(locale string) string { return i18n.Resolve(locale) }

// FetchHotfixes reads the card's exact BOOT build and returns a matching checklist.
// expectedBuild is the just-flashed image's build, or empty for standalone use.
// ApplyHotfixes accepts only file names from this session's last successful offer.
func (s *Session) FetchHotfixes(d Disk, expectedBuild string, observer Observer) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hotfixOffer = nil
	s.hotfixIdentity = ""
	dev, e := adapt(d)
	if e != nil {
		return "", e
	}
	emit(observer, flash.Progress{Phase: "Checking for hotfixes"})
	build, _, e := firmware.CardBuildIdentity(dev, dev.size)
	if e != nil {
		return "", e
	}
	if expectedBuild != "" && expectedBuild != build {
		return "", errors.New(firmware.HotfixChanged)
	}
	endpoint := s.hotfixURL
	if endpoint == "" {
		endpoint = firmware.HotfixIndexURL
	}
	index, e := firmware.FetchHotfixIndex(context.Background(), endpoint)
	if e != nil {
		return "", e
	}
	s.hotfixOffer = index.ForBuild(build)
	s.hotfixIdentity = d.Identity()
	return encode(s.hotfixOffer)
}

// ApplyHotfixes writes selected JSON file names, emits download/write progress,
// and returns per-file results. A nonempty result error means that item failed.
func (s *Session) ApplyHotfixes(d Disk, selected string, observer Observer) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d == nil || s.hotfixOffer == nil || d.Identity() != s.hotfixIdentity {
		return "", errors.New(firmware.HotfixChanged)
	}
	var names []string
	if e := json.Unmarshal([]byte(selected), &names); e != nil {
		return "", e
	}
	dev, e := adapt(d)
	if e != nil {
		return "", e
	}
	results, e := firmware.ApplyHotfixes(context.Background(), dev, dev.size, s.hotfixOffer, names, s.dir, func(p firmware.HotfixProgress) {
		emit(observer, flash.Progress{Phase: p.Phase, Name: p.Name, Done: p.Done, Total: p.Total})
	})
	if e != nil {
		return "", e
	}
	return encode(results)
}
