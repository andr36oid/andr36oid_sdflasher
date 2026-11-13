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
	"path"
	"path/filepath"
	"strings"
	"time"
)

const DTBBetaRepository = "andr36oid/beta_builds"
const DTBReleaseRepository = "andr36oid/releases"

type dtbAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}
type dtbRelease struct {
	Tag    string     `json:"tag_name"`
	Draft  bool       `json:"draft"`
	Assets []dtbAsset `json:"assets"`
}

func dtbReleaseRead(ctx context.Context, raw string, limit int64) ([]byte, error) {
	b, e := bootloaderRead(ctx, raw, limit)
	if errors.Is(e, errBootloaderFetch) {
		return nil, errors.New(DTBPackNoOnline)
	}
	return b, e
}
func dtbAssetBytes(ctx context.Context, a dtbAsset, repo, tag string, limit int64) ([]byte, error) {
	if path.Base(a.Name) != a.Name || a.Size < 1 || a.Size > limit || !bootloaderAssetURL(a.URL, repo, tag, a.Name) {
		return nil, errors.New(DTBPackInvalid)
	}
	if !strings.HasPrefix(a.Digest, "sha256:") {
		return nil, errors.New("device tree release asset has no SHA256 digest")
	}
	raw, e := hex.DecodeString(strings.TrimPrefix(a.Digest, "sha256:"))
	if e != nil || len(raw) != 32 {
		return nil, errors.New(DTBPackInvalid)
	}
	b, e := dtbReleaseRead(ctx, a.URL, limit)
	if e != nil {
		return nil, e
	}
	sum := sha256.Sum256(b)
	if int64(len(b)) != a.Size || !strings.EqualFold(hex.EncodeToString(sum[:]), hex.EncodeToString(raw)) {
		return nil, errors.New(DTBPackInvalid)
	}
	return b, nil
}
func manifestMatches(m DTBManifest, setup CardSetup) bool {
	stamp := strings.SplitN(setup.Build, "|", 2)
	if len(stamp) != 2 || m.Version != setup.Release {
		return false
	}
	for _, b := range m.CompatibleBuilds {
		if b == stamp[1] {
			return true
		}
	}
	return false
}

// FetchDTBPack checks release manifests before downloading a ZIP. A source tag
// recorded at flash time is tried first, then the two public release catalogs.
func FetchDTBPack(ctx context.Context, setup CardSetup, dir string) (*DTBPack, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var best *struct {
		m         DTBManifest
		manifest  []byte
		asset     dtbAsset
		repo, tag string
	}
	inspected := 0
	scan := func(repo string, releases []dtbRelease) error {
		for _, release := range releases {
			if release.Draft || release.Tag == "" {
				continue
			}
			var manifest, zipAsset *dtbAsset
			for i := range release.Assets {
				a := &release.Assets[i]
				if a.Name == "manifest.json" || strings.HasSuffix(a.Name, "-dtbs-manifest.json") {
					if manifest != nil {
						return errors.New("ambiguous device tree manifest")
					}
					manifest = a
				}
				if strings.HasSuffix(a.Name, "-dtbs.zip") {
					if zipAsset != nil {
						return errors.New("ambiguous device tree archive")
					}
					zipAsset = a
				}
			}
			if manifest == nil || zipAsset == nil {
				continue
			}
			inspected++
			if inspected > 32 {
				return errors.New("too many device tree manifests in release catalogs")
			}
			b, e := dtbAssetBytes(ctx, *manifest, repo, release.Tag, 2<<20)
			if e != nil {
				return e
			}
			m, e := decodeDTBManifest(b)
			if e != nil {
				return e
			}
			if !manifestMatches(m, setup) {
				continue
			}
			if best == nil || m.Revision > best.m.Revision {
				best = &struct {
					m         DTBManifest
					manifest  []byte
					asset     dtbAsset
					repo, tag string
				}{m, b, *zipAsset, repo, release.Tag}
			}
			if best != nil {
				return nil
			}
		}
		return nil
	}
	var last error
	for _, repo := range []string{DTBBetaRepository, DTBReleaseRepository} {
		// An exact origin avoids guesses about beta tag naming and long catalogs.
		if setup.Source != nil && setup.Source.Repository == repo && setup.Source.Tag != "" {
			b, e := dtbReleaseRead(ctx, "https://api.github.com/repos/"+repo+"/releases/tags/"+url.PathEscape(setup.Source.Tag), 2<<20)
			if e == nil {
				var release dtbRelease
				if e = json.Unmarshal(b, &release); e != nil {
					return nil, e
				}
				if e = scan(repo, []dtbRelease{release}); e != nil {
					return nil, e
				}
				if best != nil {
					break
				}
			} else {
				last = e
			}
		}
		// Also consult the catalog: a higher revision can supersede an offline set.
		b, e := dtbReleaseRead(ctx, "https://api.github.com/repos/"+repo+"/releases?per_page=100", 8<<20)
		if e != nil {
			last = e
			continue
		}
		var releases []dtbRelease
		if e = json.Unmarshal(b, &releases); e != nil {
			return nil, e
		}
		if e = scan(repo, releases); e != nil {
			return nil, e
		}
		if best != nil {
			break
		}
	}
	if best == nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		_ = last
		return nil, errors.New(DTBPackNoOnline)
	}
	digest := strings.TrimPrefix(best.asset.Digest, "sha256:")
	decoded, decodeErr := hex.DecodeString(digest)
	if !strings.HasPrefix(best.asset.Digest, "sha256:") || decodeErr != nil || len(decoded) != 32 || best.asset.Size < 1 || best.asset.Size > 256<<20 || path.Base(best.asset.Name) != best.asset.Name || !bootloaderAssetURL(best.asset.URL, best.repo, best.tag, best.asset.Name) {
		return nil, errors.New(DTBPackInvalid)
	}
	cache := filepath.Join(dir, "dtbs-"+strings.ToLower(digest)+".zip")
	if cached, e := os.Open(cache); e == nil {
		stat, err := cached.Stat()
		hash := sha256.New()
		if err == nil && stat.Size() == best.asset.Size {
			_, err = io.Copy(hash, io.LimitReader(cached, (256<<20)+1))
		} else {
			err = errors.New("cache size differs")
		}
		cached.Close()
		if err == nil && strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), digest) {
			if pack, e := ReadDTBPackManifest(cache, best.manifest); e == nil {
				pack.Source = &SetupSource{Repository: best.repo, Tag: best.tag}
				if e = saveDTBReference(pack, best.manifest, dir); e == nil {
					return pack, nil
				}
			}
		}
	}
	data, e := dtbAssetBytes(ctx, best.asset, best.repo, best.tag, 256<<20)
	if e != nil {
		return nil, e
	}
	f, e := os.CreateTemp(dir, "dtbs-download-*.zip")
	if e != nil {
		return nil, e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(data); e != nil {
		f.Close()
		return nil, e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return nil, e
	}
	if e = f.Close(); e != nil {
		return nil, e
	}
	p, e := ReadDTBPackManifest(f.Name(), best.manifest)
	if e != nil {
		return nil, e
	}
	p.Source = &SetupSource{Repository: best.repo, Tag: best.tag}
	// Reusable verified cache, keyed by content; the next lookup still fetches the
	// manifest first so a cached older revision cannot win over a release update.
	cache = filepath.Join(dir, fmt.Sprintf("dtbs-%s.zip", p.SHA256))
	if e = os.Rename(f.Name(), cache); e != nil {
		return nil, e
	}
	p.Path = cache
	if e = saveDTBReference(p, best.manifest, dir); e != nil {
		return nil, e
	}

	return p, nil
}

// RecordImageSource remembers the public OS origin for subsequent card edits.
func RecordImageSource(disk HotfixDisk, size int64, expected, source, dir string) error {
	u, e := url.Parse(source)
	if e != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 6 || parts[2] != "releases" || parts[3] != "download" {
		return nil
	}
	repo := parts[0] + "/" + parts[1]
	if repo != DTBBetaRepository && repo != DTBReleaseRepository {
		return nil
	}
	stage, e := stageSetup(disk, size, expected, dir)
	if e != nil {
		return e
	}
	defer stage.Close()
	setup, e := ReadCardSetup(stage.boot)
	if e != nil {
		return e
	}
	setup.Source = &SetupSource{Repository: repo, Tag: parts[4]}
	if e = stage.Save(setup); e != nil {
		return e
	}
	return stage.Commit()
}

func saveDTBReference(p *DTBPack, manifest []byte, dir string) error {
	p.ManifestPath = fmt.Sprintf("%s-r%d.manifest.json", p.Path, p.Manifest.Revision)
	f, e := os.CreateTemp(dir, "dtbs-manifest-*.tmp")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(manifest); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), p.ManifestPath); e != nil {
		return e
	}
	d, e := os.Open(dir)
	if e != nil {
		return e
	}
	e = d.Sync()
	d.Close()
	return e
}
