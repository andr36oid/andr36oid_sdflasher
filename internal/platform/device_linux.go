package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"strings"
	"time"
	"unsafe"
)

type block struct {
	Name, Path, Type, Model, Serial, Tran string
	Size                                  int64
	RM                                    bool
	RO                                    bool
	Sector                                int64     `json:"log-sec"`
	FSTypeJSON                            *string   `json:"fstype"`
	PartLabel                             *string   `json:"partlabel"`
	Mounts                                []*string `json:"mountpoints"`
	Children                              []block
}

func blocks() ([]block, error) {
	b, e := hostCommand("lsblk", "--bytes", "--json", "--output", "NAME,PATH,TYPE,SIZE,MODEL,SERIAL,RM,TRAN,MOUNTPOINTS,LOG-SEC,RO,FSTYPE,PARTLABEL").Output()
	if e != nil {
		return nil, e
	}
	var v struct{ Blockdevices []block }
	e = json.Unmarshal(b, &v)
	return v.Blockdevices, e
}
func critical(b block) bool {
	for _, m := range b.Mounts {
		if m == nil {
			continue
		}
		s := *m
		if s == "/" || s == "/home" || s == "/usr" || s == "/var" || s == "/boot" || strings.HasPrefix(s, "/boot/") || s == "[SWAP]" {
			return true
		}
	}
	for _, c := range b.Children {
		if critical(c) {
			return true
		}
	}
	return false
}

// members: filesystem types that mean the disk belongs to something running,
// though nothing on it shows a mount point
var members = map[string]bool{"zfs_member": true, "LVM2_member": true, "linux_raid_member": true, "crypto_LUKS": true, "bcache": true, "swap": true, "ddf_raid_member": true, "isw_raid_member": true, "ceph_bluestore": true}

// inUse: something on the disk is mounted, belongs to ZFS, LVM, RAID, LUKS or
// bcache, is swap, or has device-mapper or RAID devices on top. Only fixed disks
// are checked this way: they are never safe to offer when any of that is true.
func inUse(b block) bool {
	for _, m := range b.Mounts {
		if m != nil && *m != "" {
			return true
		}
	}
	if b.FSTypeJSON != nil && members[*b.FSTypeJSON] {
		return true
	}
	for _, c := range b.Children {
		if c.Type != "part" || inUse(c) {
			return true
		}
	}
	return false
}

// claimed: as root, the kernel refuses an exclusive open of a disk that a mount,
// ZFS, RAID, LVM or LUKS holds, also where lsblk can't name the filesystem
// (no udev). Unprivileged it can't tell, and the write's own exclusive open is the
// last guard.
func claimed(b block) bool {
	if os.Geteuid() != 0 {
		return false
	}
	f, e := os.OpenFile(b.Path, os.O_RDONLY|unix.O_EXCL, 0)
	if e != nil {
		return errors.Is(e, unix.EBUSY)
	}
	f.Close()
	return false
}

// unmount v for writing. A file manager or indexer may hold it for a moment, so a few
// tries; still busy after that is a BusyPrefix error, and the user is asked to close
// what uses it. Never a lazy unmount: the file system could still write to the card
// after the image.
func unmount(v string) error {
	var out []byte
	for i := 0; i < 5; i++ {
		var e error
		if out, e = exec.Command("umount", "--", v).CombinedOutput(); e == nil || !mounted(v) {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("%s%s (%s)", BusyPrefix, v, strings.TrimSpace(string(out)))
}

func mounted(v string) bool {
	b, e := os.ReadFile("/proc/self/mounts")
	if e != nil {
		return true
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == v {
			return true
		}
	}
	return false
}

// sdSlot: an SD card in a built-in reader (PCIe readers such as Realtek's rtsx,
// laptop and SBC slots). The kernel calls those cards not removable, but the card
// type is SD; built-in eMMC storage is MMC.
func sdSlot(b block) bool {
	if b.Tran != "mmc" {
		return false
	}
	t, e := os.ReadFile("/sys/block/" + b.Name + "/device/type")
	return e == nil && strings.TrimSpace(string(t)) == "SD"
}

// virtual block devices that are other machines' disks: ZFS volumes, network block devices
func virtual(b block) bool {
	for _, p := range []string{"zd", "nbd", "rbd", "drbd"} {
		if strings.HasPrefix(b.Name, p) {
			return true
		}
	}
	return false
}

func list(all bool) ([]Drive, error) {
	bs, e := blocks()
	if e != nil {
		return nil, e
	}
	return drives(bs, all), nil
}

// drives are the disks of lsblk's list that are offered.
func drives(bs []block, all bool) []Drive {
	var out []Drive
	for _, b := range bs {
		fixed := !b.RM && b.Tran != "usb" && !sdSlot(b)
		// a card with its lock switch on is read only; it is listed (not usable), a
		// read-only fixed disk is not
		if b.Type != "disk" || (fixed && (b.RO || !all || inUse(b) || virtual(b) || claimed(b))) || critical(b) || b.Size < 1<<30 {
			continue
		}
		d := Drive{Path: b.Path, Name: strings.TrimSpace(b.Model), Serial: strings.TrimSpace(b.Serial), Size: b.Size, SectorSize: b.Sector, Fixed: fixed, WriteProtected: b.RO,
			// USB hard drives and SSDs are not removable; card readers are
			External: b.Tran == "usb" && !b.RM}
		if d.Name == "" {
			d.Name = b.Name
		}
		var add func(block)
		add = func(x block) {
			if x.PartLabel != nil && *x.PartLabel != "" {
				d.Labels = append(d.Labels, *x.PartLabel)
			}
			for _, m := range x.Mounts {
				if m != nil && *m != "" {
					d.Volumes = append(d.Volumes, x.Path)
					break
				}
			}
			for _, c := range x.Children {
				add(c)
			}
		}
		add(b)
		out = append(out, d)
	}
	return out
}
func Open(d Drive, write bool) (*Handle, error) {
	d, e := Revalidate(d)
	if e != nil {
		return nil, e
	}
	if write && d.WriteProtected {
		return nil, errWriteProtected
	}
	if write {
		for _, v := range d.Volumes {
			if e := unmount(v); e != nil {
				return nil, e
			}
		}
	}
	flag := os.O_RDONLY
	if write {
		flag = os.O_RDWR | unix.O_EXCL
	}
	f, e := os.OpenFile(d.Path, flag, 0)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	var size uint64
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), unix.BLKGETSIZE64, uintptr(unsafe.Pointer(&size)))
	if errno != 0 {
		e = errno
	}
	if e != nil || int64(size) != d.Size {
		f.Close()
		return nil, errCapacityChanged
	}
	return &Handle{File: f, Cleanup: func() {
		if write {
			exec.Command("blockdev", "--rereadpt", d.Path).Run()
		}
	}}, nil
}
