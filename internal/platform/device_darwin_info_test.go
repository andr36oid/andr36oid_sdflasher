package platform

import "testing"

func TestWholeDisk(t *testing.T) {
	// Keys as "diskutil info -plist" prints them for a card and for one of its partitions.
	card := map[string]any{"WholeDisk": true, "Writable": true, "Ejectable": true, "RemovableMediaOrExternalDevice": true}
	part := map[string]any{"WholeDisk": false, "Writable": true, "Ejectable": true, "RemovableMediaOrExternalDevice": true}
	if !wholeDisk(card) {
		t.Fatal("whole disk with WholeDisk=true not recognised")
	}
	if wholeDisk(part) {
		t.Fatal("partition with WholeDisk=false taken for a whole disk")
	}
	if !wholeDisk(map[string]any{"Whole": true}) {
		t.Fatal("legacy Whole key not honoured")
	}
	if wholeDisk(map[string]any{}) {
		t.Fatal("disk without either key taken for a whole disk")
	}
}

// macOS: a locked card is listed write-protected, a USB hard drive as external, a
// disk under 1 GiB not at all (as on Linux and Windows).
func TestDarwinDrive(t *testing.T) {
	card := map[string]any{"WholeDisk": true, "Writable": true, "Internal": false, "Ejectable": true, "RemovableMedia": true, "RemovableMediaOrExternalDevice": true, "TotalSize": 31914983424.0, "DeviceBlockSize": 512.0, "MediaName": "SD Card Reader"}
	d, ok := darwinDrive("disk4", card, false)
	if !ok || d.WriteProtected || d.External || d.Path != "/dev/disk4" || d.Fixed {
		t.Fatalf("card: %+v %v", d, ok)
	}
	locked := map[string]any{"WholeDisk": true, "Writable": false, "Internal": true, "Ejectable": true, "RemovableMedia": true, "RemovableMediaOrExternalDevice": true, "TotalSize": 31914983424.0}
	if d, ok := darwinDrive("disk4", locked, false); !ok || !d.WriteProtected {
		t.Fatalf("locked card: %+v %v", d, ok)
	}
	hdd := map[string]any{"WholeDisk": true, "Writable": true, "Internal": false, "Ejectable": true, "RemovableMedia": false, "RemovableMediaOrExternalDevice": true, "TotalSize": 2e12}
	if d, ok := darwinDrive("disk5", hdd, false); !ok || !d.External || !d.Large() {
		t.Fatalf("USB hard drive: %+v %v", d, ok)
	}
	small := map[string]any{"WholeDisk": true, "Writable": true, "Ejectable": true, "RemovableMedia": true, "RemovableMediaOrExternalDevice": true, "TotalSize": float64(512 << 20)}
	if _, ok := darwinDrive("disk6", small, false); ok {
		t.Fatal("a disk under 1 GiB is listed")
	}
	ssd := map[string]any{"WholeDisk": true, "Writable": true, "Internal": true, "Ejectable": false, "RemovableMedia": false, "TotalSize": 5e11}
	if _, ok := darwinDrive("disk0", ssd, false); ok {
		t.Fatal("the internal SSD is listed")
	}
}

func TestMountedFrom(t *testing.T) {
	out := "/dev/disk3s1s1 on / (apfs, sealed)\n/dev/disk41s1 on /Volumes/X (msdos)\n/dev/disk4s2 on /Volumes/BOOT (msdos, local)\n"
	if !mountedFrom(out, "/dev/disk4") || mountedFrom(out, "/dev/disk5") || mountedFrom("/dev/disk41s1 on /Volumes/X (msdos)\n", "/dev/disk4") {
		t.Fatal("mount list misread")
	}
}
