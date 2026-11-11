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
	"os"
	"path/filepath"
	"strings"

	"github.com/xi2/xz"
)

// GuestRepository publishes dArkOSen as an EROFS image the console runs as it is
// (its workflow converts each dArkOSen release and verifies every file). A full
// release there says the image works with andr36oid; pre-releases are still being
// tested and are never offered.
const GuestRepository = "andr36oid/dArkOSen-R36S"

const guestManifest = "manifest.json"

// GuestPart is one file of the packed image.
type GuestPart struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// GuestRelease is manifest.json of a release (erofs/manifest.py in the repository
// documents it), with the release's download links.
type GuestRelease struct {
	Format   int    `json:"format"`
	Darkosen string `json:"darkosen"`
	Revision int    `json:"revision"`
	Image    struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"image"`
	Download struct {
		Compression string      `json:"compression"`
		Size        int64       `json:"size"`
		SHA256      string      `json:"sha256"`
		Parts       []GuestPart `json:"parts"`
	} `json:"download"`
	PartitionBytes int64  `json:"partition_bytes"`
	Minimum        string `json:"minimum_andr36oid"`
	Tag            string `json:"-"`
	assets         []Asset
}

func sha(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}

func plainName(s string) bool {
	return s != "" && len(s) <= 128 && s == filepath.Base(s) && !strings.ContainsAny(s, "/\\\x00") && s != "." && s != ".."
}

// parseGuest checks a manifest: everything the flasher acts on has to be there and sane.
func parseGuest(b []byte) (*GuestRelease, error) {
	var g GuestRelease
	if e := json.Unmarshal(b, &g); e != nil {
		return nil, e
	}
	bad := errors.New("this dArkOSen release needs a newer flasher")
	if g.Format != 1 || g.Download.Compression != "xz" {
		return nil, bad
	}
	if g.Darkosen == "" || len(g.Darkosen) > 32 || g.Revision < 1 || !plainName(g.Image.Name) || !sha(g.Image.SHA256) ||
		g.Image.Size <= 0 || g.Image.Size%512 != 0 || g.Image.Size > 64<<30 || g.PartitionBytes < g.Image.Size || g.PartitionBytes > 128<<30 ||
		len(g.Download.Parts) == 0 || len(g.Download.Parts) > 64 || !sha(g.Download.SHA256) {
		return nil, errors.New("invalid dArkOSen release")
	}
	var total int64
	seen := map[string]bool{}
	for _, p := range g.Download.Parts {
		if !plainName(p.Name) || seen[p.Name] || p.Size <= 0 || !sha(p.SHA256) {
			return nil, errors.New("invalid dArkOSen release")
		}
		seen[p.Name] = true
		total += p.Size
	}
	if total != g.Download.Size {
		return nil, errors.New("invalid dArkOSen release")
	}
	return &g, nil
}

// bind ties the manifest's parts to the release's files: same name, size and checksum
// as GitHub reports for them.
func (g *GuestRelease) bind(assets []Asset) error {
	g.assets = nil
	for _, p := range g.Download.Parts {
		found := false
		for _, a := range assets {
			if a.Name == p.Name {
				if a.Size != p.Size || strings.TrimPrefix(a.Digest, "sha256:") != p.SHA256 {
					return errors.New("the dArkOSen release's files don't match its description")
				}
				g.assets, found = append(g.assets, a), true
			}
		}
		if !found {
			return errors.New("the dArkOSen release is missing files")
		}
	}
	return nil
}

// DownloadBytes is what DownloadGuest fetches; UnpackedBytes what it leaves on disk.
func (g *GuestRelease) DownloadBytes() int64 { return g.Download.Size }

// LatestGuest returns the newest full dArkOSen release, nil when there is none.
func LatestGuest(ctx context.Context) (*GuestRelease, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=30", GuestRepository), nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "andr36oid-sdflasher")
	resp, e := client().Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub returned %s", resp.Status)
	}
	var rel []release
	if e = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&rel); e != nil {
		return nil, e
	}
	for _, r := range rel {
		if r.Draft || r.Pre {
			continue
		}
		for _, a := range r.Assets {
			if a.Name != guestManifest || a.Size <= 0 || a.Size > 1<<20 || !validReleaseURL(a.URL, GuestRepository) {
				continue
			}
			b, e := fetchSmall(ctx, a)
			if e != nil {
				return nil, e
			}
			g, e := parseGuest(b)
			if e != nil {
				return nil, e
			}
			if e = g.bind(r.Assets); e != nil {
				return nil, e
			}
			g.Tag = r.Tag
			return g, nil
		}
	}
	return nil, nil
}

func fetchSmall(ctx context.Context, a Asset) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
	if e != nil {
		return nil, e
	}
	resp, e := client().Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download returned %s", resp.Status)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, a.Size+1))
	if e != nil {
		return nil, e
	}
	sum := sha256.Sum256(b)
	if int64(len(b)) != a.Size || "sha256:"+hex.EncodeToString(sum[:]) != a.Digest {
		return nil, errors.New("download checksum mismatch")
	}
	return b, nil
}

// DownloadGuest fetches the release into dir and returns the unpacked image, checked
// against the manifest. An image already there from an earlier run is used again.
// progress counts the packed bytes, first downloading, then unpacking.
func DownloadGuest(ctx context.Context, g *GuestRelease, dir string, progress func(phase string, done, total int64)) (string, error) {
	final := filepath.Join(dir, g.Image.SHA256+"-"+g.Image.Name)
	if st, e := os.Stat(final); e == nil && st.Size() == g.Image.Size {
		return final, nil
	}
	if len(g.assets) != len(g.Download.Parts) {
		return "", errors.New("the dArkOSen release has no downloads")
	}
	var parts []string
	defer func() {
		for _, p := range parts {
			os.Remove(p)
		}
	}()
	var before int64
	for _, a := range g.assets {
		p, e := downloadRelease(ctx, a, GuestRepository, dir, func(n, _ int64) {
			if progress != nil {
				progress("download", before+n, g.Download.Size)
			}
		})
		if e != nil {
			return "", e
		}
		parts = append(parts, p)
		before += a.Size
	}
	return unpackGuest(ctx, g, parts, dir, progress)
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
	n   *int64
}

func (c ctxReader) Read(b []byte) (int, error) {
	if e := c.ctx.Err(); e != nil {
		return 0, e
	}
	n, e := c.r.Read(b)
	*c.n += int64(n)
	return n, e
}

// unpackGuest joins the parts, unpacks them and keeps the image only if its size and
// checksum are the manifest's.
func unpackGuest(ctx context.Context, g *GuestRelease, parts []string, dir string, progress func(string, int64, int64)) (string, error) {
	var readers []io.Reader
	for _, p := range parts {
		f, e := os.Open(p)
		if e != nil {
			return "", e
		}
		defer f.Close()
		readers = append(readers, f)
	}
	var packed int64
	z, e := xz.NewReader(ctxReader{ctx, io.MultiReader(readers...), &packed}, 0)
	if e != nil {
		return "", e
	}
	out, e := os.CreateTemp(dir, "darkosen-*.part")
	if e != nil {
		return "", e
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	defer out.Close()
	h := sha256.New()
	buf := make([]byte, 4<<20)
	var n int64
	for {
		read, re := z.Read(buf)
		if read > 0 {
			if n += int64(read); n > g.Image.Size {
				return "", errors.New("the dArkOSen download is damaged")
			}
			h.Write(buf[:read])
			if _, e = out.Write(buf[:read]); e != nil {
				return "", e
			}
			if progress != nil {
				progress("unpack", min(packed, g.Download.Size), g.Download.Size)
			}
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", errors.New("the dArkOSen download is damaged")
		}
	}
	if n != g.Image.Size || hex.EncodeToString(h.Sum(nil)) != g.Image.SHA256 {
		return "", errors.New("the dArkOSen download is damaged")
	}
	if e = out.Sync(); e != nil {
		return "", e
	}
	if e = out.Close(); e != nil {
		return "", e
	}
	final := filepath.Join(dir, g.Image.SHA256+"-"+g.Image.Name)
	if e = os.Rename(tmp, final); e != nil {
		return "", e
	}
	return final, nil
}
