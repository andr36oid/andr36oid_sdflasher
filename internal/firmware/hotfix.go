package firmware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
)

const HotfixIndexURL = "https://github.com/andr36oid/hotfixes/raw/main/hotfixes.json"

// These errors are translation keys in both frontends.
const (
	HotfixNoBuild     = "This card has no complete build identity. Hotfixes need a card flashed with a recent flasher and an andr36oid release from 2026-10-22 or later."
	HotfixNewFormat   = "This hotfix index needs a newer flasher. Update the flasher to use hotfixes."
	HotfixFetchFailed = "Could not fetch hotfixes. Check your internet connection and try again. You can keep using the flasher."
	HotfixChanged     = "The card build changed. Check for hotfixes again."
)

type Hotfix struct {
	Name        string `json:"name"`
	File        string `json:"file"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Build       string `json:"build"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

func (h Hotfix) Preselected() bool { return h.Type == "bugfix" }

type HotfixIndex struct {
	Format   int      `json:"format"`
	BaseURL  string   `json:"base_url"`
	Hotfixes []Hotfix `json:"hotfixes"`
}

type HotfixOffer struct {
	Build    string   `json:"build"`
	BaseURL  string   `json:"base_url"`
	Hotfixes []Hotfix `json:"hotfixes"`
}

type HotfixResult struct {
	File  string `json:"file"`
	Error string `json:"error,omitempty"`
}
type HotfixProgress struct {
	Phase string `json:"phase"`
	Name  string `json:"name"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
}

var hotfixFilename = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*\.zip$`)

func validHotfix(h Hotfix) bool {
	sum, e := hex.DecodeString(h.SHA256)
	return hotfixFilename.MatchString(h.File) && len(h.File) <= 255 && h.Size > 0 && h.Size <= (1<<32)-1 && e == nil && len(sum) == sha256.Size && h.Build != "" && h.Name != "" && h.Title != ""
}
func validHotfixURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.RawQuery == "" && u.Fragment == "" && strings.HasSuffix(raw, "/")
}

// ParseHotfixIndex ignores unknown fields, but refuses versions it cannot interpret.
func ParseHotfixIndex(b []byte) (*HotfixIndex, error) {
	var index HotfixIndex
	if e := json.Unmarshal(b, &index); e != nil {
		return nil, e
	}
	if index.Format != 1 {
		return nil, errors.New(HotfixNewFormat)
	}
	if !validHotfixURL(index.BaseURL) {
		return nil, errors.New("invalid hotfix base URL")
	}
	seen := map[string]bool{}
	for i, h := range index.Hotfixes {
		key := h.Build + "|" + strings.ToLower(h.File)
		if !validHotfix(h) || seen[key] {
			return nil, errors.New("invalid or duplicate hotfix entry")
		}
		seen[key] = true
		if h.Type != "bugfix" {
			index.Hotfixes[i].Type = "extra"
		}
	}
	return &index, nil
}
func (index *HotfixIndex) ForBuild(build string) *HotfixOffer {
	offer := &HotfixOffer{Build: build, BaseURL: index.BaseURL, Hotfixes: []Hotfix{}}
	if build != "" {
		for _, h := range index.Hotfixes {
			if h.Build == build {
				offer.Hotfixes = append(offer.Hotfixes, h)
			}
		}
	}
	return offer
}

var hotfixHTTP = &http.Client{Timeout: 2 * time.Minute}

func hotfixGet(ctx context.Context, raw string) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "andr36oid-sdflasher")
	res, e := hotfixHTTP.Do(req)
	if e != nil {
		return nil, e
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return res, nil
}
func FetchHotfixIndex(ctx context.Context, raw string) (*HotfixIndex, error) {
	res, e := hotfixGet(ctx, raw)
	if e != nil {
		return nil, errors.New(HotfixFetchFailed)
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if e != nil || len(b) > 2<<20 {
		return nil, errors.New(HotfixFetchFailed)
	}
	index, e := ParseHotfixIndex(b)
	if e != nil && e.Error() != HotfixNewFormat {
		return nil, errors.New(HotfixFetchFailed)
	}
	return index, e
}

// CardBuildIdentity reads only BOOT, with the existing sector-aligned reader.
func CardBuildIdentity(d io.ReaderAt, size int64) (string, layout.Table, error) {
	tab, e := layout.Read(d, size)
	if e != nil {
		return "", tab, e
	}
	if _, ok := tab.Find("BOOT"); !ok {
		return "", tab, errors.New(HotfixNoBuild)
	}
	boot, e := OpenBoot(d, tab, true)
	if e != nil {
		return "", tab, e
	}
	build := BuildIdentity(boot)
	if build == "" {
		return "", tab, errors.New(HotfixNoBuild)
	}
	return build, tab, nil
}

// DownloadHotfix returns a verified temporary file. The caller removes it.
func DownloadHotfix(ctx context.Context, base string, h Hotfix, dir string, progress func(int64, int64)) (string, error) {
	if !validHotfix(h) || !validHotfixURL(base) {
		return "", errors.New("invalid hotfix entry")
	}
	res, e := hotfixGet(ctx, base+h.File)
	if e != nil {
		return "", e
	}
	defer res.Body.Close()
	f, e := os.CreateTemp(dir, "hotfix-*.zip")
	if e != nil {
		return "", e
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(f.Name())
		}
	}()
	hash := sha256.New()
	r := io.LimitReader(res.Body, h.Size+1)
	buf := make([]byte, 64<<10)
	var n int64
	for {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		count, re := r.Read(buf)
		if count > 0 {
			if _, e := f.Write(buf[:count]); e != nil {
				return "", e
			}
			hash.Write(buf[:count])
			n += int64(count)
			if progress != nil {
				progress(n, h.Size)
			}
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			return "", re
		}
	}
	if n != h.Size {
		return "", errors.New("hotfix size mismatch")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), h.SHA256) {
		return "", errors.New("hotfix SHA-256 mismatch")
	}
	if e := f.Close(); e != nil {
		return "", e
	}
	ok = true
	return f.Name(), nil
}

// WriteHotfix writes an already verified ZIP without unpacking it or deleting siblings.
// Callers use a temporary BOOT copy so filesystem failures cannot alter the card.
func WriteHotfix(boot filesystem.FileSystem, h Hotfix, data []byte) error {
	sum := sha256.Sum256(data)
	if !validHotfix(h) || int64(len(data)) != h.Size || !strings.EqualFold(hex.EncodeToString(sum[:]), h.SHA256) {
		return errors.New("hotfix verification failed")
	}
	return placeHotfix(boot, h, bytes.NewReader(data))
}

// placeHotfix only receives a verified download and writes to the staged FAT.
func placeHotfix(boot filesystem.FileSystem, h Hotfix, data io.Reader) error {
	if _, e := boot.ReadDir("hotfix"); e != nil {
		if e := boot.Mkdir("hotfix"); e != nil {
			return e
		}
	}
	f, e := boot.OpenFile("hotfix/"+h.File, os.O_CREATE|os.O_TRUNC|os.O_RDWR)
	if e != nil {
		return e
	}
	n, e := io.Copy(f, data)
	ce := f.Close()
	if e != nil {
		return e
	}
	if n != h.Size {
		return io.ErrShortWrite
	}
	return ce
}

type HotfixDisk interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
}

// ApplyHotfixes rechecks the target build, stages BOOT, and writes only changed
// sectors inside BOOT. A failed fetch/verification never reaches the filesystem.
func ApplyHotfixes(ctx context.Context, disk HotfixDisk, size int64, offer *HotfixOffer, selected []string, dir string, observer func(HotfixProgress)) ([]HotfixResult, error) {
	results := []HotfixResult{}
	if offer == nil || offer.Build == "" {
		return results, errors.New(HotfixNoBuild)
	}
	chosen := []Hotfix{}
	seen := map[string]bool{}
	for _, name := range selected {
		found := false
		for _, h := range offer.Hotfixes {
			if h.File == name && h.Build == offer.Build && validHotfix(h) && !seen[strings.ToLower(name)] {
				chosen = append(chosen, h)
				seen[strings.ToLower(name)] = true
				found = true
				break
			}
		}
		if !found {
			return results, errors.New("invalid hotfix selection")
		}
	}
	build, tab, e := CardBuildIdentity(disk, size)
	if e != nil {
		return results, e
	}
	if build != offer.Build {
		return results, errors.New(HotfixChanged)
	}
	if len(chosen) == 0 {
		return results, nil
	}
	p, _ := tab.Find("BOOT")
	staged, e := os.CreateTemp(dir, "hotfix-boot-*.fat")
	if e != nil {
		return results, e
	}
	defer func() { staged.Close(); os.Remove(staged.Name()) }()
	if _, e = io.Copy(staged, io.NewSectionReader(disk, p.Offset(), p.Size())); e != nil {
		return results, e
	}
	boot, e := fat32.Read(file.New(staged, false), p.Size(), 0, 512)
	if e != nil {
		return results, e
	}
	emit := func(phase, name string, n, total int64) {
		if observer != nil {
			observer(HotfixProgress{Phase: phase, Name: name, Done: n, Total: total})
		}
	}
	for _, h := range chosen {
		emit("Downloading hotfix", h.File, 0, h.Size)
		tmp, err := DownloadHotfix(ctx, offer.BaseURL, h, dir, func(n, total int64) { emit("Downloading hotfix", h.File, n, total) })
		result := HotfixResult{File: h.File}
		if err == nil {
			emit("Verifying hotfix", h.File, h.Size, h.Size)
			data, readErr := os.Open(tmp)
			if readErr != nil {
				os.Remove(tmp)
				return results, readErr
			}
			err = placeHotfix(boot, h, data)
			closeErr := data.Close()
			os.Remove(tmp)
			if err != nil {
				return results, err
			}
			if closeErr != nil {
				return results, closeErr
			}
		} else {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	if e := ctx.Err(); e != nil {
		return results, e
	}
	// Compare full sectors to keep raw USB and OS device I/O aligned. Other
	// partitions, the partition table, and unchanged BOOT sectors are never written.
	emit("Writing hotfixes", "BOOT", 0, p.Size())
	old, next := make([]byte, 1<<20), make([]byte, 1<<20)
	for off := int64(0); off < p.Size(); {
		n := min(int64(len(next)), p.Size()-off)
		if _, e = disk.ReadAt(old[:n], p.Offset()+off); e != nil {
			return results, e
		}
		if _, e = staged.ReadAt(next[:n], off); e != nil {
			return results, e
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
			count, err := disk.WriteAt(next[start:sector], p.Offset()+off+start)
			if err != nil {
				return results, err
			}
			if int64(count) != sector-start {
				return results, io.ErrShortWrite
			}
		}
		off += n
		emit("Writing hotfixes", "BOOT", off, p.Size())
	}
	if e = disk.Sync(); e != nil {
		return results, e
	}
	// Read back through the raw device before reporting success.
	check, e := OpenBoot(disk, tab, true)
	if e != nil {
		return results, e
	}
	for _, result := range results {
		if result.Error != "" {
			continue
		}
		for _, h := range chosen {
			if h.File == result.File {
				f, err := check.OpenFile("/hotfix/"+h.File, os.O_RDONLY)
				if err != nil {
					return results, err
				}
				hash := sha256.New()
				n, readErr := io.Copy(hash, io.LimitReader(f, h.Size+1))
				closeErr := f.Close()
				if readErr != nil || closeErr != nil || n != h.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), h.SHA256) {
					return results, errors.New("written hotfix did not verify")
				}
			}
		}
	}
	return results, nil
}
