package firmware

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/diskfs/go-diskfs/filesystem"
)

// writeBootFile follows PrepareBoot's truncate/write pattern and checks the full
// file, including its length. Callers using a raw device must also sync and verify
// through a newly opened filesystem after committing their staged BOOT.
func writeBootFile(boot filesystem.FileSystem, name string, data []byte) error {
	f, err := boot.OpenFile(strings.TrimPrefix(name, "/"), os.O_CREATE|os.O_TRUNC|os.O_RDWR)
	if err != nil {
		return fmt.Errorf("open BOOT/%s: %w", name, err)
	}
	n, err := f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("write BOOT/%s: %w", name, err)
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	if closeErr != nil {
		return closeErr
	}
	return verifyBootFile(boot, name, data)
}
func verifyBootFile(boot filesystem.FileSystem, name string, want []byte) error {
	got, err := ReadFile(boot, "/"+strings.TrimPrefix(name, "/"), int64(len(want)))
	if err != nil {
		return fmt.Errorf("verify BOOT/%s: %w", name, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("BOOT/%s readback did not verify", name)
	}
	return nil
}

// commitBoot writes changed, sector-aligned runs only within the BOOT partition.
// This also supports raw Windows/macOS handles that reject unaligned I/O.
func commitBoot(disk HotfixDisk, staged *os.File, offset, size int64, progress func(int64, int64)) error {
	old, next := make([]byte, 1<<20), make([]byte, 1<<20)
	for off := int64(0); off < size; {
		n := min(int64(len(next)), size-off)
		if _, err := disk.ReadAt(old[:n], offset+off); err != nil {
			return err
		}
		if _, err := staged.ReadAt(next[:n], off); err != nil {
			return err
		}
		for sector := int64(0); sector < n; {
			if bytes.Equal(old[sector:sector+512], next[sector:sector+512]) {
				sector += 512
				continue
			}
			start := sector
			for sector < n && !bytes.Equal(old[sector:sector+512], next[sector:sector+512]) {
				sector += 512
			}
			count, err := disk.WriteAt(next[start:sector], offset+off+start)
			if err != nil {
				return err
			}
			if int64(count) != sector-start {
				return io.ErrShortWrite
			}
		}
		off += n
		if progress != nil {
			progress(off, size)
		}
	}
	return disk.Sync()
}
