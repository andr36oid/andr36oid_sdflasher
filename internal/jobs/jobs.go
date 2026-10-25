package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Request struct {
	HotfixOffer     *firmware.HotfixOffer `json:"hotfix_offer,omitempty"`
	HotfixFiles     []string              `json:"hotfix_files,omitempty"`
	Recovery        string                `json:"recovery,omitempty"`
	Action          string                `json:"action"`
	Drive           platform.Drive        `json:"drive"`
	Image           string                `json:"image,omitempty"`
	ImageHash       string                `json:"image_hash,omitempty"`
	ImageSource     string                `json:"image_source,omitempty"`
	CardFingerprint string                `json:"card_fingerprint,omitempty"`
	Mode            string                `json:"mode,omitempty"`
	Profile         string                `json:"profile,omitempty"`
	NoROMs          bool                  `json:"no_roms,omitempty"`
	UserdataGiB     int                   `json:"userdata_gib,omitempty"`
	CustomDTB       string                `json:"custom_dtb,omitempty"`
	CustomAccepted  bool                  `json:"custom_accepted,omitempty"`
	ConvertDTB      bool                  `json:"convert_dtb,omitempty"`
	SystemImage     string                `json:"system_image,omitempty"`
	SystemAccepted  bool                  `json:"system_accepted,omitempty"`
}
type Result struct {
	BuildIdentity string                  `json:"build_identity,omitempty"`
	HotfixResults []firmware.HotfixResult `json:"hotfix_results,omitempty"`
	Done          bool                    `json:"done"`
	Error         string                  `json:"error,omitempty"`
	Card          *flash.Card             `json:"card,omitempty"`
	Progress      *flash.Progress         `json:"progress,omitempty"`
	Backup        string                  `json:"backup,omitempty"`
}

func CacheDir() (string, error) {
	d, e := os.UserCacheDir()
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
		if req.Action == "hotfix-list" || req.Action == "hotfix-apply" {
			h, e := platform.Open(d, req.Action == "hotfix-apply")
			if e != nil {
				return e
			}
			defer h.Close()
			if req.Action == "hotfix-list" {
				result.BuildIdentity, _, e = firmware.CardBuildIdentity(h.File, d.Size)
				return e
			}
			h.Remount = true
			result.HotfixResults, e = firmware.ApplyHotfixes(context.Background(), h.File, d.Size, req.HotfixOffer, req.HotfixFiles, dir, func(p firmware.HotfixProgress) {
				emit(Result{Progress: &flash.Progress{Phase: p.Phase, Name: p.Name, Done: p.Done, Total: p.Total}})
			})
			return e
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
		h, e := platform.Open(d, true)
		if e != nil {
			return e
		}
		// a new card stays mounted for adding APKs to AUTOAPK; the app ejects it
		h.Remount = req.Mode == "install"
		defer h.Close()
		card, e := flash.InspectCard(h.File, d.Size)
		if e != nil {
			return e
		}
		if card.Fingerprint != req.CardFingerprint {
			return errors.New("card changed after inspection; select it again")
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
		p, e := flash.Build(im, card, d.Size, req.Mode, req.Profile, req.NoROMs, req.UserdataGiB)
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
		boot, e := firmware.PrepareBoot(im, req.Profile, p.NoROMs, req.Mode == "update", req.CustomDTB, dir, firmware.BootOptions{ConvertDTB: req.ConvertDTB, UserdataGiB: p.UserdataGiB, Keep: p.Keep})
		if e != nil {
			return e
		}
		defer os.Remove(boot)
		backup := filepath.Join(dir, "recovery")
		if req.Mode == "update" {
			result.Backup = backup
		}
		return flash.Execute(context.Background(), h.File, im, p, boot, backup, func(p flash.Progress) { emit(Result{Progress: &p, Backup: result.Backup}) })
	}
	if e := work(); e != nil {
		result.Error = e.Error()
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
func Run(req Request, onProgress func(flash.Progress)) (Result, error) {
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
	exe, e := helper(dir)
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
	go func() { ch <- platform.RunHelper(exe, job) }()
	timer := time.NewTicker(150 * time.Millisecond)
	defer timer.Stop()
	var latest Result
	poll := func() {
		b, e := os.ReadFile(filepath.Join(dir, "status.json"))
		if e == nil {
			var r Result
			if json.Unmarshal(b, &r) == nil {
				latest = r
				if r.Progress != nil && onProgress != nil {
					onProgress(*r.Progress)
				}
			}
		}
	}
	for {
		select {
		case <-timer.C:
			poll()
		case e := <-ch:
			poll()
			if e != nil {
				return latest, fmt.Errorf("administrator access was denied or the helper stopped: %w", e)
			}
			if !latest.Done {
				return latest, fmt.Errorf("disk helper ended without a result; recovery files, if present, are in %s", dir)
			}
			if latest.Error != "" {
				return latest, errors.New(latest.Error)
			}
			return latest, nil
		}
	}
}
