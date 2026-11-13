package firmware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	StandardBootloaderRepo = "andr36oid/u-boot"
	NewBootloaderRepo      = "andr36oid/uboot-mainline"
	BootloaderFetchFailed  = "Couldn't reach GitHub. The image's bootloader will be used."
)

var errBootloaderFetch = errors.New(BootloaderFetchFailed)

type BootloaderBuild struct {
	Choice     string `json:"choice"`
	Repository string `json:"repository"`
	Version    string `json:"version"`
	Tag        string `json:"tag"`
	URL        string `json:"url"`
	SHA256     string `json:"sha256"`
}

var bootloaderTag = regexp.MustCompile(`^[0-9]{8}-[0-9]{4}$`)

func bootloaderRepository(choice string) (string, string, error) {
	switch choice {
	case "standard":
		return StandardBootloaderRepo, "2017.09", nil
	case "new":
		return NewBootloaderRepo, "2026.10", nil
	}
	return "", "", errors.New("unknown bootloader choice")
}
func bootloaderAssetURL(raw, repo, tag, name string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.RawPath == "" && u.Path == "/"+repo+"/releases/download/"+tag+"/"+name
}
func bootloaderRead(ctx context.Context, raw string, limit int64) ([]byte, error) {
	res, e := hotfixGet(ctx, raw)
	if e != nil {
		return nil, fmt.Errorf("%w: %v", errBootloaderFetch, e)
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if e != nil {
		return nil, fmt.Errorf("%w: %v", errBootloaderFetch, e)
	}
	if int64(len(b)) > limit {
		return nil, errors.New("bootloader response exceeds size limit")
	}
	return b, nil
}
func bootloaderSum(b []byte) (string, error) {
	sum := ""
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./") != "uboot.img" {
			continue
		}
		raw, e := hex.DecodeString(fields[0])
		if e != nil || len(raw) != sha256.Size || sum != "" {
			return "", errors.New("invalid or duplicate U-Boot checksum")
		}
		sum = strings.ToLower(fields[0])
	}
	if sum == "" {
		return "", errors.New("SHA256SUMS has no U-Boot entry")
	}
	return sum, nil
}

// FetchBootloaderBuild uses the hotfix HTTP client and does not send card or user
// details. Only the two named repositories and their classic uboot.img are used.
func FetchBootloaderBuild(ctx context.Context, choice string) (*BootloaderBuild, error) {
	repo, version, e := bootloaderRepository(choice)
	if e != nil {
		return nil, e
	}
	b, e := bootloaderRead(ctx, "https://api.github.com/repos/"+repo+"/releases/latest", 2<<20)
	if e != nil {
		return nil, e
	}
	var release struct {
		Tag    string `json:"tag_name"`
		Draft  bool   `json:"draft"`
		Assets []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if e = json.Unmarshal(b, &release); e != nil {
		return nil, e
	}
	if release.Draft || !bootloaderTag.MatchString(release.Tag) || len(release.Assets) > 64 {
		return nil, errors.New("invalid bootloader release")
	}
	imageURL, sumsURL := "", ""
	for _, a := range release.Assets {
		switch a.Name {
		case "uboot.img":
			if imageURL != "" || a.Size != BootloaderSize || !bootloaderAssetURL(a.URL, repo, release.Tag, a.Name) {
				return nil, errors.New("invalid U-Boot release asset")
			}
			imageURL = a.URL
		case "SHA256SUMS":
			if sumsURL != "" || a.Size <= 0 || a.Size > 64<<10 || !bootloaderAssetURL(a.URL, repo, release.Tag, a.Name) {
				return nil, errors.New("invalid U-Boot checksum asset")
			}
			sumsURL = a.URL
		}
	}
	if imageURL == "" || sumsURL == "" {
		return nil, errors.New("bootloader release is incomplete")
	}
	b, e = bootloaderRead(ctx, sumsURL, 64<<10)
	if e != nil {
		return nil, e
	}
	sum, e := bootloaderSum(b)
	if e != nil {
		return nil, e
	}
	return &BootloaderBuild{Choice: choice, Repository: repo, Version: version, Tag: release.Tag, URL: imageURL, SHA256: sum}, nil
}
func validBootloaderBuild(b *BootloaderBuild) bool {
	if b == nil {
		return false
	}
	repo, version, e := bootloaderRepository(b.Choice)
	sum, decode := hex.DecodeString(b.SHA256)
	return e == nil && repo == b.Repository && version == b.Version && bootloaderTag.MatchString(b.Tag) && bootloaderAssetURL(b.URL, repo, b.Tag, "uboot.img") && decode == nil && len(sum) == sha256.Size
}

// DownloadBootloader validates a cached file on each use; new downloads are
// renamed into the cache only after size, SHA and all four loader copies pass.
func DownloadBootloader(ctx context.Context, b *BootloaderBuild, dir string) ([]byte, error) {
	if !validBootloaderBuild(b) {
		return nil, errors.New("invalid bootloader offer")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	name := filepath.Join(dir, strings.ToLower(b.SHA256)+"-uboot.img")
	verify := func(data []byte) error {
		sum := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), b.SHA256) {
			return errors.New("bootloader SHA-256 differs")
		}
		return CheckBootloaderContainer(data)
	}
	if data, e := ReadBootloaderFile(name); e == nil && verify(data) == nil {
		return data, nil
	}
	data, e := bootloaderRead(ctx, b.URL, BootloaderSize)
	if e != nil {
		return nil, e
	}
	if e = verify(data); e != nil {
		return nil, e
	}
	f, e := os.CreateTemp(dir, "bootloader-*.tmp")
	if e != nil {
		return nil, e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if n, e := f.Write(data); e != nil {
		return nil, e
	} else if n != len(data) {
		return nil, io.ErrShortWrite
	}
	if e = f.Sync(); e != nil {
		return nil, e
	}
	if e = f.Close(); e != nil {
		return nil, e
	}
	if e = os.Rename(f.Name(), name); e != nil {
		return nil, e
	}
	d, e := os.Open(dir)
	if e != nil {
		return nil, e
	}
	e = d.Sync()
	d.Close()
	if e != nil {
		return nil, e
	}
	return data, nil
}
func BootloaderDescription(b *BootloaderBuild) string {
	if b == nil || b.Choice == "image" {
		return "Bootloader: as in the image"
	}
	if b.Choice == "file" {
		return "Bootloader: local file"
	}
	if b.Choice == "new" {
		return fmt.Sprintf("Bootloader: New U-Boot %s (%s)", b.Version, b.Tag)
	}
	return fmt.Sprintf("Bootloader: Standard U-Boot %s (%s)", b.Version, b.Tag)
}

// FetchBootloaderChoices is shared by the desktop and Android option rows.
// Both download rows stay unavailable if either release lookup fails.
func FetchBootloaderChoices(ctx context.Context) ([]*BootloaderBuild, error) {
	ctx, deadline := context.WithTimeout(ctx, 2*time.Minute)
	defer deadline()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		index int
		build *BootloaderBuild
		err   error
	}
	ready := make(chan result, 2)
	for n, choice := range []string{"standard", "new"} {
		go func(n int, choice string) { b, e := FetchBootloaderBuild(ctx, choice); ready <- result{n, b, e} }(n, choice)
	}
	out := make([]*BootloaderBuild, 2)
	var first error
	for n := 0; n < 2; n++ {
		r := <-ready
		if r.err != nil && first == nil {
			first = r.err
			cancel()
		}
		out[r.index] = r.build
	}
	if first != nil {
		return nil, first
	}
	return out, nil
}
