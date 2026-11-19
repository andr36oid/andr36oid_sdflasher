package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Request struct {
	DTBManifest string              `json:"dtb_manifest,omitempty"`
	DTBFile     string              `json:"dtb_file,omitempty"`
	DTBOnline   bool                `json:"dtb_online,omitempty"`
	DTBExpected string              `json:"dtb_expected,omitempty"`
	DTBOptions  firmware.DTBOptions `json:"dtb_options,omitempty"`

	Bootloader         *firmware.BootloaderChoice `json:"bootloader,omitempty"`
	BootloaderExpected string                     `json:"bootloader_expected,omitempty"`
	KernelOffer        *catalog.KernelOffer       `json:"kernel_offer,omitempty"`
	HotfixOffer        *firmware.HotfixOffer      `json:"hotfix_offer,omitempty"`
	HotfixFiles        []string                   `json:"hotfix_files,omitempty"`
	Recovery           string                     `json:"recovery,omitempty"`
	Action             string                     `json:"action"`
	Drive              platform.Drive             `json:"drive"`
	Image              string                     `json:"image,omitempty"`
	ImageHash          string                     `json:"image_hash,omitempty"`
	ImageSource        string                     `json:"image_source,omitempty"`
	CardFingerprint    string                     `json:"card_fingerprint,omitempty"`
	Mode               string                     `json:"mode,omitempty"`
	Profile            string                     `json:"profile,omitempty"`
	NoROMs             bool                       `json:"no_roms,omitempty"`
	UserdataGiB        int                        `json:"userdata_gib,omitempty"`
	CustomDTB          string                     `json:"custom_dtb,omitempty"`
	CustomAccepted     bool                       `json:"custom_accepted,omitempty"`
	ConvertDTB         bool                       `json:"convert_dtb,omitempty"`
	SystemImage        string                     `json:"system_image,omitempty"`
	SystemAccepted     bool                       `json:"system_accepted,omitempty"`
	// Guest is the dArkOSen release that gets its own partition on a fresh card.
	Guest *flash.Guest `json:"guest,omitempty"`
}
type Result struct {
	BootTarget *firmware.BootTarget `json:"boot_target,omitempty"`
	DTBPack    *firmware.DTBPack    `json:"dtb_pack,omitempty"`
	DTBPlan    *firmware.DTBPlan    `json:"dtb_plan,omitempty"`
	DTBResult  *firmware.DTBResult  `json:"dtb_result,omitempty"`

	Bootloader     *firmware.BootloaderOutcome `json:"bootloader,omitempty"`
	Setup          *firmware.CardSetup         `json:"setup,omitempty"`
	KernelIdentity *firmware.KernelIdentity    `json:"kernel_identity,omitempty"`
	BuildIdentity  string                      `json:"build_identity,omitempty"`
	FirstBoot      *firmware.FirstBoot         `json:"first_boot,omitempty"`
	HotfixResults  []firmware.HotfixResult     `json:"hotfix_results,omitempty"`
	Done           bool                        `json:"done"`
	Error          string                      `json:"error,omitempty"`
	Card           *flash.Card                 `json:"card,omitempty"`
	Progress       *flash.Progress             `json:"progress,omitempty"`
	Backup         string                      `json:"backup,omitempty"`

	// ErrorKind is flash.Classify of the error, so that the app can add hints.
	ErrorKind flash.FailureKind `json:"error_kind,omitempty"`
}

func CacheDir() (string, error) {
	d, e := cacheBase()
	if e != nil {
		return "", e
	}
	p := filepath.Join(d, "andr36oid-sdflasher")
	return p, os.MkdirAll(p, 0700)
}
func Worker(job string) error {
	st, e := os.Lstat(job)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return errors.New("invalid job file")
	}
	f, e := os.Open(job)
	if e != nil {
		return e
	}
	var req Request
	e = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&req)
	f.Close()
	if e != nil {
		return e
	}
	dir := filepath.Dir(job)
	status := filepath.Join(dir, "status.json")
	emit := func(r Result) {
		b, _ := json.Marshal(r)
		tmp := status + ".tmp"
		if os.WriteFile(tmp, b, 0644) == nil {
			os.Rename(tmp, status)
		}
	}
	var result Result
	defer func() { result.Done = true; emit(result) }()
	work := func() error {
		d, e := platform.Revalidate(req.Drive)
		if e != nil {
			return e
		}
		if req.Action == "inspect" {
			h, e := platform.Open(d, false)
			if e != nil {
				return e
			}
			defer h.Close()
			card, e := flash.InspectCard(h.File, d.Size)
			result.Card = &card
			return e
		}

		if req.Action == "dtb-info" || req.Action == "dtb-plan" || req.Action == "dtb-apply" || req.Action == "dtb-restore" || req.Action == "dtb-restore-plan" {
			writing := req.Action == "dtb-apply" || req.Action == "dtb-restore"
			h, e := platform.Open(d, writing)
			if e != nil {
				return e
			}
			defer h.Close()
			card, e := flash.InspectCard(h.File, d.Size)
			if e != nil {
				return e
			}
			result.Card = &card
			build, tab, e := firmware.CardBuildIdentity(h.File, d.Size)
			if e != nil {
				return errors.New(firmware.DTBPackNoBuild)
			}
			result.BuildIdentity = build
			boot, e := firmware.OpenBoot(h.File, tab, true)
			if e != nil {
				return e
			}
			setup, e := firmware.ReadCardSetup(boot)
			if e != nil {
				return e
			}
			result.Setup = &setup
			if req.Action == "dtb-info" {
				return nil
			}
			if req.DTBExpected != build || req.CardFingerprint == "" || req.CardFingerprint != card.Fingerprint {
				return errors.New("the card changed; choose it again")
			}
			if req.Action == "dtb-restore-plan" {
				result.DTBPlan, e = firmware.PlanRestoreDTB(h.File, d.Size, build, dir)
				return e
			}
			if req.Action == "dtb-restore" {
				h.Remount = true
				result.DTBResult, e = firmware.RestoreDTBSetup(h.File, d.Size, build, dir, req.DTBOptions)
				return e
			}
			var pack *firmware.DTBPack
			if req.DTBOnline {
				pack, e = firmware.FetchDTBPack(context.Background(), setup, dir)
			} else {
				pack, e = firmware.ReadDTBPackReference(req.DTBFile, req.DTBManifest)
			}
			if e != nil {
				return e
			}
			result.DTBPack = pack
			result.DTBPlan, e = firmware.PlanDTBPack(h.File, d.Size, pack, build, dir, req.DTBOptions)
			if e != nil {
				return e
			}
			if req.Action == "dtb-plan" {
				return nil
			}
			h.Remount = true
			emit(Result{Progress: &flash.Progress{Phase: "Writing device trees..."}})
			req.DTBOptions.Progress = func(done, total int64) {
				emit(Result{Progress: &flash.Progress{Phase: "Writing device trees...", Done: done, Total: total}})
			}
			result.DTBResult, e = firmware.ApplyDTBPack(h.File, d.Size, pack, build, dir, req.DTBOptions)
			return e
		}
		if req.Action == "bootloader-info" || req.Action == "bootloader-apply" {
			h, err := platform.Open(d, req.Action == "bootloader-apply")
			if err != nil {
				return err
			}
			defer h.Close()
			card, err := flash.InspectCard(h.File, d.Size)
			if err != nil {
				return err
			}
			result.Card = &card
			build, tab, err := firmware.CardBuildIdentity(h.File, d.Size)
			if err != nil {
				return err
			}
			result.BuildIdentity = build
			if req.Action == "bootloader-info" {
				boot, err := firmware.OpenBoot(h.File, tab, true)
				if err != nil {
					return err
				}
				setup, err := firmware.ReadCardSetup(boot)
				result.Setup = &setup
				if err == nil {
					target, e := firmware.CardBootTarget(h.File, d.Size, nil)
					if e != nil {
						return e
					}
					result.BootTarget = &target
				}
				return err
			}
			if d.Fixed || card.DataCard {
				return errors.New(firmware.BootloaderOnlySD)
			}
			if req.Bootloader == nil {
				return errors.New("choose a bootloader")
			}
			if req.CardFingerprint == "" || card.Fingerprint != req.CardFingerprint || build != req.BootloaderExpected {
				return errors.New("the card changed; choose it again")
			}
			var image *firmware.Image
			if req.Image != "" {
				if req.ImageHash == "" {
					image, err = firmware.Inspect(context.Background(), req.Image, nil)
				} else {
					image, err = firmware.InspectForWrite(context.Background(), req.Image, req.ImageHash)
				}
				if err != nil {
					return err
				}
			}
			target, err := firmware.CardBootTarget(h.File, d.Size, image)
			if err != nil {
				return err
			}
			data, out, err := firmware.ResolveBootOptions(context.Background(), *req.Bootloader, image, target, dir)
			result.Bootloader = out
			if err != nil {
				return err
			}
			if data == nil {
				return nil
			}
			h.Remount = true
			emit(Result{Progress: &flash.Progress{Phase: "Bootloader", Done: 0, Total: firmware.BootloaderSize}})
			return firmware.ApplyResolvedBootOptions(h.File, d.Size, data, out, build, dir)
		}
		if req.Action == "hotfix-list" || req.Action == "hotfix-apply" {
			h, e := platform.Open(d, req.Action == "hotfix-apply")
			if e != nil {
				return e
			}
			defer h.Close()
			if req.Action == "hotfix-list" {
				var tab layout.Table
				result.BuildIdentity, tab, e = firmware.CardBuildIdentity(h.File, d.Size)
				if e == nil {
					first := firmware.CardFirstBoot(h.File, tab, result.BuildIdentity)
					result.FirstBoot = &first
				}
				return e
			}
			h.Remount = true
			result.HotfixResults, e = firmware.ApplyHotfixes(context.Background(), h.File, d.Size, req.HotfixOffer, req.HotfixFiles, dir, func(p firmware.HotfixProgress) {
				emit(Result{Progress: &flash.Progress{Phase: p.Phase, Name: p.Name, Done: p.Done, Total: p.Total}})
			})
			return e
		}
		if req.Action == "kernel-list" {
			h, err := platform.Open(d, false)
			if err != nil {
				return err
			}
			defer h.Close()
			identity, _, err := firmware.CardKernelIdentity(h.File, d.Size)
			result.KernelIdentity = &identity
			return err
		}
		if req.Action == "kernel-apply" {
			offer := req.KernelOffer
			asset, err := catalog.DownloadKernel(context.Background(), offer, dir, func(n, total int64) {
				emit(Result{Progress: &flash.Progress{Phase: "Downloading kernel update", Done: n, Total: total}})
			})
			if err != nil {
				return err
			}
			defer os.Remove(asset)
			bundle, err := firmware.ReadKernelArchive(asset)
			if err != nil {
				return err
			}
			if firmware.KernelVersion(bundle.Manifest.Version) != firmware.KernelVersion(offer.Version) || !firmware.KernelCompatible(bundle.Manifest.CompatibleBuilds, offer.Current.Build) {
				return errors.New("kernel manifest does not match the offered release")
			}
			// Downloads may take a while: revalidate again before opening writable.
			d, err = platform.Revalidate(req.Drive)
			if err != nil {
				return err
			}
			h, err := platform.Open(d, true)
			if err != nil {
				return err
			}
			h.Remount = true
			defer h.Close()
			emit(Result{Progress: &flash.Progress{Phase: "Writing and verifying kernel update"}})
			return firmware.ApplyKernelUpdate(h.File, d.Size, bundle, offer.Current, dir)
		}

		if req.Action == "restore" {
			original, e := os.Open(filepath.Join(filepath.Dir(req.Recovery), "request.json"))
			if e != nil {
				return errors.New("original update request is missing")
			}
			var previous Request
			e = json.NewDecoder(io.LimitReader(original, 1<<20)).Decode(&previous)
			original.Close()
			if e != nil {
				return e
			}
			if !platform.Same(d, previous.Drive) || previous.Mode != "update" {
				return errors.New("this recovery backup belongs to a different card")
			}
			h, e := platform.Open(d, true)
			if e != nil {
				return e
			}
			defer h.Close()
			emit(Result{Progress: &flash.Progress{Phase: "restoring", Name: "Verifying and restoring the recovery backup"}})
			return flash.RestoreBackupForCard(context.Background(), h.File, req.Recovery, d.Size)
		}
		if req.Action != "write" {
			return errors.New("unknown job action")
		}
		if req.CustomDTB != "" && !req.CustomAccepted {
			return errors.New("custom DTB warning has not been accepted")
		}
		emit(Result{Progress: &flash.Progress{Phase: "preflight", Name: "Validating release"}})
		im, e := firmware.InspectForWrite(context.Background(), req.Image, req.ImageHash)
		if e != nil {
			return e
		}
		var freshPack *firmware.DTBPack
		if req.DTBFile != "" {
			freshPack, e = firmware.ReadDTBPackReference(req.DTBFile, req.DTBManifest)
			if e != nil {
				return e
			}
			im, e = firmware.ImageWithDTBPack(im, freshPack, dir)
			if e != nil {
				return e
			}
		}
		h, e := platform.Open(d, true)
		if e != nil {
			if _, busy := platform.Busy(e.Error()); busy {
				return e
			}
			if wp := flash.WriteProtectedError(e); wp != nil {
				return wp
			}
			// no access to the card, or it went away
			return flash.DeviceFailure(e)
		}
		// a new card stays mounted for adding APKs to AUTOAPK; the app ejects it
		h.Remount = req.Mode == "install"
		defer h.Close()
		// errors of the card itself, also outside the write, get the failure hints
		dev := flash.DeviceErrors(h.File)
		card, e := flash.InspectCard(dev, d.Size)
		if e != nil {
			return e
		}
		if card.Fingerprint != req.CardFingerprint {
			return errors.New("card changed after inspection; select it again")
		}
		var loader []byte
		if req.Bootloader != nil {
			if d.Fixed || card.DataCard {
				if req.Bootloader.Changed() || req.Bootloader.File != "" {
					return errors.New(firmware.BootloaderOnlySD)
				}
			} else {
				emit(Result{Progress: &flash.Progress{Phase: "Bootloader"}})
				loader, result.Bootloader, e = firmware.ResolveBootOptions(context.Background(), *req.Bootloader, im, firmware.ImageBootTarget(im, req.Profile), dir)
				if e != nil {
					return e
				}
			}
		}
		if req.ConvertDTB && req.CustomDTB != "" {
			conv, e := firmware.ConvertCustom(im, req.CustomDTB)
			if e != nil {
				return e
			}
			if conv.Profile != "" {
				req.Profile = conv.Profile
			}
		}
		p, e := flash.BuildWith(im, card, d.Size, req.Mode, req.Profile, req.NoROMs, req.UserdataGiB, flash.Options{Guest: req.Guest})
		if e != nil {
			return e
		}
		if req.SystemImage != "" {
			if !req.SystemAccepted {
				return errors.New(firmware.GSIWarning)
			}
			emit(Result{Progress: &flash.Progress{Phase: "preflight", Name: "Preparing the system image"}})
			sys, e := firmware.PrepareSystem(context.Background(), im, req.SystemImage, dir)
			if e != nil {
				return e
			}
			defer os.Remove(sys)
			if e = p.UseSystemImage(sys); e != nil {
				return e
			}
		}
		files, e := p.BootFiles()
		if e != nil {
			return e
		}
		boot, e := firmware.PrepareBoot(im, req.Profile, p.NoROMs, req.Mode == "update", req.CustomDTB, dir, firmware.BootOptions{ConvertDTB: req.ConvertDTB, UserdataGiB: p.UserdataGiB, Keep: p.Keep, Prepared: p.Prepared, Files: files})
		if e != nil {
			return e
		}
		defer os.Remove(boot)
		backup := filepath.Join(dir, "recovery")
		if req.Mode == "update" {
			result.Backup = backup
		}
		if e = flash.Execute(context.Background(), dev, im, p, boot, backup, func(p flash.Progress) { emit(Result{Progress: &p, Backup: result.Backup}) }); e != nil {
			return e
		}
		if e = firmware.RecordImageSource(dev, d.Size, im.BuildIdentity, req.ImageSource, dir); e != nil {
			return e
		}
		if freshPack != nil && req.CustomDTB == "" {
			result.DTBResult, e = firmware.ApplyDTBPack(dev, d.Size, freshPack, im.BuildIdentity, dir, firmware.DTBOptions{Profile: req.Profile})
			if e != nil {
				return e
			}
		}
		if loader != nil {
			emit(Result{Progress: &flash.Progress{Phase: "Bootloader", Done: 0, Total: firmware.BootloaderSize}})
			if e = firmware.ApplyResolvedBootOptions(dev, d.Size, loader, result.Bootloader, im.BuildIdentity, dir); e != nil {
				return e
			}
		}
		return nil
	}
	if e := work(); e != nil {
		result.Error, result.ErrorKind = e.Error(), flash.Classify(e)
	}
	return nil
}
func helperName() string {
	name := "andr36oid-sdflasher-helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// HelperAvailable reports whether the privileged disk helper sits next to the
// app. The UI calls this at startup so it can refuse clearly, instead of only
// failing once a card job runs: a common mistake is launching the executable
// straight from inside the downloaded zip, which leaves the helper behind.
func HelperAvailable() error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	if _, e = os.Stat(filepath.Join(filepath.Dir(exe), helperName())); e != nil {
		return fmt.Errorf("disk helper missing: keep both application files together: %w", e)
	}
	return nil
}

func helper(dir string) (string, error) {
	exe, e := os.Executable()
	if e != nil {
		return "", e
	}
	name := helperName()
	src := filepath.Join(filepath.Dir(exe), name)
	if _, e = os.Stat(src); e != nil {
		return "", fmt.Errorf("disk helper missing: keep both application files together: %w", e)
	}
	if os.Getenv("FLATPAK_ID") == "" && os.Getenv("APPIMAGE") == "" {
		return src, nil
	}
	in, e := os.Open(src)
	if e != nil {
		return "", e
	}
	defer in.Close()
	dst := filepath.Join(dir, name)
	out, e := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if e != nil {
		return "", e
	}
	_, e = io.Copy(out, in)
	out.Close()
	return dst, e
}

// writes: the actions that change the card. The computer is kept awake while one runs.
var writes = map[string]bool{"write": true, "restore": true, "dtb-apply": true, "dtb-restore": true, "bootloader-apply": true, "hotfix-apply": true, "kernel-apply": true}

// inhibitSleep is platform.InhibitSleep (the tests count it).
var inhibitSleep = platform.InhibitSleep

// runHelper is platform.RunHelper (the tests stand in for the helper).
var runHelper = platform.RunHelper

// helperPath is helper (the tests stand in for the helper).
var helperPath = helper

func Run(req Request, onProgress func(flash.Progress)) (Result, error) {
	if writes[req.Action] {
		// a computer that goes to sleep halfway leaves the card incomplete
		release := inhibitSleep("Writing an SD card")
		defer release()
	}
	cache, e := CacheDir()
	if e != nil {
		return Result{}, e
	}
	dir, e := os.MkdirTemp(cache, "operation-")
	if e != nil {
		return Result{}, e
	}
	// Keep the recovery directory owned by the desktop user so its manifest
	// remains selectable after the privileged helper has saved the backup.
	if req.Action == "write" && req.Mode == "update" {
		if e = os.Mkdir(filepath.Join(dir, "recovery"), 0700); e != nil {
			return Result{}, e
		}
	}
	exe, e := helperPath(dir)
	if e != nil {
		return Result{}, e
	}
	b, e := json.Marshal(req)
	if e != nil {
		return Result{}, e
	}
	job := filepath.Join(dir, "request.json")
	if e = os.WriteFile(job, b, 0600); e != nil {
		return Result{}, e
	}
	ch := make(chan error, 1)
	go func() { ch <- runHelper(exe, job) }()
	timer := time.NewTicker(150 * time.Millisecond)
	defer timer.Stop()
	var latest Result
	// began: the helper had begun to change the card
	began := false
	poll := func() {
		b, e := os.ReadFile(filepath.Join(dir, "status.json"))
		if e == nil {
			var r Result
			if json.Unmarshal(b, &r) == nil {
				latest = r
				if r.Progress != nil {
					began = began || req.Action == "write" && r.Progress.Writing()
					if onProgress != nil {
						onProgress(*r.Progress)
					}
				}
			}
		}
	}
	// stopped is the error of a helper that ended (crashed, was killed) halfway: the
	// card is incomplete when it had begun to write
	stopped := func(e error) error {
		if !began {
			return e
		}
		return flash.DeviceFailure(fmt.Errorf("%w (%v)", flash.ErrStoppedMidWrite.New(), e))
	}
	for {
		select {
		case <-timer.C:
			poll()
		case e := <-ch:
			poll()
			if latest.Done && latest.Error != "" {
				// the helper said what went wrong before it ended
				return latest, flash.NewFailure(latest.ErrorKind, errors.New(latest.Error))
			}
			if e != nil {
				return latest, stopped(i18n.Detailed(i18n.CauseHelperStopped.New(), fmt.Errorf("administrator access was denied or the helper stopped: %w", e)))
			}
			if !latest.Done {
				return latest, stopped(i18n.Detailed(i18n.CauseHelperStopped.New(), fmt.Errorf("disk helper ended without a result; recovery files, if present, are in %s", dir)))
			}
			if latest.Error != "" {
				return latest, flash.NewFailure(latest.ErrorKind, errors.New(latest.Error))
			}
			return latest, nil
		}
	}
}
