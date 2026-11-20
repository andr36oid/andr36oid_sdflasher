package firmware

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"regexp"

	"github.com/diskfs/go-diskfs/filesystem"
)

// What a card says about the kernel line and graphics driver of the andr36oid it
// runs. The image builder (device/gameconsole/r36s/mkimg.sh) writes kernel_line and
// gpu_variant into andr36oid-build-v2.txt since the hybrid releases (2026-11-12).
// Older cards have andr36oid-build.txt (format 1, from 2026-10-18) or no file at all,
// and neither key: their kernel line is read from the kernel's own version string,
// their graphics driver is not known (it is told apart only on vendor, an EROFS
// partition this flasher does not read).
const (
	VariantFromInfo   = "info"   // andr36oid-build-v2.txt
	VariantFromKernel = "kernel" // the version string of the kernel on BOOT
)

// CardVariant is the kernel line ("4.19", "5.10") and graphics driver ("gles",
// "vulkan") of the andr36oid on a card's BOOT; "" for what could not be read. From is
// where the kernel line came from (VariantFromInfo, VariantFromKernel or "").
type CardVariant struct {
	Kernel   string
	Graphics string
	From     string
}

// ReadCardVariant reads the card's build file first, then the kernel's version.
func ReadCardVariant(boot filesystem.FileSystem) CardVariant {
	if b, e := ReadFile(boot, "/"+BuildInfoV2File, 64<<10); e == nil {
		m, _ := ParseBuildInfo(b)
		k, g := m["kernel_line"], m["gpu_variant"]
		if m["format"] == "2" && (k == "4.19" || k == "5.10") {
			v := CardVariant{Kernel: k, From: VariantFromInfo}
			if g == "gles" || g == "vulkan" {
				v.Graphics = g
			}
			return v
		}
	}
	if k := bootKernelLine(boot); k != "" {
		return CardVariant{Kernel: k, From: VariantFromKernel}
	}
	return CardVariant{}
}

// bootKernelLine finds "Linux version 4.19..." or "5.10..." in BOOT's kernel:
// Image.gz (the hybrid releases on) or Image (before). "" when neither has it.
func bootKernelLine(boot filesystem.FileSystem) string {
	if f, e := boot.OpenFile("/"+KernelGzipFile, os.O_RDONLY); e == nil {
		defer f.Close()
		if z, e := gzip.NewReader(io.LimitReader(f, kernelGzipLimit)); e == nil {
			if k := KernelLine(io.LimitReader(z, kernelRawLimit)); k != "" {
				return k
			}
		}
	}
	if f, e := boot.OpenFile("/"+KernelImageFile, os.O_RDONLY); e == nil {
		defer f.Close()
		return KernelLine(io.LimitReader(f, kernelRawLimit))
	}
	return ""
}

var linuxBanner = regexp.MustCompile(`Linux version (\d+)\.(\d+)\.`)

// KernelLine reads an uncompressed kernel until its banner ("Linux version
// 5.10.198-rk3326-itskenny0 ...") and returns "4.19" or "5.10"; "" for another or no
// version.
func KernelLine(r io.Reader) string {
	const chunk, keep = 1 << 20, 64
	buf := make([]byte, 0, chunk+keep)
	tmp := make([]byte, chunk)
	for {
		n, e := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if i := bytes.Index(buf, []byte("Linux version ")); i >= 0 && (len(buf)-i >= 32 || e != nil) {
			if m := linuxBanner.FindSubmatch(buf[i:]); m != nil {
				switch string(m[1]) + "." + string(m[2]) {
				case "4.19":
					return "4.19"
				case "5.10":
					return "5.10"
				}
				return ""
			}
		}
		if e != nil {
			return ""
		}
		if len(buf) > keep {
			// keep the tail: the banner may straddle two reads
			buf = append(buf[:0], buf[len(buf)-keep:]...)
		}
	}
}
