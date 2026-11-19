package cli

import (
	"encoding/json"
	"fmt"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
	"os"
)

func runDTBs(o opts, args []string) int {
	if len(args) > 0 && args[0] == "undo" {
		if len(args) > 2 || (len(args) == 2 && o.drive != "" && o.drive != args[1]) {
			fmt.Fprintln(os.Stderr, "dtbs undo accepts one card path; do not select two different cards.")
			return 2
		}
		if len(args) == 2 {
			o.drive = args[1]
		}
		return runDTBUndo(o)
	}

	if len(args) < 2 || args[0] != "info" && args[0] != "apply" {
		fmt.Fprintln(os.Stderr, "Usage: -cli dtbs info <zip> | -cli [--profile <id>] [--replace-anyway] dtbs apply <zip> <card> | -cli dtbs undo <card>")
		return 2
	}
	p, e := firmware.ReadDTBPack(args[1])
	if e != nil {
		printFailure("", e)
		return 1
	}
	if args[0] == "info" {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "dtbs info needs one zip path")
			return 2
		}
		b, _ := json.MarshalIndent(p.Manifest, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	if len(args) != 3 {
		fmt.Fprintln(os.Stderr, "dtbs apply needs a zip and a card path")
		return 2
	}
	d, e := pickDrive(o.in, o.all, args[2])
	if e != nil {
		printFailure("", e)
		return 1
	}
	info, e := jobs.Run(jobs.Request{Action: "dtb-info", Drive: d}, nil)
	if e != nil {
		printFailure("", e)
		return 1
	}
	req := jobs.Request{Action: "dtb-plan", Drive: d, DTBFile: args[1], DTBExpected: info.BuildIdentity, CardFingerprint: info.Card.Fingerprint, DTBOptions: firmware.DTBOptions{Profile: o.profile, ReplaceAnyway: o.replaceDTBs}}
	planned, e := jobs.Run(req, nil)
	if e != nil {
		printFailure("", e)
		return 1
	}
	if planned.DTBPlan.Already && !planned.DTBPlan.Manual {
		fmt.Println(firmware.DTBPackAlready)
		return 0
	}
	if planned.DTBPlan.Manual && !o.replaceDTBs {
		fmt.Fprintln(os.Stderr, "The console files on this card were changed by hand. Run again with --replace-anyway after keeping a copy.")
		return 1
	}
	if !o.assumeYes && !yes(o.in, "Write device trees and keep the current files for undo? [y/N]: ", false) {
		return 0
	}
	if !cliConfirmDTBScreen(o, planned.DTBPlan.ScreenChanged) {
		return 0
	}
	req.DTBOptions.ScreenAccepted = planned.DTBPlan.ScreenChanged

	req.Action = "dtb-apply"
	_, e = jobs.Run(req, func(p flash.Progress) { printProgress(p) })
	if e != nil {
		printFailure("", e)
		return 1
	}
	fmt.Println("Device trees updated.")
	return 0
}

func cliConfirmDTBScreen(o opts, changed bool) bool {
	if !changed {
		return true
	}
	fmt.Println(firmware.DTBScreenWarningTitle)
	fmt.Println(firmware.DTBScreenWarning)
	return o.screenAccepted || yes(o.in, "I'll watch the screen [y/N]: ", false)
}

func runDTBUndo(o opts) int {
	drive, e := pickDrive(o.in, o.all, o.drive)
	if e != nil {
		printFailure("", e)
		return 1
	}
	return undoDTBOnDrive(o, drive, jobs.Run)
}

// Use the same inspection, restore plan and guarded restore job as the app.
func undoDTBOnDrive(o opts, drive platform.Drive, runJob func(jobs.Request, func(flash.Progress)) (jobs.Result, error)) int {
	info, e := runJob(jobs.Request{Action: "dtb-info", Drive: drive}, nil)
	if e != nil {
		printFailure("", e)
		return 1
	}
	if info.Setup == nil || len(info.Setup.History) == 0 {
		fmt.Fprintln(os.Stderr, "No previous device-tree set is saved on this card. Nothing was changed.")
		return 1
	}
	if info.Card == nil || info.Card.Fingerprint == "" || info.BuildIdentity == "" {
		fmt.Fprintln(os.Stderr, "The card could not be identified. Nothing was changed.")
		return 1
	}
	req := jobs.Request{Action: "dtb-restore-plan", Drive: drive, DTBExpected: info.BuildIdentity, CardFingerprint: info.Card.Fingerprint}
	planned, e := runJob(req, nil)
	if e != nil {
		printFailure("", e)
		return 1
	}
	if planned.DTBPlan == nil {
		fmt.Fprintln(os.Stderr, "The previous device-tree set could not be checked. Nothing was changed.")
		return 1
	}
	if !o.assumeYes && !yes(o.in, "Put the previous device-tree files back? [y/N]: ", false) {
		return 0
	}
	if !cliConfirmDTBScreen(o, planned.DTBPlan.ScreenChanged) {
		return 0
	}
	req.DTBOptions.ScreenAccepted = planned.DTBPlan.ScreenChanged
	req.Action = "dtb-restore"
	if _, e = runJob(req, func(p flash.Progress) { printProgress(p) }); e != nil {
		printFailure("", e)
		return 1
	}
	fmt.Println("Previous device trees are back.")
	return 0
}
