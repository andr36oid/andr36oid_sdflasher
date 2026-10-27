package firmware

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
	"golang.org/x/mod/semver"
)

const (
	KernelArchiveLimit  = 128 << 20
	KernelFilesLimit    = 128 << 20
	KernelManifestLimit = 64 << 10
	KernelMarkerFile    = "kernel-applied.txt"
	KernelNoBuild       = "This card has no complete build identity. Kernel updates need an andr36oid card with readable build information."
	KernelChanged       = "The card build or kernel changed. Check for kernel updates again."
)

// KernelIdentity is read from BOOT, never inferred from the Linux vermagic.
// Version may be empty for releases predating kernel release metadata.
type KernelIdentity struct {
	Build   string `json:"build"`
	Version string `json:"version"`
}
type kernelMarker struct {
	Format  int    `json:"format"`
	Build   string `json:"build"`
	Version string `json:"version"`
}

func KernelVersion(v string) string {
	v = "v" + strings.TrimPrefix(v, "v")
	if len(v) > 128 || !semver.IsValid(v) {
		return ""
	}
	return v
}
func ValidKernelBuilds(builds []string) bool {
	if len(builds) == 0 || len(builds) > 4096 {
		return false
	}
	seen := map[string]bool{}
	for _, build := range builds {
		parts := strings.Split(build, "|")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(build) != build || strings.ContainsAny(build, "\r\n\x00") || len(build) > 1024 || seen[build] {
			return false
		}
		seen[build] = true
	}
	return true
}
func KernelCompatible(builds []string, build string) bool {
	for _, b := range builds {
		if build != "" && b == build {
			return true
		}
	}
	return false
}
func ReadKernelIdentity(boot filesystem.FileSystem) (KernelIdentity, error) {
	state := KernelIdentity{Build: BuildIdentity(boot)}
	if !ValidKernelBuilds([]string{state.Build}) {
		return state, errors.New(KernelNoBuild)
	}
	// A marker belongs only to the build for which it was written. Full image
	// updates replace BOOT and do not carry this marker forward.
	entries, err := boot.ReadDir(".")
	if err != nil {
		return state, err
	}
	for _, entry := range entries {
		if !strings.EqualFold(entry.Name(), KernelMarkerFile) {
			continue
		}
		b, err := ReadFile(boot, "/"+KernelMarkerFile, 4096)
		if err != nil {
			return state, err
		}
		var marker kernelMarker
		if json.Unmarshal(b, &marker) != nil || marker.Format != 1 || !ValidKernelBuilds([]string{marker.Build}) || KernelVersion(marker.Version) == "" {
			return state, errors.New("invalid applied kernel marker")
		}
		if marker.Build == state.Build {
			state.Version = marker.Version
			return state, nil
		}
	}
	b, err := ReadFile(boot, "/"+BuildInfoFile, 64<<10)
	if err != nil {
		return state, err
	}
	props, _ := ParseBuildInfo(b)
	// Unlike image build versions, this property uses the kernel-index version
	// namespace. An absent/unrecognized value leaves the kernel version unknown.
	if v := props["kernel_version"]; KernelVersion(v) != "" {
		state.Version = v
	}
	return state, nil
}
func CardKernelIdentity(d io.ReaderAt, size int64) (KernelIdentity, layout.Table, error) {
	tab, err := layout.Read(d, size)
	if err != nil {
		return KernelIdentity{}, tab, err
	}
	if _, ok := tab.Find("BOOT"); !ok {
		return KernelIdentity{}, tab, errors.New(KernelNoBuild)
	}
	boot, err := OpenBoot(d, tab, true)
	if err != nil {
		return KernelIdentity{}, tab, err
	}
	state, err := ReadKernelIdentity(boot)
	return state, tab, err
}

type KernelFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type KernelManifest struct {
	Format           int          `json:"format"`
	Version          string       `json:"version"`
	CompatibleBuilds []string     `json:"compatible_builds"`
	Files            []KernelFile `json:"files"`
}
type KernelBundle struct {
	Manifest KernelManifest
	Files    map[string][]byte
}

var kernelDTBName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*-(kernel|uboot)\.dtb$`)

func kernelFileLimit(name string) int64 {
	switch name {
	case "Image":
		return 96 << 20
	case "boot.ini":
		return 128 << 10
	}
	if len(name) <= 255 && kernelDTBName.MatchString(name) {
		return 4 << 20
	}
	return 0
}
func validKernelManifest(m KernelManifest) error {
	if m.Format != 1 || KernelVersion(m.Version) == "" || !ValidKernelBuilds(m.CompatibleBuilds) || len(m.Files) == 0 || len(m.Files) > 64 {
		return errors.New("invalid kernel manifest")
	}
	seen := map[string]bool{}
	var total int64
	for _, f := range m.Files {
		limit := kernelFileLimit(f.Name)
		sum, err := hex.DecodeString(f.SHA256)
		key := strings.ToLower(f.Name)
		if limit == 0 || f.Size <= 0 || f.Size > limit || seen[key] || err != nil || len(sum) != sha256.Size {
			return errors.New("invalid kernel manifest file")
		}
		seen[key] = true
		total += f.Size
	}
	if !seen["image"] || total > KernelFilesLimit {
		return errors.New("kernel bundle is missing Image or exceeds size limit")
	}
	return nil
}
func validateKernelBundle(bundle *KernelBundle) error {
	if bundle == nil {
		return errors.New("missing kernel bundle")
	}
	if err := validKernelManifest(bundle.Manifest); err != nil {
		return err
	}
	if len(bundle.Files) != len(bundle.Manifest.Files) {
		return errors.New("kernel file list differs from manifest")
	}
	for _, f := range bundle.Manifest.Files {
		data, ok := bundle.Files[f.Name]
		sum := sha256.Sum256(data)
		if !ok || int64(len(data)) != f.Size || !strings.EqualFold(hex.EncodeToString(sum[:]), f.SHA256) {
			return fmt.Errorf("kernel file %s checksum or size mismatch", f.Name)
		}
	}
	return nil
}

// ReadKernelArchive accepts root files only. It never extracts paths to disk.
// Both ZIP declarations and decompressed bytes are bounded before use.
func ReadKernelArchive(filename string) (*KernelBundle, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() <= 0 || st.Size() > KernelArchiveLimit {
		return nil, errors.New("kernel archive exceeds size limit")
	}
	z, err := zip.NewReader(f, st.Size())
	if err != nil {
		return nil, err
	}
	if len(z.File) < 2 || len(z.File) > 65 {
		return nil, errors.New("invalid kernel archive file count")
	}
	files := map[string]*zip.File{}
	seen := map[string]bool{}
	var total uint64
	for _, f := range z.File {
		limit := kernelFileLimit(f.Name)
		if f.Name == "manifest.json" {
			limit = KernelManifestLimit
		}
		key := strings.ToLower(f.Name)
		if limit == 0 || !f.Mode().IsRegular() || seen[key] || f.UncompressedSize64 == 0 || f.UncompressedSize64 > uint64(limit) {
			return nil, fmt.Errorf("invalid kernel archive entry %q", f.Name)
		}
		seen[key] = true
		files[f.Name] = f
		total += f.UncompressedSize64
		if total > KernelFilesLimit+KernelManifestLimit {
			return nil, errors.New("kernel archive expands beyond size limit")
		}
	}
	manifest, ok := files["manifest.json"]
	if !ok {
		return nil, errors.New("kernel archive has no manifest.json")
	}
	b, err := readKernelZipFile(manifest, KernelManifestLimit)
	if err != nil {
		return nil, err
	}
	bundle := &KernelBundle{Files: map[string][]byte{}}
	if err = json.Unmarshal(b, &bundle.Manifest); err != nil {
		return nil, err
	}
	if err = validKernelManifest(bundle.Manifest); err != nil {
		return nil, err
	}
	if len(files) != len(bundle.Manifest.Files)+1 {
		return nil, errors.New("kernel archive differs from manifest")
	}
	for _, metadata := range bundle.Manifest.Files {
		f, ok := files[metadata.Name]
		if !ok || f.UncompressedSize64 != uint64(metadata.Size) {
			return nil, errors.New("kernel archive file size differs from manifest")
		}
		data, err := readKernelZipFile(f, metadata.Size)
		if err != nil {
			return nil, err
		}
		bundle.Files[metadata.Name] = data
	}
	if err = validateKernelBundle(bundle); err != nil {
		return nil, err
	}
	return bundle, nil
}
func readKernelZipFile(f *zip.File, limit int64) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("kernel ZIP entry exceeds size limit")
	}
	return b, nil
}

var bootDTBLoad = regexp.MustCompile(`(?m)^\s*(?:if\s+)?load\s+mmc\s+\S+\s+\S+\s+([^\s;]+\.dtb)\s*(?:;\s*then)?\s*$`)

func checkKernelBootScript(boot filesystem.FileSystem, script []byte) error {
	// FAT accepts short aliases too. Refuse a lookup resolving to the preserved
	// boot.ini-android (or another file) instead of the active boot.ini.
	f, err := boot.OpenFile("boot.ini", os.O_RDONLY)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if !strings.EqualFold(st.Name(), "boot.ini") {
		return errors.New("kernel boot.ini does not resolve to the card's active boot script")
	}
	old, err := ReadFile(boot, "/boot.ini", 128<<10)
	if err != nil {
		return err
	}
	before, after := bootDTBLoad.FindAllSubmatch(old, -1), bootDTBLoad.FindAllSubmatch(script, -1)
	if len(before) == 0 || len(before) != len(after) {
		return errors.New("kernel boot.ini must preserve the card's DTB selection")
	}
	for i := range before {
		if string(before[i][1]) != string(after[i][1]) {
			return errors.New("kernel boot.ini must preserve the card's DTB selection")
		}
	}
	return nil
}

// ApplyKernelUpdate stages changes before touching the card, commits only BOOT,
// and records the version only after all kernel files verify on the raw device.
// Like other raw-device writes, it must not be interrupted by removing the card.
func ApplyKernelUpdate(disk HotfixDisk, size int64, bundle *KernelBundle, expected KernelIdentity, dir string) error {
	if err := validateKernelBundle(bundle); err != nil {
		return err
	}
	current, tab, err := CardKernelIdentity(disk, size)
	if err != nil {
		return err
	}
	version := bundle.Manifest.Version
	if current.Build != expected.Build || !KernelCompatible(bundle.Manifest.CompatibleBuilds, current.Build) || (current.Version != expected.Version && KernelVersion(current.Version) != KernelVersion(version)) {
		return errors.New(KernelChanged)
	}
	if current.Version != "" && semver.Compare(KernelVersion(version), KernelVersion(current.Version)) < 0 {
		return errors.New("refusing to downgrade the installed kernel")
	}
	p, _ := tab.Find("BOOT")
	staged, err := os.CreateTemp(dir, "kernel-boot-*.fat")
	if err != nil {
		return err
	}
	defer func() { staged.Close(); os.Remove(staged.Name()) }()
	if _, err = io.Copy(staged, io.NewSectionReader(disk, p.Offset(), p.Size())); err != nil {
		return err
	}
	boot, err := fat32.Read(file.New(staged, false), p.Size(), 0, 512)
	if err != nil {
		return err
	}
	if script, ok := bundle.Files["boot.ini"]; ok {
		if err = checkKernelBootScript(boot, script); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(bundle.Files))
	for name := range bundle.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err = writeBootFile(boot, name, bundle.Files[name]); err != nil {
			return err
		}
	}
	if err = staged.Sync(); err != nil {
		return err
	}
	if err = commitBoot(disk, staged, p.Offset(), p.Size(), nil); err != nil {
		return err
	}
	check, err := OpenBoot(disk, tab, true)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err = verifyBootFile(check, name, bundle.Files[name]); err != nil {
			return err
		}
	}
	marker, _ := json.Marshal(kernelMarker{Format: 1, Build: current.Build, Version: version})
	marker = append(marker, '\n')
	if err = writeBootFile(boot, KernelMarkerFile, marker); err != nil {
		return err
	}
	if err = staged.Sync(); err != nil {
		return err
	}
	if err = commitBoot(disk, staged, p.Offset(), p.Size(), nil); err != nil {
		return err
	}
	check, err = OpenBoot(disk, tab, true)
	if err != nil {
		return err
	}
	if err = verifyBootFile(check, KernelMarkerFile, marker); err != nil {
		return err
	}
	for _, name := range names {
		if err = verifyBootFile(check, name, bundle.Files[name]); err != nil {
			return err
		}
	}
	return nil
}
