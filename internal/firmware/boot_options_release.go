package firmware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const BootOptionsCatalogURL = "https://github.com/andr36oid/hotfixes/raw/main/boot-options.json"

type BootFirmwareEntry struct {
	BootRule
	Repository   string `json:"repo"`
	Asset        string `json:"asset"`
	SHA256Source string `json:"sha256_source"`
	Tag          string `json:"tag,omitempty"`
}
type BootCatalog struct {
	Format  int                 `json:"format"`
	Entries []BootFirmwareEntry `json:"entries"`
}
type BootOffers struct {
	BuildsFailed bool               `json:"builds_failed"`
	Builds       []*BootloaderBuild `json:"builds"`
	Catalog      *BootCatalog       `json:"catalog"`
	Rules        []BootRule         `json:"rules"`
}

func ParseBootCatalog(b []byte) (*BootCatalog, error) {
	var c BootCatalog
	if e := json.Unmarshal(b, &c); e != nil {
		return nil, e
	}
	if c.Format != 1 || len(c.Entries) > 64 {
		return nil, errors.New("unsupported boot options catalogue")
	}
	seen := map[string]bool{}
	for _, v := range c.Entries {
		if v.Kind != "sleep" || !bootOptionID.MatchString(v.ID) || v.ID == "image" || v.Label == "" || len(v.Label) > 160 || seen[v.ID] || (v.Tag != "" && v.Tag != "latest" && !bootloaderTag.MatchString(v.Tag)) || v.Repository != StandardBootloaderRepo || v.Asset != "trust.img" || v.SHA256Source != "SHA256SUMS" || len(v.WorksWith.Chains) != 1 || v.WorksWith.Chains[0] != "standard" || len(v.WorksWith.Storage) != 1 || v.WorksWith.Storage[0] != "sd" {
			return nil, errors.New("invalid boot options catalogue entry")
		}
		for _, p := range v.Broken {
			if !containsBoot([]string{"chain", "bootloader", "sleep", "kernel_line", "profile", "storage"}, p.Field) || p.Value == "" || p.Reason == "" {
				return nil, errors.New("invalid compatibility pair")
			}
		}
		seen[v.ID] = true
	}
	return &c, nil
}
func FetchBootCatalog(ctx context.Context) (*BootCatalog, error) {
	b, e := bootloaderRead(ctx, BootOptionsCatalogURL, 256<<10)
	if e != nil {
		return nil, e
	}
	return ParseBootCatalog(b)
}
func CatalogBootRules(c *BootCatalog) []BootRule {
	r := BootRules()
	if c != nil {
		for _, v := range c.Entries {
			r = append(r, v.BootRule)
		}
	}
	return r
}
func FetchBootOffers(ctx context.Context) (*BootOffers, error) {
	b, e := FetchBootloaderChoices(ctx)
	// An unavailable catalogue never hides independently verified bootloader builds.
	c, _ := FetchBootCatalog(ctx)
	return &BootOffers{BuildsFailed: e != nil, Builds: b, Catalog: c, Rules: CatalogBootRules(c)}, e
}

type bootAsset struct {
	repo, tag, name, url, sum string
	size                      int64
}

func releaseBootAssets(ctx context.Context, repo string, names map[string]int64) (map[string]bootAsset, error) {
	return releaseBootAssetsAt(ctx, repo, "latest", names)
}
func releaseBootAssetsAt(ctx context.Context, repo, tag string, names map[string]int64) (map[string]bootAsset, error) {
	if repo != StandardBootloaderRepo && repo != NewBootloaderRepo {
		return nil, errors.New("firmware repository is not allowed")
	}
	if tag == "" {
		tag = "latest"
	}
	if tag != "latest" && !bootloaderTag.MatchString(tag) {
		return nil, errors.New("invalid firmware tag")
	}
	endpoint := "latest"
	if tag != "latest" {
		endpoint = "tags/" + tag
	}
	raw, e := bootloaderRead(ctx, "https://api.github.com/repos/"+repo+"/releases/"+endpoint, 2<<20)
	if e != nil {
		return nil, e
	}
	var r struct {
		Tag    string `json:"tag_name"`
		Draft  bool   `json:"draft"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if e = json.Unmarshal(raw, &r); e != nil {
		return nil, e
	}
	if r.Draft || (tag != "latest" && r.Tag != tag) || !bootloaderTag.MatchString(r.Tag) || len(r.Assets) > 64 {
		return nil, errors.New("invalid firmware release")
	}
	found := map[string]bootAsset{}
	sumsURL := ""
	for _, a := range r.Assets {
		max, wanted := names[a.Name]
		if !wanted && a.Name != "SHA256SUMS" {
			continue
		}
		if !bootloaderAssetURL(a.URL, repo, r.Tag, a.Name) {
			return nil, errors.New("invalid firmware asset URL")
		}
		if a.Name == "SHA256SUMS" {
			if sumsURL != "" || a.Size <= 0 || a.Size > 64<<10 {
				return nil, errors.New("invalid firmware checksums")
			}
			sumsURL = a.URL
			continue
		}
		if _, yes := found[a.Name]; yes || a.Size <= 0 || a.Size > max {
			return nil, errors.New("firmware asset exceeds its slot")
		}
		found[a.Name] = bootAsset{repo: repo, tag: r.Tag, name: a.Name, url: a.URL, size: a.Size}
	}
	if sumsURL == "" || len(found) != len(names) {
		return nil, errors.New("firmware release is incomplete")
	}
	sums, e := bootloaderRead(ctx, sumsURL, 64<<10)
	if e != nil {
		return nil, e
	}
	for name, a := range found {
		for _, line := range strings.Split(string(sums), "\n") {
			f := strings.Fields(line)
			if len(f) != 2 || strings.TrimPrefix(strings.TrimPrefix(f[1], "*"), "./") != name {
				continue
			}
			v, err := hex.DecodeString(f[0])
			if err != nil || len(v) != 32 || a.sum != "" {
				return nil, errors.New("invalid firmware checksum")
			}
			a.sum = strings.ToLower(f[0])
		}
		if a.sum == "" {
			return nil, fmt.Errorf("missing checksum for %s", name)
		}
		found[name] = a
	}
	return found, nil
}
func downloadBootAsset(ctx context.Context, a bootAsset, dir string, check func([]byte) error) ([]byte, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, a.sum+"-"+a.name)
	verify := func(b []byte) error {
		if int64(len(b)) != a.size {
			return errors.New("firmware size differs")
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != a.sum {
			return errors.New("firmware SHA-256 differs")
		}
		return check(b)
	}
	if f, e := os.Open(path); e == nil {
		st, err := f.Stat()
		f.Close()
		if err == nil && st.Size() == a.size {
			if b, e := os.ReadFile(path); e == nil && verify(b) == nil {
				return b, nil
			}
		}
	}
	b, e := bootloaderRead(ctx, a.url, a.size)
	if e != nil {
		return nil, e
	}
	if e = verify(b); e != nil {
		return nil, e
	}
	f, e := os.CreateTemp(dir, "boot-*.tmp")
	if e != nil {
		return nil, e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return nil, e
	}
	if e = f.Sync(); e != nil {
		return nil, e
	}
	if e = f.Close(); e != nil {
		return nil, e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return nil, e
	}
	return b, nil
}
