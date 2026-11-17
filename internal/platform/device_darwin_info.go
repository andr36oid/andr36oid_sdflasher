package platform

import "strings"

// The parts of the macOS listing that only read diskutil's answers; they build
// everywhere so that the tests run everywhere.

// darwinDrive is the drive "diskutil info -plist" describes for disk id, ok false
// for one that isn't listed: a partition, a disk under 1 GiB, a fixed internal disk
// (unless all), or a read-only fixed one.
func darwinDrive(id string, inf map[string]any, all bool) (Drive, bool) {
	internal, _ := inf["Internal"].(bool)
	ejectable, _ := inf["Ejectable"].(bool)
	removable, _ := inf["RemovableMediaOrExternalDevice"].(bool)
	removableMedia, _ := inf["RemovableMedia"].(bool)
	writable, _ := inf["Writable"].(bool)
	size, _ := inf["TotalSize"].(float64)
	// A card in a built-in reader is Internal but ejectable/removable; only a
	// truly fixed internal disk (the system SSD) is hidden unless ListAll asks.
	fixed := internal && !ejectable && !removable
	if (fixed && (!all || !writable)) || !wholeDisk(inf) || int64(size) < 1<<30 {
		return Drive{}, false
	}
	sec, _ := inf["DeviceBlockSize"].(float64)
	name, _ := inf["MediaName"].(string)
	serial, _ := inf["DiskUUID"].(string)
	return Drive{Path: "/dev/" + id, Name: name, Serial: serial, Size: int64(size), SectorSize: int64(sec), Fixed: fixed,
		// a card with its lock switch on is listed, but not writable
		WriteProtected: !writable,
		// an external disk whose medium can't be taken out: a USB hard drive or SSD
		External: !internal && !removableMedia}, true
}

// wholeDisk reports whether "diskutil info -plist" describes a whole disk rather
// than a partition. diskutil names the key "WholeDisk"; there is no "Whole" key,
// and reading only that one skipped every disk, so no card was ever listed.
func wholeDisk(inf map[string]any) bool {
	if v, ok := inf["WholeDisk"].(bool); ok {
		return v
	}
	v, _ := inf["Whole"].(bool)
	return v
}

// mountedFrom: the output of mount(8) has a volume of disk (/dev/diskN) mounted.
func mountedFrom(mounts, disk string) bool {
	for _, line := range strings.Split(mounts, "\n") {
		dev, _, _ := strings.Cut(line, " ")
		if strings.HasPrefix(dev, disk+"s") || dev == disk {
			return true
		}
	}
	return false
}
