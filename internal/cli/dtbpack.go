package cli

import (
	"encoding/json"
	"fmt"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"os"
)

func runDTBs(o opts, args []string) int {
	if len(args) < 2 || args[0] != "info" && args[0] != "apply" {
		fmt.Fprintln(os.Stderr, "Usage: -cli dtbs info <zip> | -cli [--profile <id>] [--replace-anyway] dtbs apply <zip> <card>")
		return 2
	}
	p, e := firmware.ReadDTBPack(args[1])
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
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
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	info, e := jobs.Run(jobs.Request{Action: "dtb-info", Drive: d}, nil)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	req := jobs.Request{Action: "dtb-plan", Drive: d, DTBFile: args[1], DTBExpected: info.BuildIdentity, CardFingerprint: info.Card.Fingerprint, DTBOptions: firmware.DTBOptions{Profile: o.profile, ReplaceAnyway: o.replaceDTBs}}
	planned, e := jobs.Run(req, nil)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
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
		fmt.Fprintln(os.Stderr, e)
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
