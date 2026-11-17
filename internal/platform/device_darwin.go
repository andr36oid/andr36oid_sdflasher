package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

func diskInfo(args ...string) (map[string]any, error) {
	b, e := exec.Command("/usr/sbin/diskutil", args...).Output()
	if e != nil {
		return nil, e
	}
	cmd := exec.Command("/usr/bin/plutil", "-convert", "json", "-o", "-", "--", "-")
	cmd.Stdin = strings.NewReader(string(b))
	b, e = cmd.Output()
	if e != nil {
		return nil, e
	}
	var v map[string]any
	e = json.Unmarshal(b, &v)
	return v, e
}
func list(all bool) ([]Drive, error) {
	// A built-in SD card reader sits on an internal bus, so "diskutil list
	// external" never lists the card. List every physical disk and keep only
	// removable media below; the disk the computer runs from stays protected.
	args := []string{"list", "-plist", "physical"}
	v, e := diskInfo(args...)
	if e != nil {
		return nil, e
	}

	protected := map[string]bool{}
	root, e := diskInfo("info", "-plist", "/")
	if e != nil {
		return nil, e
	}
	rootID, _ := root["ParentWholeDisk"].(string)
	protected[rootID] = true
	apfs, e := diskInfo("apfs", "list", "-plist")
	if e != nil {
		return nil, fmt.Errorf("cannot identify the system disk: %w", e)
	}
	containers, _ := apfs["Containers"].([]any)
	base := regexp.MustCompile(`^disk[0-9]+`)
	for _, entry := range containers {
		c, _ := entry.(map[string]any)
		ref, _ := c["ContainerReference"].(string)
		system := ref == rootID
		volumes, _ := c["Volumes"].([]any)
		for _, v := range volumes {
			vm, _ := v.(map[string]any)
			mp, _ := vm["MountPoint"].(string)
			if mp == "/" || strings.HasPrefix(mp, "/System/Volumes/") {
				system = true
			}
		}
		if system {
			stores, _ := c["PhysicalStores"].([]any)
			for _, store := range stores {
				sm, _ := store.(map[string]any)
				id, _ := sm["DeviceIdentifier"].(string)
				protected[base.FindString(id)] = true
			}
		}
	}
	var out []Drive
	disks, _ := v["AllDisksAndPartitions"].([]any)
	for _, entry := range disks {
		m, _ := entry.(map[string]any)
		id, _ := m["DeviceIdentifier"].(string)
		if id == "" || protected[id] {
			continue
		}
		inf, e := diskInfo("info", "-plist", id)
		if e != nil {
			continue
		}
		if d, ok := darwinDrive(id, inf, all); ok {
			out = append(out, d)
		}
	}
	return out, nil
}

func Open(d Drive, write bool) (*Handle, error) {
	d, e := Revalidate(d)
	if e != nil {
		return nil, e
	}
	if write && d.WriteProtected {
		return nil, errWriteProtected
	}
	var lock *os.File
	if write {
		// Two flashers must not write one card: a lock file named after the disk
		// (the helper runs as root, so /var/run is the same for every user)
		if lock, e = lockDisk(d.Path); e != nil {
			return nil, e
		}
		if b, e := exec.Command("/usr/sbin/diskutil", "unmountDisk", d.Path).CombinedOutput(); e != nil {
			lock.Close()
			return nil, fmt.Errorf("%s%s (%s)", BusyPrefix, d.Path, strings.TrimSpace(string(b)))
		}
	}
	flag := os.O_RDONLY
	if write {
		// O_EXLOCK: also an exclusive lock on the disk itself, where the system
		// supports one on a device
		flag = os.O_RDWR | unix.O_EXLOCK | unix.O_NONBLOCK
	}
	raw := strings.Replace(d.Path, "/dev/disk", "/dev/rdisk", 1)
	f, e := os.OpenFile(raw, flag, 0)
	if write && e != nil && (errors.Is(e, unix.ENOTSUP) || errors.Is(e, unix.EOPNOTSUPP) || errors.Is(e, unix.EINVAL)) {
		f, e = os.OpenFile(raw, os.O_RDWR, 0)
	}
	if e != nil {
		if lock != nil {
			lock.Close()
		}
		if errors.Is(e, unix.EAGAIN) || errors.Is(e, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("%s%s (another program has it open for writing)", BusyPrefix, d.Path)
		}
		return nil, e
	}
	if write {
		// back to blocking I/O for the write itself
		unix.SetNonblock(int(f.Fd()), false)
	}
	h := &Handle{File: f}
	// macOS mounts the card's partitions again as soon as they appear (the new
	// partition table makes them appear); keep them unmounted until the end
	stop := func() {}
	if write {
		stop = keepUnmounted(d.Path)
	}
	h.Cleanup = func() {
		stop()
		if lock != nil {
			lock.Close()
		}
		if !write {
			return
		}
		if h.Remount {
			exec.Command("/usr/sbin/diskutil", "mountDisk", d.Path).Run()
		} else {
			exec.Command("/usr/sbin/diskutil", "eject", d.Path).Run()
		}
	}
	return h, nil
}

// lockDisk takes the flasher's lock on the disk at path (/dev/diskN): a lock file
// named after the disk, held with flock until it is closed. A second flasher gets a
// BusyPrefix error.
func lockDisk(path string) (*os.File, error) {
	name := "andr36oid-sdflasher-" + filepath.Base(path) + ".lock"
	var f *os.File
	var e error
	for _, dir := range []string{"/var/run", "/tmp"} {
		if f, e = os.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_CREATE, 0600); e == nil {
			break
		}
	}
	if e != nil {
		// no place for the lock: the exclusive open of the disk still guards it
		return nil, nil
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("%s%s (another andr36oid SD Flasher is writing this card)", BusyPrefix, path)
	}
	return f, nil
}

// keepUnmounted unmounts the disk's volumes again whenever macOS mounts one, until
// the returned function is called.
func keepUnmounted(disk string) func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				if out, e := exec.Command("/sbin/mount").Output(); e == nil && mountedFrom(string(out), disk) {
					exec.Command("/usr/sbin/diskutil", "unmountDisk", disk).Run()
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
		})
	}
}
