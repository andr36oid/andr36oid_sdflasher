package firmware

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
)

const DTBPackInvalid = "This file is not a complete device tree set. Download it again."
const DTBPackNoBuild = "This card has no readable andr36oid build information, so the right set cannot be chosen."
const DTBPackAlready = "This card already has these device trees."
const DTBPackNoOnline = "No device tree set was published for this card's build. If you were given a file, choose \"Use a file I downloaded\"."

var DTBRootFiles = append([]string{"rk3326-r36s-android.dtb"}, UBootDTBNames...)
var dtbProfileID = regexp.MustCompile(`^(Devices|Panels|Experimental)/[A-Za-z0-9_-]+$`)
var dtbFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*\.dtb$`)

type DTBFile struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type DTBLine struct {
	KernelBuild int                `json:"kernel_build"`
	PictureSets map[string]string  `json:"picture_sets"`
	Files       map[string]DTBFile `json:"files"`
}
type DTBManifest struct {
	Format           int                `json:"format"`
	Version          string             `json:"version"`
	Revision         int                `json:"revision"`
	CompatibleBuilds []string           `json:"compatible_builds"`
	Lines            map[string]DTBLine `json:"lines"`
}
type DTBPack struct {
	ManifestPath string            `json:"manifest_path,omitempty"`
	Path         string            `json:"path"`
	Manifest     DTBManifest       `json:"manifest"`
	Files        map[string][]byte `json:"-"`
	SHA256       string            `json:"sha256"`
	Source       *SetupSource      `json:"source,omitempty"`
}

func dtbPath(name string) bool {
	if name == "andr36oid-profiles.txt" {
		return true
	}
	if strings.ContainsAny(name, "\\\x00") || path.Clean(name) != name {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) == 2 && parts[0] == "root" {
		for _, n := range DTBRootFiles {
			if n == parts[1] {
				return true
			}
		}
		return false
	}
	return len(parts) == 3 && dtbProfileID.MatchString(parts[0]+"/"+parts[1]) && dtbFileName.MatchString(parts[2])
}
func decodeDTBManifest(b []byte) (DTBManifest, error) {
	var m DTBManifest
	if len(b) > 2<<20 {
		return m, errors.New(DTBPackInvalid)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&m); e != nil {
		return m, errors.New(DTBPackInvalid)
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return m, errors.New(DTBPackInvalid)
	}
	if m.Format != 1 || m.Revision < 1 || m.Version == "" || len(m.Version) > 256 || len(m.CompatibleBuilds) == 0 || len(m.CompatibleBuilds) > 128 || len(m.Lines) != 2 {
		return m, errors.New(DTBPackInvalid)
	}
	seen := map[string]bool{}
	for _, b := range m.CompatibleBuilds {
		if b == "" || len(b) > 1024 || strings.ContainsAny(b, "|\r\n\x00") || seen[b] {
			return m, errors.New(DTBPackInvalid)
		}
		seen[b] = true
	}
	var total int64
	for _, kernel := range []string{"4.19", "5.10"} {
		line, ok := m.Lines[kernel]
		if !ok || line.KernelBuild < 1 || len(line.Files) < 8 || len(line.Files) > 4096 {
			return m, errors.New(DTBPackInvalid)
		}
		for name, f := range line.Files {
			raw, e := hex.DecodeString(f.SHA256)
			if !dtbPath(name) || f.Size < 1 || f.Size > 4<<20 || e != nil || len(raw) != 32 {
				return m, errors.New(DTBPackInvalid)
			}
			total += f.Size
		}
		for _, n := range append([]string{"andr36oid-profiles.txt"}, rootPackNames()...) {
			if _, ok := line.Files[n]; !ok {
				return m, errors.New(DTBPackInvalid)
			}
		}
		for id, set := range line.PictureSets {
			if !dtbProfileID.MatchString(id) || !pictureSet.MatchString(set) {
				return m, errors.New(DTBPackInvalid)
			}
		}
	}
	if total > 256<<20 {
		return m, errors.New(DTBPackInvalid)
	}
	return m, nil
}
func rootPackNames() []string {
	out := make([]string, len(DTBRootFiles))
	for i, n := range DTBRootFiles {
		out[i] = "root/" + n
	}
	return out
}
func ReadDTBPack(filename string) (*DTBPack, error) { return ReadDTBPackManifest(filename, nil) }

// Online callers supply the independently fetched manifest. An older embedded
// manifest never overrides the release asset; the payload must match the winner.
func ReadDTBPackManifest(filename string, online []byte) (*DTBPack, error) {
	f, e := os.Open(filename)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if st.Size() < 1 || st.Size() > 256<<20 {
		return nil, errors.New(DTBPackInvalid)
	}
	z, e := zip.NewReader(f, st.Size())
	if e != nil || len(z.File) > 8193 {
		return nil, errors.New(DTBPackInvalid)
	}
	files := map[string][]byte{}
	fold := map[string]bool{}
	var total uint64
	for _, entry := range z.File {
		n := entry.Name
		lower := strings.ToLower(n)
		limit := uint64(4 << 20)
		if n == "manifest.json" {
			limit = 2 << 20
		}
		parts := strings.SplitN(n, "/", 2)
		if fold[lower] || entry.FileInfo().IsDir() || entry.Mode()&os.ModeSymlink != 0 || entry.UncompressedSize64 > limit || n != "manifest.json" && (len(parts) != 2 || (parts[0] != "4.19" && parts[0] != "5.10") || !dtbPath(parts[1])) {
			return nil, errors.New(DTBPackInvalid)
		}
		fold[lower] = true
		total += entry.UncompressedSize64
		if total > 258<<20 {
			return nil, errors.New(DTBPackInvalid)
		}
		reader, e := entry.Open()
		if e != nil {
			return nil, errors.New(DTBPackInvalid)
		}
		b, e := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
		ce := reader.Close()
		if e != nil || ce != nil || uint64(len(b)) != entry.UncompressedSize64 {
			return nil, errors.New(DTBPackInvalid)
		}
		files[n] = b
	}
	embedded, e := decodeDTBManifest(files["manifest.json"])
	if e != nil {
		return nil, e
	}
	m := embedded
	if online != nil {
		m, e = decodeDTBManifest(online)
		if e != nil {
			return nil, e
		}
		if m.Version != embedded.Version || m.Revision < embedded.Revision {
			return nil, errors.New("release manifest is older than the downloaded set")
		}
	}
	expected := 1
	for kernel, line := range m.Lines {
		known := map[string]bool{}
		catalog := files[kernel+"/andr36oid-profiles.txt"]
		for _, raw := range strings.Split(string(catalog), "\n") {
			raw = strings.TrimSpace(raw)
			if raw == "" || strings.HasPrefix(raw, "#") {
				continue
			}
			parts := strings.Split(raw, "|")
			if len(parts) < 3 || !dtbProfileID.MatchString(parts[0]) || known[parts[0]] {
				return nil, errors.New(DTBPackInvalid)
			}
			known[parts[0]] = true
		}
		if len(known) == 0 || len(known) != len(line.PictureSets) {
			return nil, errors.New(DTBPackInvalid)
		}
		for id := range known {
			if line.PictureSets[id] == "" {
				return nil, errors.New(DTBPackInvalid)
			}
			for _, n := range DTBRootFiles {
				if _, ok := line.Files[id+"/"+n]; !ok {
					return nil, errors.New(DTBPackInvalid)
				}
			}
		}
		for name, desc := range line.Files {
			b, ok := files[kernel+"/"+name]
			sum := sha256.Sum256(b)
			if !ok || int64(len(b)) != desc.Size || hex.EncodeToString(sum[:]) != strings.ToLower(desc.SHA256) || strings.HasSuffix(name, ".dtb") && !validDTB(b) {
				return nil, errors.New(DTBPackInvalid)
			}
			if strings.HasSuffix(name, ".dtb") && !strings.HasPrefix(name, "root/") {
				if !known[path.Dir(name)] {
					return nil, errors.New(DTBPackInvalid)
				}
			}
			expected++
		}
	}
	if len(files) != expected {
		return nil, errors.New(DTBPackInvalid)
	}
	hash := sha256.New()
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return nil, e
	}
	if _, e = io.Copy(hash, f); e != nil {
		return nil, e
	}
	return &DTBPack{Path: filename, Manifest: m, Files: files, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}
func (p *DTBPack) compatible(setup CardSetup) error {
	stamp := strings.SplitN(setup.Build, "|", 2)
	if len(stamp) != 2 {
		return errors.New(DTBPackNoBuild)
	}
	match := false
	for _, b := range p.Manifest.CompatibleBuilds {
		if b == stamp[1] {
			match = true
		}
	}
	if !match || setup.Release != p.Manifest.Version {
		return fmt.Errorf("This set is for build %s. The card has build %s. Use the set that was published with the card's build.", strings.Join(p.Manifest.CompatibleBuilds, ", "), stamp[1])
	}
	line, ok := p.Manifest.Lines[setup.KernelLine]
	if !ok {
		return fmt.Errorf("This set has no files for the %s kernel that is on the card.", setup.KernelLine)
	}
	if setup.KernelBuild != 0 && line.KernelBuild != setup.KernelBuild {
		return errors.New("this set belongs to another kernel build")
	}
	return nil
}

func ReadDTBPackReference(filename, reference string) (*DTBPack, error) {
	if reference == "" {
		return ReadDTBPack(filename)
	}
	f, e := os.Open(reference)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if e != nil || len(b) > 2<<20 {
		return nil, errors.New(DTBPackInvalid)
	}
	p, e := ReadDTBPackManifest(filename, b)
	if e == nil {
		p.ManifestPath = reference
	}
	return p, e
}
