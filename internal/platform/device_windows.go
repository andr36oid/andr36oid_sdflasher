package platform

import (
	"encoding/json"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"strings"
	"unsafe"
)

func list(all bool) ([]Drive, error) {
	// fixed: any bus but USB, SD and MMC; only listed with all
	removable := ` -and $_.BusType -in @('USB','SD','MMC')`
	if all {
		removable = ""
	}
	// Enumerate each disk in its own try/catch so one disk that errors (a reader
	// with no card, a provider hiccup) can't abort the whole list, and always
	// emit a JSON array so a single match still unmarshals. Get-Disk itself stays
	// under Stop, so a real storage-provider failure still surfaces (with its
	// message, captured from stderr below) instead of a bare "exit status 1".
	script := `$ErrorActionPreference='Stop'; $d=@(); foreach ($k in @(Get-Disk | Where-Object { -not $_.IsBoot -and -not $_.IsSystem -and -not $_.IsReadOnly -and $_.Size -ge 1GB` + removable + ` })) { try { $v=@(Get-Partition -DiskNumber $k.Number -ErrorAction SilentlyContinue | ForEach-Object { $p=$_; $g=$p | Get-Volume -ErrorAction SilentlyContinue; if ($g -and $g.Path) { $g.Path }; $p.AccessPaths | Where-Object { $_ -and $_.StartsWith('\\?\Volume{') } } | ForEach-Object { $_.TrimEnd('\') } | Where-Object { $_ } | Select-Object -Unique); $d += @{ path=('\\.\PhysicalDrive'+$k.Number); name=$k.FriendlyName; serial=$k.SerialNumber; size=[long]$k.Size; sector_size=[long]$k.LogicalSectorSize; volumes=$v; fixed=($k.BusType -notin @('USB','SD','MMC')) } } catch {} }; ConvertTo-Json -InputObject @($d) -Compress -Depth 4`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	hide(cmd)
	b, e := cmd.Output()
	if e != nil {
		// cmd.Stderr is nil, so ExitError carries PowerShell's own message; pass it
		// through instead of just "exit status 1".
		if ee, ok := e.(*exec.ExitError); ok {
			if msg := firstLine(string(ee.Stderr)); msg != "" {
				return nil, fmt.Errorf("%w: %s", e, msg)
			}
		}
		return nil, e
	}
	// ConvertTo-Json unwraps a one-element array to a lone object on some
	// PowerShell versions; accept either shape.
	var ds []Drive
	if trimmed := strings.TrimSpace(string(b)); trimmed == "" {
		return nil, nil
	} else if strings.HasPrefix(trimmed, "{") {
		var one Drive
		if e = json.Unmarshal(b, &one); e == nil {
			ds = []Drive{one}
		}
	} else {
		e = json.Unmarshal(b, &ds)
	}
	for i := range ds {
		ds[i].Serial = strings.TrimSpace(ds[i].Serial)
	}
	return ds, e
}

// firstLine returns the first non-empty, trimmed line of s, capped so a huge
// error can't flood the UI.
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			if len(ln) > 300 {
				ln = ln[:300]
			}
			return ln
		}
	}
	return ""
}
// Disk IOCTLs and the SET_DISK_ATTRIBUTES layout (ntdddisk.h). Taking the disk
// offline force-dismounts every volume and, unlike per-volume locking, stops
// Windows from auto-mounting any partition for as long as it stays offline,
// including partitions this write creates. Version/Persist/Attributes offsets
// line up on both amd64 and 386 (Version 4 + Persist 4 puts Attributes at 8).
const (
	ioctlSetDiskAttributes = 0x0007C0F4 // IOCTL_DISK_SET_DISK_ATTRIBUTES
	ioctlUpdateProperties  = 0x00070140 // IOCTL_DISK_UPDATE_PROPERTIES
	diskAttributeOffline   = 0x0000000000000001
)

type setDiskAttributes struct {
	Version        uint32
	Persist        uint32 // BOOLEAN Persist + BYTE Reserved1[3]; left 0 so it never persists across reboot
	Attributes     uint64
	AttributesMask uint64
	Reserved2      [4]uint32
}

// setDiskOffline marks the physical disk offline (or back online) and makes
// Windows re-read it so the change takes effect (dismount on offline, remount on
// online). The handle must be opened for read and write.
func setDiskOffline(h windows.Handle, offline bool) error {
	attr := setDiskAttributes{Version: uint32(unsafe.Sizeof(setDiskAttributes{})), AttributesMask: diskAttributeOffline}
	if offline {
		attr.Attributes = diskAttributeOffline
	}
	var n uint32
	e := windows.DeviceIoControl(h, ioctlSetDiskAttributes, (*byte)(unsafe.Pointer(&attr)), uint32(unsafe.Sizeof(attr)), nil, 0, &n, nil)
	if e != nil {
		return e
	}
	// Best effort: a reader that took the attribute should also accept the re-read.
	windows.DeviceIoControl(h, ioctlUpdateProperties, nil, 0, nil, 0, &n, nil)
	return nil
}

func Open(d Drive, write bool) (*Handle, error) {
	d, e := Revalidate(d)
	if e != nil {
		return nil, e
	}
	var volumes []windows.Handle
	offlined := false
	path := d.Path
	cleanup := func() {
		for _, h := range volumes {
			windows.CloseHandle(h)
		}
		// Bring the disk back online so Windows re-reads the new table and mounts
		// the card for the user. Done on a fresh handle because the write handle
		// is closed first by Handle.Close.
		if offlined {
			if p, e := windows.UTF16PtrFromString(path); e == nil {
				if rh, e := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0); e == nil {
					setDiskOffline(rh, false)
					windows.CloseHandle(rh)
				}
			}
		}
	}
	p, e := windows.UTF16PtrFromString(d.Path)
	if e != nil {
		cleanup()
		return nil, e
	}
	access := uint32(windows.GENERIC_READ)
	if write {
		access |= windows.GENERIC_WRITE
	}
	h, e := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_WRITE_THROUGH, 0)
	if e != nil {
		cleanup()
		return nil, e
	}
	// Validate capacity while the disk is still online; geometry queries are most
	// reliable before any offline/dismount below.
	var n uint32
	var size int64
	if e = windows.DeviceIoControl(h, 0x7405c, nil, 0, (*byte)(unsafe.Pointer(&size)), 8, &n, nil); e != nil || size != d.Size {
		windows.CloseHandle(h)
		cleanup()
		return nil, fmt.Errorf("card capacity changed")
	}
	if write {
		// Preferred path: take the whole disk offline. This clears every mounted
		// volume at once and keeps Windows from re-mounting a partition mid-write
		// (the raw image and the raw FAT writes to BOOT would otherwise be denied,
		// especially when adding hotfixes after a fresh install re-creates the
		// partitions). Fall back to per-volume lock and dismount for a reader whose
		// driver rejects SET_DISK_ATTRIBUTES.
		if setDiskOffline(h, true) == nil {
			offlined = true
		} else {
			for _, v := range d.Volumes {
				vp, e := windows.UTF16PtrFromString(v)
				if e != nil {
					windows.CloseHandle(h)
					cleanup()
					return nil, e
				}
				vh, e := windows.CreateFile(vp, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
				if e != nil {
					windows.CloseHandle(h)
					cleanup()
					return nil, e
				}
				volumes = append(volumes, vh)
				var n uint32
				for _, op := range []uint32{0x90018, 0x90020} { // FSCTL_LOCK_VOLUME, FSCTL_DISMOUNT_VOLUME
					if e = windows.DeviceIoControl(vh, op, nil, 0, nil, 0, &n, nil); e != nil {
						windows.CloseHandle(h)
						cleanup()
						return nil, fmt.Errorf("%s%s (%w)", BusyPrefix, v, e)
					}
				}
			}
		}
	}
	f := os.NewFile(uintptr(h), d.Path)
	return &Handle{File: f, Cleanup: cleanup}, nil
}
