package cli

import (
	"context"
	"errors"
	"fmt"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"io"
	"os"
)

func cliBootloader(o opts) (*firmware.BootloaderChoice, error) {
	if o.bootChain != "" && o.bootChain != "standard" && o.bootChain != "mainline" {
		return nil, errors.New("--boot-chain must be standard or mainline")
	}
	c := &firmware.BootloaderChoice{Chain: o.bootChain, Sleep: o.sleepFirmware, Force: o.forceBootOptions, Choice: o.bootloader}
	if o.bootloaderFile != "" {
		if o.bootloader != "" {
			return nil, errors.New("choose --bootloader or --bootloader-file, not both")
		}
		c.Choice = "file"
		c.File = o.bootloaderFile
	}
	if c.Choice == "" {
		c.Choice = "image"
	}
	if c.Choice != "image" && c.Choice != "standard" && c.Choice != "new" && c.Choice != "file" {
		return nil, errors.New("--bootloader must be image, standard or new")
	}
	if o.bootloader == "" && o.bootloaderFile == "" && o.bootChain == "" && o.sleepFirmware == "" {
		return nil, nil
	}
	if c.Changed() && c.Chain == "" {
		c.Chain = "standard"
	}
	if c.ChainID() == "mainline" {
		for _, issue := range firmware.EvaluateBootOptions(*c, firmware.BootTarget{Storage: "sd", ImageAvailable: true}, firmware.BootRules()) {
			if issue.Level == "impossible" {
				return nil, errors.New(issue.Reason)
			}
		}
	}
	return c, nil
}
func printBootloader(out *firmware.BootloaderOutcome) {
	if out == nil {
		return
	}
	if out.Options != nil {
		fmt.Printf("Boot chain: %s; sleep firmware: %s\n", out.Options.Chain, out.Options.Sleep)
	}
	if out.Warning != "" {
		fmt.Println(out.Warning)
		return
	}
	if out.Record == nil {
		return
	}
	fmt.Println(firmware.BootloaderDescription(&firmware.BootloaderBuild{Choice: out.Record.Choice, Version: out.Record.Version, Tag: out.Record.Tag}))
}
func runBootloader(o opts) int {
	choice, e := cliBootloader(o)
	if e != nil {
		printFailure("", e)
		return 2
	}
	if choice == nil {
		fmt.Fprintln(os.Stderr, "Choose --bootloader image|standard|new, --boot-chain standard|mainline or --sleep-firmware image|<id>.")
		return 2
	}
	drive, e := pickDrive(o.in, o.all, o.drive)
	if e != nil {
		printFailure("", e)
		return 1
	}
	info, e := jobs.Run(jobs.Request{Action: "bootloader-info", Drive: drive}, nil)
	if e != nil {
		printFailure("", e)
		return 1
	}
	if drive.Fixed || info.Card == nil || info.Card.DataCard {
		fmt.Fprintln(os.Stderr, firmware.BootloaderOnlySD)
		return 1
	}
	target := firmware.BootTarget{Storage: "sd"}
	if info.BootTarget != nil {
		target = *info.BootTarget
	}
	target.ImageAvailable = o.imagePath != ""
	if e = cliCheckBootOptions(*choice, target); e != nil {
		printFailure("", e)
		return 1
	}
	if !o.assumeYes && !yes(o.in, "Write the selected boot options on this card? [y/N]: ", false) {
		return 0
	}
	req := jobs.Request{Action: "bootloader-apply", Drive: drive, Bootloader: choice, BootloaderExpected: info.BuildIdentity, CardFingerprint: info.Card.Fingerprint, Image: o.imagePath}
	out, e := jobs.Run(req, func(p flash.Progress) { printProgress(p) })
	if e != nil {
		printFailure("", e)
		return 1
	}
	printBootloader(out.Bootloader)
	return 0
}

// Warn before the confirmation/write, using the writer's own table. The helper
// rechecks it after any network/card change; this preflight never grants consent.
func cliCheckBootOptions(c firmware.BootloaderChoice, t firmware.BootTarget) error {
	var catalog *firmware.BootCatalog
	if c.SleepID() != "image" {
		var e error
		catalog, e = firmware.FetchBootCatalog(context.Background())
		if e != nil {
			return e
		}
	}
	return cliBootIssues(c, t, firmware.CatalogBootRules(catalog), os.Stderr)
}
func cliBootIssues(c firmware.BootloaderChoice, t firmware.BootTarget, rules []firmware.BootRule, w io.Writer) error {
	issues, e := firmware.CheckBootOptions(c, t, rules)
	if e != nil {
		return e
	}
	for _, issue := range issues {
		if issue.Level == "untested" {
			fmt.Fprintln(w, "Warning:", issue.Reason)
		}
	}
	return nil
}
