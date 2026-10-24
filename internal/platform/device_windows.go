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
	script := `$ErrorActionPreference='Stop'; $d=@(); foreach ($k in @(Get-Disk | Where-Object { -not $_.IsBoot -and -not $_.IsSystem -and -not $_.IsReadOnly -and $_.Size -ge 1GB` + removable + ` })) { try { $v=@(Get-Partition -DiskNumber $k.Number -ErrorAction SilentlyContinue | ForEach-Object { $_.AccessPaths } | Where-Object { $_ -and $_.StartsWith('\\?\Volume{') } | ForEach-Object { $_.TrimEnd('\') }); $d += @{ path=('\\.\PhysicalDrive'+$k.Number); name=$k.FriendlyName; serial=$k.SerialNumber; size=[long]$k.Size; sector_size=[long]$k.LogicalSectorSize; volumes=$v; fixed=($k.BusType -notin @('USB','SD','MMC')) } } catch {} }; ConvertTo-Json -InputObject @($d) -Compress -Depth 4`
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
func Open(d Drive, write bool) (*Handle, error) {
	d, e := Revalidate(d)
	if e != nil {
		return nil, e
	}
	var volumes []windows.Handle
	cleanup := func() {
		for _, h := range volumes {
			windows.CloseHandle(h)
		}
	}
	if write {
		for _, v := range d.Volumes {
			p, e := windows.UTF16PtrFromString(v)
			if e != nil {
				cleanup()
				return nil, e
			}
			h, e := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
			if e != nil {
				cleanup()
				return nil, e
			}
			volumes = append(volumes, h)
			var n uint32
			for _, op := range []uint32{0x90018, 0x90020} {
				if e = windows.DeviceIoControl(h, op, nil, 0, nil, 0, &n, nil); e != nil {
					cleanup()
					return nil, fmt.Errorf("%s%s (%w)", BusyPrefix, v, e)
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
	var n uint32
	var size int64
	if e = windows.DeviceIoControl(h, 0x7405c, nil, 0, (*byte)(unsafe.Pointer(&size)), 8, &n, nil); e != nil || size != d.Size {
		windows.CloseHandle(h)
		cleanup()
		return nil, fmt.Errorf("card capacity changed")
	}
	f := os.NewFile(uintptr(h), d.Path)
	return &Handle{File: f, Cleanup: cleanup}, nil
}
