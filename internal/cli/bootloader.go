package cli

import (
	"errors"
	"fmt"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"os"
)

func cliBootloader(o opts) (*firmware.BootloaderChoice, error) {
	if o.bootloaderFile != "" {
		if o.bootloader != "" {
			return nil, errors.New("choose --bootloader or --bootloader-file, not both")
		}
		return &firmware.BootloaderChoice{Choice: "file", File: o.bootloaderFile}, nil
	}
	switch o.bootloader {
	case "":
		return nil, nil
	case "image", "standard", "new":
		return &firmware.BootloaderChoice{Choice: o.bootloader}, nil
	}
	return nil, errors.New("--bootloader must be image, standard or new")
}
func printBootloader(out *firmware.BootloaderOutcome) {
	if out == nil {
		return
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
	if e != nil || choice == nil {
		fmt.Fprintln(os.Stderr, "Choose --bootloader image|standard|new or --bootloader-file uboot.img.")
		return 2
	}
	drive, e := pickDrive(o.in, o.all, o.drive)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	info, e := jobs.Run(jobs.Request{Action: "bootloader-info", Drive: drive}, nil)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	if drive.Fixed || info.Card == nil || info.Card.DataCard {
		fmt.Fprintln(os.Stderr, firmware.BootloaderOnlySD)
		return 1
	}
	if !o.assumeYes && !yes(o.in, "Write only the U-Boot slot on this card? [y/N]: ", false) {
		return 0
	}
	req := jobs.Request{Action: "bootloader-apply", Drive: drive, Bootloader: choice, BootloaderExpected: info.BuildIdentity, CardFingerprint: info.Card.Fingerprint, Image: o.imagePath}
	out, e := jobs.Run(req, func(p flash.Progress) { printProgress(p) })
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	printBootloader(out.Bootloader)
	return 0
}
