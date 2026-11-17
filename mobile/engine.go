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
		return nil, flash.DeviceFailure(errors.New("unsupported card capacity"))
	}
	return &usbDisk{disk: d, size: size}, nil
}
func (d *usbDisk) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off > d.size || int64(len(p)) > d.size-off || off%512 != 0 || len(p)%512 != 0 {
		return 0, errors.New("invalid USB read range")
	}
	b, e := d.disk.Read(off, int64(len(p)))
	if e != nil {
		return 0, flash.DeviceFailure(e)
	}
	if len(b) != len(p) {
		return 0, flash.DeviceFailure(io.ErrUnexpectedEOF)
	}
	return copy(p, b), nil
}
func (d *usbDisk) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off > d.size || int64(len(p)) > d.size-off || off%512 != 0 || len(p)%512 != 0 {
		return 0, errors.New("invalid USB write range")
	}
	if e := d.disk.Write(off, p); e != nil {
		return 0, flash.DeviceFailure(e)
	}
	return len(p), nil
}
func (d *usbDisk) Sync() error     { return flash.DeviceFailure(d.disk.Flush()) }
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
	// guests are the game system releases offered for image (by system), guestPaths
	// their unpacked images once DownloadGuestSystem fetched them; they go with the
	// loaded release (guestFor).
	guests     map[string]*catalog.GuestRelease
	guestFor   string
	guestPaths map[string]string
	// failure is flash.Classify of the last Write's error ("" after a good one).
	failure flash.FailureKind
	// cancel stops the write in progress; guarded by cancelMu, not mu, so the
	// app can call Cancel while Write holds mu.
	cancelMu sync.Mutex
	cancel   context.CancelFunc
}

// Stopped is the message of the error Write returns when Cancel ended it. The
// app matches on it to show "stopped" rather than a failure.
const Stopped = "stopped by the user"

// StoppedIncomplete is Stopped for a new card that was stopped after the write had
// begun to change it: it has to be written again before it is used. It starts with
// Stopped.
const StoppedIncomplete = Stopped + "; the card is incomplete"

// stopReason is the error of a write that ended with e: Stopped when Cancel ended it
// (StoppedIncomplete when a new card had begun to change), e itself otherwise and
// when the restore of a stopped update failed (the card is not as it was).
func stopReason(cancelled bool, e error, began bool, mode string) error {
	if !cancelled || errors.Is(e, flash.ErrRestoreFailed) {
		return e
	}
	if began && mode == "install" {
		return errors.New(StoppedIncomplete)
	}
	return errors.New(Stopped)
}

// StoppedDownload is Stopped for a game system download that Cancel ended, before
// anything was written. It starts with Stopped.
const StoppedDownload = Stopped + "; nothing was written"

// Explain is the user's message in an error text from the engine, translated, or ""
// when it has none (the technical text is shown as it is).
func Explain(locale, message string) string { return i18n.Explain(locale, message) }

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
	loaded, e := s.Load(path, observer)
	if e != nil {
		return loaded, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.image.Source = a.URL
	return encode(s.image)
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
	Bootloader     *firmware.BootloaderChoice `json:"bootloader,omitempty"`
	Mode           string                     `json:"mode"`
	Profile        string                     `json:"profile"`
	NoROMs         bool                       `json:"no_roms"`
	UserdataGiB    int                        `json:"userdata_gib"`
	Fingerprint    string                     `json:"fingerprint"`
	Identity       string                     `json:"identity"`
	Custom         string                     `json:"custom"`
	CustomAccepted bool                       `json:"custom_accepted"`
	ConvertDTB     bool                       `json:"convert_dtb"`
	System         string                     `json:"system"`
	SystemAccepted bool                       `json:"system_accepted"`
	// GuestSystem is the game system put on a new card, in its own partition:
	// "dArkOS", "dArkOSRE", "dArkOSen" or "none" ("" is none, or dArkOSen with Guest).
	// DownloadGuestSystem must have fetched it.
	GuestSystem string `json:"guest_system"`
	// Guest is the older form of GuestSystem "dArkOSen", still understood.
	Guest bool `json:"guest"`
}

// guestSystem is the system opts ask for, catalog.NoGuest for none.
func (o options) guestSystem() (string, error) {
	if o.GuestSystem == "" && o.Guest {
		return "dArkOSen", nil
	}
	id, ok := catalog.ParseGuestChoice(o.GuestSystem)
	if !ok {
		return "", fmt.Errorf("unknown game system %q", o.GuestSystem)
	}
	return id, nil
}

type guestOffer struct {
	System         string `json:"system"`
	Name           string `json:"name"`
	Darkosen       string `json:"darkosen"`
	DownloadBytes  int64  `json:"download_bytes"`
	PartitionBytes int64  `json:"partition_bytes"`
	Downloaded     bool   `json:"downloaded"`
	// State is "ready"; "none" (no release published yet), "missing" (the lookup
	// failed: no connection, GitHub, a damaged release), "flasher" (the release
	// needs a newer flasher) or "too_old" (the loaded release can't run it).
	State string `json:"state"`
}

// latestGuest is catalog.LatestGuest (the tests replace it).
var latestGuest = catalog.LatestGuest

// Why a game system can't be offered, as guestOffer.State.
const (
	guestNone    = "none"
	guestMissing = "missing"
	guestFlasher = "flasher"
	guestTooOld  = "too_old"
)

// offerFor is the release of system id that fits the loaded release, nil for none.
// It forgets what was looked up or downloaded for another release.
func (s *Session) offerFor(id string) *catalog.GuestRelease {
	if s.image == nil || s.guestFor != s.image.SHA256 {
		s.guests, s.guestPaths, s.guestFor = nil, nil, ""
		return nil
	}
	return s.guests[id]
}

// lookupGuest finds the latest release of src for the loaded release im and keeps it.
func (s *Session) lookupGuest(im *firmware.Image, src catalog.GuestSource) (guestOffer, error) {
	o := guestOffer{System: src.ID, Name: src.Display, State: guestMissing}
	g, e := latestGuest(context.Background(), src)
	if errors.Is(e, catalog.ErrGuestNeedsNewerFlasher) {
		o.State = guestFlasher
		return o, nil
	}
	if e != nil {
		return o, e
	}
	if g == nil {
		o.State = guestNone
		return o, nil
	}
	o.Darkosen, o.DownloadBytes, o.PartitionBytes = g.Darkosen, g.DownloadBytes(), g.PartitionBytes
	if e := flash.GuestFits(im, &flash.Guest{Distribution: g.Distribution, Minimum: g.Minimum}); e != nil {
		o.State = guestFlasher
		if errors.Is(e, flash.ErrGuestNeedsNewerRelease) {
			o.State = guestTooOld
		}
		return o, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.image != im {
		return o, nil
	}
	if s.guestFor != im.SHA256 {
		s.guests, s.guestPaths, s.guestFor = map[string]*catalog.GuestRelease{}, map[string]string{}, im.SHA256
	}
	if old := s.guests[src.ID]; old == nil || old.Image.SHA256 != g.Image.SHA256 {
		s.guests[src.ID] = g
		delete(s.guestPaths, src.ID)
	}
	o.State, o.Downloaded = "ready", s.guestPaths[src.ID] != ""
	return o, nil
}

// GuestOffers lists the game systems for a new card with the loaded release, as JSON
// {"default": "dArkOS", "systems": [...]} in the order to offer them (guestOffer;
// "none" is not listed), or "" when the release can't run a system from its own
// partition. A system that can't be looked up is listed as "missing".
func (s *Session) GuestOffers() (string, error) {
	s.mu.Lock()
	im := s.image
	s.mu.Unlock()
	if im == nil || !im.Subsystem() {
		return "", nil
	}
	list := make([]guestOffer, len(catalog.GuestSources))
	var wg sync.WaitGroup
	for i, src := range catalog.GuestSources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list[i], _ = s.lookupGuest(im, src)
		}()
	}
	wg.Wait()
	return encode(struct {
		Default string       `json:"default"`
		Systems []guestOffer `json:"systems"`
	}{catalog.DefaultGuest, list})
}

// GuestOffer is the older single offer: dArkOSen, as JSON, or "" when it can't go on
// a new card with the loaded release.
func (s *Session) GuestOffer() (string, error) {
	s.mu.Lock()
	im := s.image
	s.mu.Unlock()
	if im == nil || !im.Subsystem() {
		return "", nil
	}
	src, _ := catalog.FindGuestSource("dArkOSen")
	o, e := s.lookupGuest(im, src)
	if e != nil || o.State != "ready" {
		return "", e
	}
	return encode(o)
}

// DownloadGuestSystem fetches and unpacks the release of system id that GuestOffers
// listed as ready; the events are "downloading" and then "unpack". Write with that
// "guest_system" needs it.
func (s *Session) DownloadGuestSystem(id string, observer Observer) error {
	s.mu.Lock()
	g, im := s.offerFor(id), s.image
	s.mu.Unlock()
	if g == nil {
		return errors.New("this game system can't go on a card with this Android release")
	}
	// Cancel stops the download too
	ctx, done := s.writeContext()
	defer done()
	path, e := catalog.DownloadGuest(ctx, g, s.dir, func(phase string, done, total int64) {
		if phase != "unpack" {
			phase = "downloading"
		}
		progress(observer, phase)(done, total)
	})
	if e != nil {
		if ctx.Err() != nil {
			return errors.New(StoppedDownload)
		}
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.image == im && s.guests[id] == g {
		s.guestPaths[id] = path
	}
	return nil
}

// DownloadGuest is the older form of DownloadGuestSystem("dArkOSen").
func (s *Session) DownloadGuest(observer Observer) error {
	return s.DownloadGuestSystem("dArkOSen", observer)
}

// guestChoice is the game system image for a plan that asks for one.
func (s *Session) guestChoice(opts options) (*flash.Guest, error) {
	return s.guestForOptions(opts, false)
}

// A preview describes the partition before the erase confirmation/download.
// Write always rebuilds its plan with preview=false and the verified download.
func (s *Session) guestForOptions(opts options, preview bool) (*flash.Guest, error) {
	id, e := opts.guestSystem()
	if e != nil || id == catalog.NoGuest {
		return nil, e
	}
	g := s.offerFor(id)
	if g == nil || (s.guestPaths[id] == "" && !preview) {
		return nil, fmt.Errorf("download %s first", id)
	}
	if opts.Mode != "install" {
		return nil, fmt.Errorf("%s only goes on a newly written card", id)
	}
	path := s.guestPaths[id]
	if path == "" {
		// Metadata only: Plan never opens this path. It is not saved in the
		// session and cannot satisfy Write's strict download requirement.
		path = filepath.Join(s.dir, "guest-preview")
	}
	return &flash.Guest{Path: path, Distribution: g.Distribution, Darkosen: g.Darkosen, Revision: g.Revision, Minimum: g.Minimum, Name: g.Image.Name, Size: g.Image.Size, SHA256: g.Image.SHA256, PartitionBytes: g.PartitionBytes}, nil
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
	if card.DataCard && opts.Bootloader != nil {
		if opts.Bootloader.Changed() || opts.Bootloader.File != "" {
			return nil, opts, errors.New(firmware.BootloaderOnlySD)
		}
		opts.Bootloader = nil
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
	Bootloader *firmware.BootloaderOutcome `json:"bootloader,omitempty"`
	Anchors    []anchor                    `json:"anchors,omitempty"`
	Directory  string                      `json:"directory"`
	Identity   string                      `json:"identity"`
	Size       int64                       `json:"size"`
	Created    string                      `json:"created"`
	Version    string                      `json:"version"`
	Profile    string                      `json:"profile"`
}

// Write writes the card. After a failure, LastFailureHints has the hints to show
// after the error.
func (s *Session) Write(d Disk, input string, observer Observer) (string, error) {
	out, e := s.write(d, input, observer)
	s.mu.Lock()
	s.failure = flash.Classify(e)
	s.mu.Unlock()
	return out, e
}

// LastFailureHints are the hints for the last Write's failure: English lines, one
// per line, each to be translated; "" when there are none (flash.FailureHints).
func (s *Session) LastFailureHints() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(flash.FailureHints(s.failure, true), "\n")
}

// ReaderFailureHints are the hints for a failure of the card reader itself (the app
// could not open or talk to it), in the form of LastFailureHints.
func ReaderFailureHints() string {
	return strings.Join(flash.FailureHints(flash.FailureDevice, true), "\n")
}

func (s *Session) write(d Disk, input string, observer Observer) (string, error) {
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
	ctx, done := s.writeContext()
	defer done()
	var loader []byte
	var outcome *firmware.BootloaderOutcome
	if opts.Bootloader != nil {
		emit(observer, flash.Progress{Phase: "Bootloader"})
		loader, outcome, e = firmware.ResolveBootOptions(ctx, *opts.Bootloader, s.image, firmware.ImageBootTarget(s.image, opts.Profile), dir)
		if e != nil {
			return "", e
		}
	}
	rec := record{Directory: dir, Identity: d.Identity(), Size: dev.size, Created: time.Now().UTC().Format(time.RFC3339), Version: s.image.Version, Profile: opts.Profile}
	rec.Bootloader = outcome
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
	began := false
	if e = flash.Execute(ctx, dev, s.image, p, boot, backup, func(p flash.Progress) { began = began || p.Writing(); emit(observer, p) }); e != nil {
		return "", fmt.Errorf("%w\nRecovery: %s", stopReason(ctx.Err() != nil, e, began, p.Mode), dir)
	}
	if e = firmware.RecordImageSource(dev, dev.size, s.image.BuildIdentity, s.image.Source, dir); e != nil {
		return "", e
	}
	if s.image.DTBPack != nil && opts.Custom == "" {
		if _, e = firmware.ApplyDTBPack(dev, dev.size, s.image.DTBPack, s.image.BuildIdentity, dir, firmware.DTBOptions{Profile: opts.Profile}); e != nil {
			return "", e
		}
	}
	if loader != nil {
		if e = ctx.Err(); e != nil {
			return "", errors.New(Stopped)
		}
		emit(observer, flash.Progress{Phase: "Bootloader", Done: 0, Total: firmware.BootloaderSize})
		if e = firmware.ApplyResolvedBootOptions(dev, dev.size, loader, outcome, s.image.BuildIdentity, dir); e != nil {
			return "", e
		}
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
		return "", errors.New("this release cannot run a game system from its own partition")
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
			return "", errors.New("this release cannot prepare a game system partition")
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
func Translate(locale, key string) string {
	key, args := firmware.DTBErrorDetails(key)
	return i18n.Text(locale, key, args...)
}

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

// CacheUsage is what the app's storage holds, as JSON: "total" bytes, "free" (what
// ClearCache frees: downloaded releases, unpacked images and game systems) and
// "backups" bytes in "backup_count" recovery backups, which it keeps.
func (s *Session) CacheUsage() (string, error) {
	total, backups, count := firmware.WorkDirUsage(s.dir)
	return encode(map[string]int64{"total": total, "free": total - backups, "backups": backups, "backup_count": int64(count)})
}

// ClearCache frees the app's storage: downloaded releases, unpacked images and game
// systems go, recovery backups stay. The loaded release goes with them; choose it
// again afterwards. It waits for a write or download to finish.
func (s *Session) ClearCache() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.image, s.guests, s.guestPaths, s.guestFor = nil, nil, nil, ""
	firmware.ClearWorkDir(s.dir, false)
	return nil
}
