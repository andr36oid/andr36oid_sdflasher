package catalog

// Adversarial GitHub answers for the release and game system lookups and downloads,
// served by an in-memory transport (no network).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type funcTransport func(*http.Request) (*http.Response, error)

func (f funcTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func withTransport(t *testing.T, rt http.RoundTripper) {
	old := http.DefaultTransport
	http.DefaultTransport = rt
	t.Cleanup(func() { http.DefaultTransport = old })
}

func serve(h http.HandlerFunc) http.RoundTripper {
	return funcTransport(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		h(w, r)
		return w.Result(), nil
	})
}

// A connection that never answers ends the lookup after guestLookupTimeout, not after
// the half hour of the download client: the apps hold the write back meanwhile.
func TestHarnessGuestLookupStalls(t *testing.T) {
	old := guestLookupTimeout
	guestLookupTimeout = 200 * time.Millisecond
	t.Cleanup(func() { guestLookupTimeout = old })
	withTransport(t, funcTransport(func(r *http.Request) (*http.Response, error) {
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(10 * time.Second):
			return nil, errors.New("not stopped")
		}
	}))
	start := time.Now()
	g, e := LatestGuest(context.Background(), GuestSources[0])
	if g != nil || e == nil || time.Since(start) > 5*time.Second {
		t.Fatal("stalled lookup:", g, e, time.Since(start))
	}
}

func releaseJSON(rel ...map[string]any) []byte { b, _ := json.Marshal(rel); return b }

// GitHub's errors and odd answers: every one is an error or "none", never a panic or a
// release the flasher would act on.
func TestHarnessGuestLookupAnswers(t *testing.T) {
	src, _ := FindGuestSource("dArkOSen")
	manifest, assets := manifestFor("", "dArkOSen_R36", "11112026")
	url := func(name string) string {
		return "https://github.com/" + src.Repository + "/releases/download/11112026-erofs1/" + name
	}
	var list []map[string]any
	for _, a := range assets {
		list = append(list, map[string]any{"name": a.Name, "size": a.Size, "digest": a.Digest, "browser_download_url": url(a.Name)})
	}
	ms := fmt.Sprintf("%x", sha256Sum(manifest))
	good := map[string]any{"name": "x", "tag_name": "11112026-erofs1", "assets": append(list, map[string]any{"name": "manifest.json", "size": len(manifest), "digest": "sha256:" + ms, "browser_download_url": url("manifest.json")})}
	for _, c := range []struct {
		name     string
		status   int
		body     []byte
		manifest []byte
		wantNil  bool
		wantErr  bool
	}{
		{"rate limited", 403, []byte(`{"message":"API rate limit exceeded"}`), nil, true, true},
		{"server error", 502, []byte("bad gateway"), nil, true, true},
		{"not json", 200, []byte("<html>"), nil, true, true},
		{"object, not a list", 200, []byte(`{"message":"Not Found"}`), nil, true, true},
		{"empty list", 200, []byte(`[]`), nil, true, false},
		{"drafts and pre-releases only", 200, releaseJSON(map[string]any{"tag_name": "11112026-erofs1", "draft": true}, map[string]any{"tag_name": "11112026-erofs2", "prerelease": true}), nil, true, false},
		{"tag of another form", 200, releaseJSON(map[string]any{"tag_name": "v1.0", "assets": good["assets"]}), nil, true, false},
		{"no manifest", 200, releaseJSON(map[string]any{"tag_name": "11112026-erofs1", "assets": list}), nil, true, true},
		{"manifest damaged", 200, releaseJSON(good), append([]byte{}, manifest[:len(manifest)-1]...), true, true},
		{"good", 200, releaseJSON(good), manifest, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			withTransport(t, serve(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/manifest.json") {
					body := c.manifest
					if body == nil {
						body = manifest
					}
					w.Write(body)
					return
				}
				w.WriteHeader(c.status)
				w.Write(c.body)
			}))
			g, e := LatestGuest(context.Background(), src)
			if (g == nil) != c.wantNil || (e != nil) != c.wantErr {
				t.Fatalf("got %v, %v", g, e)
			}
		})
	}
}

// A manifest that names another system than the repository's is refused; an
// oversized manifest asset is never fetched.
func TestHarnessGuestManifestMismatch(t *testing.T) {
	src, _ := FindGuestSource("dArkOSRE")
	manifest, assets := manifestFor("dArkOS", "dArkOS_RG351MP", "11072026")
	r := &release{Tag: "11072026-erofs1"}
	for _, a := range assets {
		r.Assets = append(r.Assets, a)
	}
	if _, e := guestFromRelease(src, r, manifest); e == nil || !strings.Contains(e.Error(), "describes") {
		t.Fatal(e)
	}
	big := Asset{Name: "manifest.json", Size: 2 << 20, URL: "https://github.com/" + src.Repository + "/releases/download/03082026-erofs1/manifest.json"}
	fetched := false
	withTransport(t, serve(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "manifest.json") {
			fetched = true
		}
		w.Write(releaseJSON(map[string]any{"tag_name": "03082026-erofs1", "assets": []any{map[string]any{"name": big.Name, "size": big.Size, "browser_download_url": big.URL, "digest": "sha256:" + strings.Repeat("a", 64)}}}))
	}))
	if g, e := LatestGuest(context.Background(), src); g != nil || e == nil || fetched {
		t.Fatal("oversized manifest:", g, e, fetched)
	}
}

// Downloads: cut short, too long, wrong checksum, server error. Each is an error and
// leaves no file behind; a good one is kept under its digest and used again.
func TestHarnessDownloadDamaged(t *testing.T) {
	data := bytes.Repeat([]byte("release image "), 100000)
	digest := fmt.Sprintf("%x", sha256Sum(data))
	a := Asset{Name: "andr36oid-20261116-2249-5.10-vulkan.img.xz", Size: int64(len(data)), Digest: "sha256:" + digest,
		URL: "https://github.com/" + Repository + "/releases/download/20261116/andr36oid-20261116-2249-5.10-vulkan.img.xz"}
	for _, c := range []struct {
		name string
		body []byte
		code int
	}{
		{"cut short", data[:len(data)/2], 200},
		{"too long", append(append([]byte{}, data...), 'x'), 200},
		{"flipped byte", append(append([]byte{}, data[:10]...), append([]byte{data[10] ^ 1}, data[11:]...)...), 200},
		{"not found", []byte("Not Found"), 404},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			withTransport(t, serve(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(c.code); w.Write(c.body) }))
			if p, e := Download(context.Background(), a, dir, nil); e == nil {
				t.Fatal("damaged download kept:", p)
			}
			if left, _ := os.ReadDir(dir); len(left) != 0 {
				t.Fatal("left behind:", left[0].Name())
			}
		})
	}
	dir := t.TempDir()
	withTransport(t, serve(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	p, e := Download(context.Background(), a, dir, nil)
	if e != nil || filepath.Base(p) != digest+"-"+a.Name {
		t.Fatal(p, e)
	}
	// a release without a digest, or with a URL of another repository, is refused
	for _, bad := range []Asset{{Name: a.Name, Size: a.Size, URL: a.URL}, {Name: a.Name, Size: a.Size, Digest: a.Digest, URL: "https://github.com/someone/else/releases/download/x/" + a.Name}} {
		if _, e := Download(context.Background(), bad, t.TempDir(), nil); e == nil {
			t.Fatal("accepted", bad)
		}
	}
}

// A connection that drops in the middle: the error comes back and nothing is kept.
func TestHarnessDownloadInterrupted(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 1<<20)
	digest := fmt.Sprintf("%x", sha256Sum(data))
	a := Asset{Name: "a.img.xz", Size: int64(len(data)), Digest: "sha256:" + digest, URL: "https://github.com/" + Repository + "/releases/download/t/a.img.xz"}
	withTransport(t, funcTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(io.MultiReader(bytes.NewReader(data[:1000]), errReader{})), Header: http.Header{}, Request: r}, nil
	}))
	dir := t.TempDir()
	if _, e := Download(context.Background(), a, dir, nil); e == nil || !strings.Contains(e.Error(), "connection reset") {
		t.Fatal(e)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatal("left behind:", left[0].Name())
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset by peer") }

// The release list: GitHub's errors are errors; releases with only some of the four
// variants, odd names and checksum files list only the images.
func TestHarnessReleaseList(t *testing.T) {
	withTransport(t, serve(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	if _, e := List(context.Background()); e == nil || !strings.Contains(e.Error(), "403") {
		t.Fatal(e)
	}
	pub := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	rel := []release{{Tag: "20261116", Published: pub, Assets: []Asset{
		{Name: "andr36oid-20261116-2249-5.10-vulkan.img.xz"},
		{Name: "andr36oid-20261116-2249-5.10-vulkan.img.xz.sha256"},
		{Name: "andr36oid-20261116-2249-4.19-gles.img.gz"},
		{Name: "andr36oid-20261116-2249-4.19-gles.img.xz"},
		{Name: "pack.log"},
		{Name: "ANDR36OID-20261116-K510-VULKAN.IMG.XZ"},
	}}, {Tag: "pre", Pre: true, Published: pub, Assets: []Asset{{Name: "andr36oid-20261117-0101-5.10-vulkan.img.xz"}}}}
	var names []string
	for _, a := range usable(rel) {
		names = append(names, a.Name+"|"+a.Release)
	}
	want := "andr36oid-20261116-2249-5.10-vulkan.img.xz|20261116,andr36oid-20261116-2249-4.19-gles.img.xz|20261116,ANDR36OID-20261116-K510-VULKAN.IMG.XZ|20261116,andr36oid-20261117-0101-5.10-vulkan.img.xz|pre (pre-release)"
	if strings.Join(names, ",") != want {
		t.Fatal(strings.Join(names, ","))
	}
}

func sha256Sum(b []byte) []byte { s := sha256.Sum256(b); return s[:] }

// A release already in the cache (named by its digest, checksum right) is used again
// without the network; a damaged cached copy is downloaded again.
func TestHarnessDownloadUsesTheCache(t *testing.T) {
	data := bytes.Repeat([]byte("cached image "), 50000)
	digest := fmt.Sprintf("%x", sha256Sum(data))
	a := Asset{Name: "andr36oid-20261116-2249-5.10-vulkan.img.xz", Size: int64(len(data)), Digest: "sha256:" + digest,
		URL: "https://github.com/" + Repository + "/releases/download/20261116/andr36oid-20261116-2249-5.10-vulkan.img.xz"}
	dir := t.TempDir()
	requests := 0
	withTransport(t, serve(func(w http.ResponseWriter, r *http.Request) { requests++; w.Write(data) }))
	first, e := Download(context.Background(), a, dir, nil)
	if e != nil || requests != 1 {
		t.Fatal(e, requests)
	}
	again, e := Download(context.Background(), a, dir, nil)
	if e != nil || again != first || requests != 1 {
		t.Fatal("cached release downloaded again:", e, requests)
	}
	b, _ := os.ReadFile(first)
	b[100] ^= 1
	os.WriteFile(first, b, 0600)
	if _, e = Download(context.Background(), a, dir, nil); e != nil || requests != 2 {
		t.Fatal("damaged cache used:", e, requests)
	}
	if b, _ = os.ReadFile(first); !bytes.Equal(b, data) {
		t.Fatal("cache not repaired")
	}
}

// A release whose BOOT record the console would reject (RECORD-FORMAT: 8 digit
// version, 32 bit revision, short plain .img name, image of at least 4 KiB) is refused
// before its download, not after.
func TestHarnessGuestRecordContract(t *testing.T) {
	ok, _ := manifestFor("dArkOS", "dArkOS_RG351MP", "11072026")
	if _, e := parseGuest(ok); e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct{ key, value string }{
		{"darkosen", `"1.0"`}, {"darkosen", `"110720260"`}, {"revision", `4294967296`},
		{"name", `"dArkOS image.erofs.img"`}, {"name", `"dArkOS_RG351MP_11072026.erofs"`},
		{"name", `"` + strings.Repeat("a", 75) + `.img"`}, {"name", `"dÄrkOS.img"`}, {"size", `512`},
	} {
		var m map[string]any
		json.Unmarshal(ok, &m)
		var v any
		json.Unmarshal([]byte(c.value), &v)
		switch c.key {
		case "name", "size":
			m["image"].(map[string]any)[c.key] = v
		default:
			m[c.key] = v
		}
		if c.key == "size" {
			m["partition_bytes"] = int64(5 << 30)
		}
		raw, _ := json.Marshal(m)
		if _, e := parseGuest(raw); e == nil {
			t.Errorf("%s=%s accepted", c.key, c.value)
		}
	}
	long, _ := manifestFor("dArkOS", strings.Repeat("a", 64), "11072026") // 64+19 bytes
	if _, e := parseGuest(long); e == nil {
		t.Error("78+ byte image name accepted")
	}
}

// The game system download stops before it starts when the disk can't hold the parts
// and the unpacked image together; one already unpacked needs nothing.
func TestHarnessGuestNeedsFreeSpace(t *testing.T) {
	manifest, assets := manifestFor("dArkOS", "dArkOS_RG351MP", "11112026")
	g, e := parseGuest(manifest)
	if e != nil {
		t.Fatal(e)
	}
	g.assets = assets
	dir := t.TempDir()
	var asked int64
	old := spaceFor
	t.Cleanup(func() { spaceFor = old })
	spaceFor = func(_ string, need int64) error { asked = need; return errors.New("no space") }
	if _, e = DownloadGuest(context.Background(), g, dir, nil); e == nil || e.Error() != "no space" || asked != g.Download.Size+g.Image.Size {
		t.Fatal(e, asked)
	}
	os.WriteFile(filepath.Join(dir, g.Image.SHA256+"-"+g.Image.Name), nil, 0600)
	os.Truncate(filepath.Join(dir, g.Image.SHA256+"-"+g.Image.Name), g.Image.Size)
	if g.NeedBytes(dir) != 0 {
		t.Fatal("an unpacked image needs space again")
	}
	// a release download asks for its own size first
	spaceFor = func(_ string, need int64) error { asked = need; return errors.New("no space") }
	a := Asset{Name: "andr36oid.img.xz", Size: 12345, URL: "https://github.com/" + Repository + "/releases/download/v1/andr36oid.img.xz", Digest: "sha256:" + strings.Repeat("c", 64)}
	if _, e = Download(context.Background(), a, dir, nil); e == nil || asked != 12345 {
		t.Fatal(e, asked)
	}
}

// A lookup or download stopped by the user ends at once with context.Canceled.
func TestHarnessGuestDownloadCancels(t *testing.T) {
	manifest, assets := manifestFor("dArkOS", "dArkOS_RG351MP", "11112026")
	g, _ := parseGuest(manifest)
	for i := range assets {
		assets[i].URL = "https://github.com/andr36oid/dArkOS-erofs/releases/download/x/" + assets[i].Name
	}
	g.assets, g.Source = assets, GuestSources[0]
	old := spaceFor
	t.Cleanup(func() { spaceFor = old })
	spaceFor = func(string, int64) error { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	withTransport(t, funcTransport(func(r *http.Request) (*http.Response, error) {
		cancel()
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	if _, e := DownloadGuest(ctx, g, t.TempDir(), nil); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := LatestGuest(ctx, GuestSources[0]); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

// A release in a form this flasher doesn't know says so (the apps tell it apart from
// a lookup that failed).
func TestHarnessGuestNewerFlasher(t *testing.T) {
	manifest, _ := manifestFor("dArkOS", "dArkOS_RG351MP", "11112026")
	var m map[string]any
	json.Unmarshal(manifest, &m)
	m["format"] = 2
	b, _ := json.Marshal(m)
	if _, e := parseGuest(b); !errors.Is(e, ErrGuestNeedsNewerFlasher) {
		t.Fatal(e)
	}
}
