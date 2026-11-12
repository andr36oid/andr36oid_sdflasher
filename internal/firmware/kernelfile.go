package firmware

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"

	"github.com/diskfs/go-diskfs/filesystem"
)

// From the hybrid release on BOOT holds the kernel compressed: Image.gz and a
// 52-byte Image.gz.meta that boot.ini checks before it unpacks (sizes and
// CRC-32 of both forms, and the load fields of the kernel's header). Older
// images have the kernel as Image.
const (
	KernelImageFile = "Image"
	KernelGzipFile  = "Image.gz"
	KernelMetaFile  = "Image.gz.meta"

	kernelGzipLimit = 16 << 20
	kernelRawLimit  = 64 << 20
)

// checkKernel says whether BOOT has a kernel the console's boot script loads:
// the compressed pair when it is there, complete and matching, or else Image.
func checkKernel(boot filesystem.FileSystem) error {
	meta, e := ReadFile(boot, "/"+KernelMetaFile, 4096)
	if e != nil {
		// Half a compressed pair is a broken image even when a plain Image sits
		// beside it: the boot script would try the pair first.
		if onBoot(boot, "/"+KernelMetaFile) {
			return errors.New("invalid Image.gz.meta")
		}
		if onBoot(boot, "/"+KernelGzipFile) {
			return errors.New("Image.gz without Image.gz.meta")
		}
		if b, e := ReadFile(boot, "/"+KernelImageFile, 128<<20); e != nil || len(b) < 4096 {
			return errors.New("missing or truncated Image")
		}
		return nil
	}
	if len(meta) != 52 || !bytes.Equal(meta[:8], []byte("A36KGZ1\x00")) ||
		crc32.ChecksumIEEE(meta[:48]) != binary.BigEndian.Uint32(meta[48:]) {
		return errors.New("invalid Image.gz.meta")
	}
	size, raw := binary.LittleEndian.Uint32(meta[8:]), binary.LittleEndian.Uint32(meta[12:])
	if size < 4096 || size > kernelGzipLimit || raw < 4096 || raw > kernelRawLimit {
		return errors.New("invalid Image.gz.meta")
	}
	gz, e := ReadFile(boot, "/"+KernelGzipFile, kernelGzipLimit)
	if e != nil || uint32(len(gz)) != size {
		return errors.New("missing or truncated Image.gz")
	}
	// plain gzip, deflate, no optional header fields: all the boot script accepts
	if gz[0] != 0x1f || gz[1] != 0x8b || gz[2] != 8 || gz[3] != 0 ||
		crc32.ChecksumIEEE(gz) != binary.BigEndian.Uint32(meta[16:]) {
		return errors.New("Image.gz does not match Image.gz.meta")
	}
	return nil
}

// onBoot says whether a file exists, whatever its size.
func onBoot(boot filesystem.FileSystem, path string) bool {
	f, e := boot.OpenFile(path, os.O_RDONLY)
	if e != nil {
		return false
	}
	f.Close()
	return true
}
