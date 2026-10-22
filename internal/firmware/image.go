package firmware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"github.com/diskfs/go-diskfs/backend"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
)

type Profile struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Console string `json:"console"`
	// Other consoles whose list shows it too, e.g. a V12 wired like the G80
	AlsoFor      []string `json:"also_for,omitempty"`
	Experimental bool     `json:"experimental"`
	Kernel       string   `json:"kernel"`
	BootFiles    []string `json:"boot_files"`
	Hash         string   `json:"hash"`
}
type Image struct {
	Source   string       `json:"source,omitempty"`
	Path     string       `json:"path"`
	Size     int64        `json:"size"`
	Table    layout.Table `json:"table"`
	Profiles []Profile    `json:"profiles"`
	Version  string       `json:"version"`
	SHA256   string       `json:"sha256"`
}

func ReadFile(fs filesystem.FileSystem, name string, limit int64) ([]byte, error) {
	f, e := fs.OpenFile(name, os.O_RDONLY)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s exceeds the allowed size", name)
	}
	return b, e
}
func OpenBoot(f io.ReaderAt, t layout.Table, readOnly bool) (*fat32.FileSystem, error) {
	p, ok := t.Find("BOOT")
	if !ok {
		p, ok = t.Number(1)
	}
	if !ok {
		return nil, errors.New("BOOT partition missing")
	}
	if !readOnly {
		return nil, errors.New("BOOT inspection is read-only")
	}
	storage := bootStorage{Storage: file.New(readOnlyImage{io.NewSectionReader(f, 0, t.Size)}, true), start: p.Offset(), size: p.Size()}
	return fat32.Read(storage, p.Size(), p.Offset(), 512)
}

// Filesystem inspection also accepts USB-backed readers without an OS disk handle.
type readOnlyImage struct{ *io.SectionReader }

func (readOnlyImage) Stat() (fs.FileInfo, error) { return nil, backend.ErrNotSuitable }
func (readOnlyImage) Close() error               { return nil }

// FAT files may end between sectors. Raw Windows and macOS devices still need
// complete sector reads; also keep every filesystem read inside BOOT.
type bootStorage struct {
	backend.Storage
	start, size int64
}

func (s bootStorage) ReadAt(p []byte, off int64) (int, error) {
	if off < s.start || off-s.start > s.size || int64(len(p)) > s.size-(off-s.start) {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off%512 == 0 && len(p)%512 == 0 {
		return s.Storage.ReadAt(p, off)
	}
	start := off / 512 * 512
	length := (off - start + int64(len(p)) + 511) / 512 * 512
	b := make([]byte, length)
	n, e := s.Storage.ReadAt(b, start)
	available := int64(n) - (off - start)
	if available < 0 {
		available = 0
	}
	if available > int64(len(p)) {
		available = int64(len(p))
	}
	copy(p, b[off-start:off-start+available])
	if int(available) != len(p) && e == nil {
		e = io.ErrUnexpectedEOF
	}
	return int(available), e
}
func Inspect(ctx context.Context, p string, progress func(int64, int64)) (*Image, error) {
	return inspect(ctx, p, progress, "")
}

// InspectForWrite checks the release structure without hashing the whole file
// again. Execute must check the digest saved at selection before writing.
func InspectForWrite(ctx context.Context, p, selectedDigest string) (*Image, error) {
	digest, err := hex.DecodeString(selectedDigest)
	if err != nil || len(digest) != sha256.Size {
		return nil, errors.New("invalid selected image checksum")
	}
	return inspect(ctx, p, nil, hex.EncodeToString(digest))
}

func inspect(ctx context.Context, p string, progress func(int64, int64), digest string) (*Image, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("select a regular image file")
	}
	t, e := layout.Read(f, st.Size())
	if e != nil {
		return nil, e
	}
	if !t.GPT {
		return nil, errors.New("this image is not an andr36oid Treble image (GPT required)")
	}
	required := []string{"BOOT", "system", "vendor", "metadata", "misc", "cache", "idbloader", "uboot", "trust"}
	for _, name := range required {
		if _, ok := t.Find(name); !ok {
			return nil, fmt.Errorf("Treble image required: %s partition missing", name)
		}
	}
	// A release image is uninitialized: importing an installed card could copy personal data.
	if _, ok := t.Find("userdata"); ok {
		return nil, errors.New("select a release image, not an image of an installed card")
	}
	boot, e := OpenBoot(f, t, true)
	if e != nil {
		return nil, e
	}
	script, e := ReadFile(boot, "/boot.ini-android", 128<<10)
	if e != nil || !bytes.Contains(script, []byte("ramdisk.uimg")) || !bytes.Contains(script, []byte("booti")) {
		return nil, errors.New("Treble Android boot configuration missing")
	}
	rd, e := ReadFile(boot, "/ramdisk.uimg", 64<<20)
	if e != nil || len(rd) < 64 || binary.BigEndian.Uint32(rd) != 0x27051956 {
		return nil, errors.New("invalid Treble ramdisk")
	}
	rdHeader := append([]byte{}, rd[:64]...)
	rdCRC := binary.BigEndian.Uint32(rdHeader[4:])
	binary.BigEndian.PutUint32(rdHeader[4:], 0)
	rdSize := int(binary.BigEndian.Uint32(rd[12:]))
	if crc32.ChecksumIEEE(rdHeader) != rdCRC || rdSize != len(rd)-64 || crc32.ChecksumIEEE(rd[64:]) != binary.BigEndian.Uint32(rd[24:]) {
		return nil, errors.New("ramdisk checksum mismatch")
	}
	if b, e := ReadFile(boot, "/Image", 128<<20); e != nil || len(b) < 4096 {
		return nil, errors.New("missing or truncated Image")
	}
	// Images up to rc1.2 resize the card with a second kernel; since rc1.3
	// Android does it on the first start and the image has no Image-resizing.
	if b, e := ReadFile(boot, "/Image-resizing", 128<<20); e == nil && len(b) < 4096 {
		return nil, errors.New("truncated Image-resizing")
	}
	props, e := releaseFacts(boot)
	if e != nil {
		return nil, e
	}
	if !bytes.Contains(props, []byte("ro.treble.enabled=true")) {
		return nil, errors.New("system does not declare Treble support")
	}
	profiles, e := Profiles(boot)
	if e != nil {
		return nil, e
	}
	if len(profiles) == 0 {
		return nil, errors.New("image has no complete shipped device profiles")
	}
	im := &Image{Source: p, Path: p, Size: st.Size(), Table: t, Profiles: profiles, Version: "Treble build"}
	for _, line := range strings.Split(string(props), "\n") {
		if strings.HasPrefix(line, "ro.andr36oid.version=") {
			im.Version = strings.TrimPrefix(line, "ro.andr36oid.version=")
		}
	}
	if digest == "" {
		im.SHA256, e = Hash(ctx, f, st.Size(), progress)
	} else {
		im.SHA256 = digest
	}
	return im, e
}
func Hash(ctx context.Context, r io.ReaderAt, size int64, progress func(int64, int64)) (string, error) {
	h := sha256.New()
	buf := make([]byte, 4<<20)
	for off := int64(0); off < size; {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		n := int64(len(buf))
		if size-off < n {
			n = size - off
		}
		if _, e := r.ReadAt(buf[:n], off); e != nil {
			return "", e
		}
		h.Write(buf[:n])
		off += n
		if progress != nil {
			progress(off, size)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func validDTB(b []byte) bool {
	if len(b) < 40 || binary.BigEndian.Uint32(b) != 0xd00dfeed {
		return false
	}
	total := uint64(binary.BigEndian.Uint32(b[4:]))
	if total < 40 || total > uint64(len(b)) {
		return false
	}
	version := binary.BigEndian.Uint32(b[20:])
	if version < 16 || version > 17 {
		return false
	}
	for _, pair := range [][2]int{{8, 36}, {12, 32}} {
		off := uint64(binary.BigEndian.Uint32(b[pair[0]:]))
		size := uint64(binary.BigEndian.Uint32(b[pair[1]:]))
		if off < 40 || off > total || size > total-off {
			return false
		}
	}
	reserve := uint64(binary.BigEndian.Uint32(b[16:]))
	return reserve >= 40 && reserve+16 <= total
}

func Profiles(fs filesystem.FileSystem) ([]Profile, error) {
	var result []Profile
	var known map[string]profileInfo
	if b, e := ReadFile(fs, "/"+ProfileList, 1<<20); e == nil {
		known = parseProfileList(b)
	}
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		if depth > 4 {
			return nil
		}
		list, e := fs.ReadDir(strings.TrimPrefix(dir, "/"))
		if e != nil {
			return e
		}
		var kernel string
		var files []string
		for _, ent := range list {
			if ent.IsDir() {
				if e := walk(path.Join(dir, ent.Name()), depth+1); e != nil {
					return e
				}
				continue
			}
			n := ent.Name()
			if strings.HasSuffix(n, ".dtb") && strings.Contains(n, "android") {
				if kernel != "" {
					return fmt.Errorf("ambiguous device profile %s", dir)
				}
				kernel = path.Join(dir, n)
			}
			if (strings.HasSuffix(n, ".dtb") && (strings.Contains(n, "kernel") || strings.Contains(n, "uboot"))) || n == "logo.bmp" || n == "logo_kernel.bmp" || n == "boot.ini-android" ||
				// the charge screens, drawn for this screen's size
				(strings.HasPrefix(n, "battery_") && strings.HasSuffix(n, ".bmp")) {
				files = append(files, path.Join(dir, n))
			}
		}
		if kernel != "" {
			b, e := ReadFile(fs, kernel, 4<<20)
			if e != nil || !validDTB(b) {
				return fmt.Errorf("invalid shipped DTB: %s", kernel)
			}
			hasBoot := false
			for _, n := range files {
				if strings.HasSuffix(n, ".dtb") {
					b, e := ReadFile(fs, n, 4<<20)
					if e != nil || !validDTB(b) {
						return fmt.Errorf("invalid bootloader DTB: %s", n)
					}
					hasBoot = true
				}
			}
			if !hasBoot {
				return fmt.Errorf("incomplete profile %s: bootloader DTB missing", dir)
			}
			sum := sha256.Sum256(b)
			id := strings.TrimPrefix(dir, "/")
			info := profileInfoFor(known, id)
			result = append(result, Profile{ID: id, Name: info.Name, Console: info.Console, AlsoFor: info.AlsoFor, Experimental: info.Experimental, Kernel: kernel, BootFiles: files, Hash: hex.EncodeToString(sum[:])})
		}
		return nil
	}
	for _, dir := range []string{"/Panels", "/Experimental", "/Devices"} {
		if _, e := fs.ReadDir(strings.TrimPrefix(dir, "/")); e == nil {
			if e = walk(dir, 0); e != nil {
				return nil, e
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// For reports whether the profile is listed under console.
func (p Profile) For(console string) bool {
	if p.Console == console {
		return true
	}
	for _, c := range p.AlsoFor {
		if c == console {
			return true
		}
	}
	return false
}

// ProfileList is BOOT/andr36oid-profiles.txt: what the image says about its device
// profiles, so the flasher carries no console data of its own. One line per profile:
//
//	folder|console|name|flags
//
// flags, comma separated: "experimental" (asked about before use) and
// "also=<console>;<console>" (listed under those consoles too).
const ProfileList = "andr36oid-profiles.txt"

type profileInfo struct {
	Name, Console string
	AlsoFor       []string
	Experimental  bool
}

func parseProfileList(b []byte) map[string]profileInfo {
	m := map[string]profileInfo{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) < 3 || f[0] == "" {
			continue
		}
		info := profileInfo{Console: strings.TrimSpace(f[1]), Name: strings.TrimSpace(f[2])}
		if len(f) > 3 {
			for _, flag := range strings.Split(f[3], ",") {
				flag = strings.TrimSpace(flag)
				switch {
				case flag == "experimental":
					info.Experimental = true
				case strings.HasPrefix(flag, "also="):
					for _, c := range strings.Split(strings.TrimPrefix(flag, "also="), ";") {
						if c = strings.TrimSpace(c); c != "" {
							info.AlsoFor = append(info.AlsoFor, c)
						}
					}
				}
			}
		}
		m[strings.Trim(strings.TrimSpace(f[0]), "/")] = info
	}
	return m
}

// profileInfoFor is the image's entry for a profile folder. Images from before the
// list show the folder's name, and the Experimental folder counts as experimental.
func profileInfoFor(list map[string]profileInfo, id string) profileInfo {
	if info, ok := list[id]; ok && info.Name != "" {
		if info.Console == "" {
			info.Console = info.Name
		}
		return info
	}
	s := strings.NewReplacer("-", " ", "_", " ").Replace(path.Base(id))
	return profileInfo{Name: s, Console: s, Experimental: strings.HasPrefix(id, "Experimental/")}
}
