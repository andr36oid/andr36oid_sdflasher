package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"golang.org/x/mod/semver"
)

const KernelRepository = "andr36oid/kernel-releases"
const KernelIndexURL = "https://github.com/andr36oid/kernel-releases/raw/main/kernels.json"
const KernelNewFormat = "This kernel index needs a newer flasher. Update the flasher to use kernel updates."

// Versions order kernel releases independently of Linux's version/vermagic.
// CompatibleBuilds contains exact BuildIdentity values, never wildcards.
type KernelRelease struct {
	Version          string   `json:"version"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	File             string   `json:"file"`
	Size             int64    `json:"size"`
	SHA256           string   `json:"sha256"`
	CompatibleBuilds []string `json:"compatible_builds"`
}
type KernelIndex struct {
	Format  int             `json:"format"`
	BaseURL string          `json:"base_url"`
	Kernels []KernelRelease `json:"kernels"`
}
type KernelOffer struct {
	KernelRelease
	URL     string                  `json:"url"`
	Current firmware.KernelIdentity `json:"current"`
}

var kernelFilename = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*\.zip$`)

func validKernelRelease(k KernelRelease) bool {
	sum, err := hex.DecodeString(k.SHA256)
	return firmware.KernelVersion(k.Version) != "" && strings.TrimSpace(k.Title) != "" && kernelFilename.MatchString(k.File) && len(k.File) <= 255 && k.Size > 0 && k.Size <= firmware.KernelArchiveLimit && err == nil && len(sum) == sha256.Size && firmware.ValidKernelBuilds(k.CompatibleBuilds)
}
func validKernelBaseURL(raw string) bool {
	if !validReleaseURL(raw, KernelRepository) || !strings.HasSuffix(raw, "/") {
		return false
	}
	u, _ := url.Parse(raw)
	tag := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"+KernelRepository+"/releases/download/"), "/")
	return tag != "" && tag != "." && !strings.Contains(tag, "..") && !strings.Contains(tag, "/")
}
func ParseKernelIndex(b []byte) (*KernelIndex, error) {
	var index KernelIndex
	if err := json.Unmarshal(b, &index); err != nil {
		return nil, err
	}
	if index.Format != 1 {
		return nil, errors.New(KernelNewFormat)
	}
	if !validKernelBaseURL(index.BaseURL) {
		return nil, errors.New("invalid kernel base URL")
	}
	versions, files := map[string]bool{}, map[string]bool{}
	for _, k := range index.Kernels {
		version := semver.Canonical(firmware.KernelVersion(k.Version))
		if !validKernelRelease(k) || versions[version] || files[strings.ToLower(k.File)] {
			return nil, errors.New("invalid or duplicate kernel entry")
		}
		versions[version], files[strings.ToLower(k.File)] = true, true
	}
	return &index, nil
}
func FetchKernelIndex(ctx context.Context, raw string) (*KernelIndex, error) {
	return fetchKernelIndex(ctx, &http.Client{Timeout: 20 * time.Second}, raw)
}
func fetchKernelIndex(ctx context.Context, client *http.Client, raw string) (*KernelIndex, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "andr36oid-sdflasher")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kernel index returned %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 2<<20 {
		return nil, errors.New("kernel index exceeds allowed size")
	}
	return ParseKernelIndex(b)
}

// An empty Version means an older image has no reliable kernel release version.
// Exact build compatibility is still mandatory; an applied marker supplies Version
// on later checks. Image release versions are not comparable to kernel releases.
func CheckKernelUpdate(ctx context.Context, currentVersion firmware.KernelIdentity) (*KernelOffer, error) {
	return checkKernelUpdate(ctx, &http.Client{Timeout: 20 * time.Second}, KernelIndexURL, currentVersion)
}
func checkKernelUpdate(ctx context.Context, client *http.Client, endpoint string, current firmware.KernelIdentity) (*KernelOffer, error) {
	if current.Build == "" {
		return nil, nil
	}
	if current.Version != "" && firmware.KernelVersion(current.Version) == "" {
		return nil, errors.New("invalid installed kernel version")
	}
	index, err := fetchKernelIndex(ctx, client, endpoint)
	if err != nil {
		return nil, err
	}
	var offer *KernelOffer
	for _, k := range index.Kernels {
		if !firmware.KernelCompatible(k.CompatibleBuilds, current.Build) {
			continue
		}
		if current.Version != "" && semver.Compare(firmware.KernelVersion(k.Version), firmware.KernelVersion(current.Version)) <= 0 {
			continue
		}
		if offer == nil || semver.Compare(firmware.KernelVersion(k.Version), firmware.KernelVersion(offer.Version)) > 0 {
			offer = &KernelOffer{KernelRelease: k, URL: index.BaseURL + k.File, Current: current}
		}
	}
	return offer, nil
}
func DownloadKernel(ctx context.Context, offer *KernelOffer, dir string, progress func(int64, int64)) (string, error) {
	if offer == nil || !validKernelRelease(offer.KernelRelease) || !strings.HasSuffix(offer.URL, "/"+offer.File) || !validKernelBaseURL(strings.TrimSuffix(offer.URL, offer.File)) || !firmware.KernelCompatible(offer.CompatibleBuilds, offer.Current.Build) {
		return "", errors.New("invalid kernel offer")
	}
	return downloadRelease(ctx, Asset{Name: offer.File, Size: offer.Size, URL: offer.URL, Digest: "sha256:" + offer.SHA256}, KernelRepository, dir, progress)
}
