package firmware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
)

func testHotfix(data string) Hotfix {
	sum := sha256.Sum256([]byte(data))
	return Hotfix{Name: "audio", File: "audio.fix.zip", Title: "Audio fix", Description: "Restores audio", Build: "v2026-10-18-release|eng.kenny.20261018.0100", Type: "bugfix", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
}
func testIndex(t *testing.T, h []Hotfix) []byte {
	t.Helper()
	b, e := json.Marshal(HotfixIndex{Format: 1, BaseURL: "https://example.com/", Hotfixes: h})
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestHotfixIndexAndMatching(t *testing.T) {
	h := testHotfix("zip")
	extra := h
	extra.File = "extra.zip"
	extra.Type = "future-kind"
	wrong := h
	wrong.Build = "different|incremental"
	b := testIndex(t, []Hotfix{h, extra, wrong})
	b = append(b[:len(b)-1], []byte(`,"future":{"field":true}}`)...)
	index, e := ParseHotfixIndex(b)
	if e != nil {
		t.Fatal(e)
	}
	offer := index.ForBuild(h.Build)
	if len(offer.Hotfixes) != 2 || !offer.Hotfixes[0].Preselected() || offer.Hotfixes[1].Preselected() || offer.Hotfixes[1].Type != "extra" {
		t.Fatalf("offer %+v", offer)
	}
	for _, build := range []string{"", "different", "v2026-10-18-release|other"} {
		if len(index.ForBuild(build).Hotfixes) != 0 {
			t.Fatalf("offered hotfixes for %q", build)
		}
	}
	for _, raw := range []string{"", `{`, `{"format":2}`, `{"format":1,"base_url":"file:///tmp/"}`} {
		if _, e := ParseHotfixIndex([]byte(raw)); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, e := ParseHotfixIndex([]byte(`{"format":2}`)); e == nil || e.Error() != HotfixNewFormat {
		t.Fatalf("format error %v", e)
	}
	for _, name := range []string{"../escape.zip", "hotfix/nested.zip", "..\\escape.zip", "bad:zip.zip", "audio.zip?x"} {
		bad := h
		bad.File = name
		if _, e := ParseHotfixIndex(testIndex(t, []Hotfix{bad})); e == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if _, e := ParseHotfixIndex(testIndex(t, []Hotfix{h, h})); e == nil {
		t.Fatal("duplicate accepted")
	}
}

func TestDownloadHotfixVerification(t *testing.T) {
	data := "verified zip contents"
	endpoint := mockHotfixHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, data) }))

	for _, kind := range []string{"good", "short", "long", "hash"} {
		t.Run(kind, func(t *testing.T) {
			h := testHotfix(data)
			switch kind {
			case "short":
				h.Size++
			case "long":
				h.Size--
			case "hash":
				h.SHA256 = strings.Repeat("0", 64)
			}
			dir := t.TempDir()
			calls := 0
			p, e := DownloadHotfix(context.Background(), endpoint+"/", h, dir, func(n, total int64) { calls++ })
			if kind == "good" {
				if e != nil {
					t.Fatal(e)
				}
				b, _ := os.ReadFile(p)
				if string(b) != data || calls == 0 {
					t.Fatal("download/progress mismatch")
				}
				os.Remove(p)
			} else if e == nil {
				t.Fatal("bad download accepted")
			}
			files, _ := os.ReadDir(dir)
			if len(files) != 0 {
				t.Fatal("temporary download left behind")
			}
		})
	}
}

func TestWriteHotfixPreservesAndOverwrites(t *testing.T) {
	f, tab := bootOnlyImage(t, map[string]string{BuildInfoFile: testBuildInfo, "unrelated.txt": "keep"})
	p, _ := tab.Find("BOOT")
	// Use the same staged FAT path as production.
	stage, e := os.CreateTemp(t.TempDir(), "boot-*.fat")
	if e != nil {
		t.Fatal(e)
	}
	defer stage.Close()
	if _, e = io.Copy(stage, io.NewSectionReader(f, p.Offset(), p.Size())); e != nil {
		t.Fatal(e)
	}
	boot, e := fat32.Read(file.New(stage, false), p.Size(), 0, 512)
	if e != nil {
		t.Fatal(e)
	}
	if e = boot.Mkdir("hotfix"); e != nil {
		t.Fatal(e)
	}
	other := testHotfix("other zip")
	other.File = "other.zip"
	for _, h := range []Hotfix{other, testHotfix("long zip contents"), testHotfix("new")} {
		data := "other zip"
		if h.File != "other.zip" {
			if h.Size == 3 {
				data = "new"
			} else {
				data = "long zip contents"
			}
		}
		if e = WriteHotfix(boot, h, []byte(data)); e != nil {
			t.Fatal(e)
		}
	}
	for name, want := range map[string]string{"/hotfix/audio.fix.zip": "new", "/hotfix/other.zip": "other zip", "/unrelated.txt": "keep", "/" + BuildInfoFile: testBuildInfo} {
		got, e := ReadFile(boot, name, 64<<10)
		if e != nil || string(got) != want {
			t.Fatalf("%s: %q, %v", name, got, e)
		}
	}
	if e = WriteHotfix(boot, testHotfix("new"), []byte("bad")); e == nil {
		t.Fatal("unverified bytes accepted")
	}
}

type boundedHotfixDisk struct {
	*os.File
	start, end int64
	writes     int
}

func (d *boundedHotfixDisk) WriteAt(b []byte, off int64) (int, error) {
	if off < d.start || off+int64(len(b)) > d.end || off%512 != 0 || len(b)%512 != 0 {
		return 0, errors.New("write outside BOOT or unaligned")
	}
	d.writes++
	return d.File.WriteAt(b, off)
}
func (d *boundedHotfixDisk) ReadAt(b []byte, off int64) (int, error) {
	if off%512 != 0 || len(b)%512 != 0 {
		return 0, errors.New("unaligned device read")
	}
	return d.File.ReadAt(b, off)
}
func TestApplyHotfixesBOOTOnlyAndDownloadFailure(t *testing.T) {
	data := "new zip"
	endpoint := mockHotfixHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, data) }))

	f, tab := bootOnlyImage(t, map[string]string{BuildInfoFile: testBuildInfo, "keep.txt": "unrelated"})
	head, tail, e := tab.Encode()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteAt(head, 0); e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteAt(tail, tab.Size-int64(len(tail))); e != nil {
		t.Fatal(e)
	}
	p, _ := tab.Find("BOOT")
	disk := &boundedHotfixDisk{File: f, start: p.Offset(), end: p.Offset() + p.Size()}
	h := testHotfix(data)
	bad := h
	bad.File = "bad.zip"
	bad.SHA256 = strings.Repeat("0", 64)
	offer := &HotfixOffer{Build: h.Build, BaseURL: endpoint + "/", Hotfixes: []Hotfix{h, bad}}
	phases := map[string]bool{}
	results, e := ApplyHotfixes(context.Background(), disk, tab.Size, offer, []string{h.File, bad.File}, t.TempDir(), func(p HotfixProgress) { phases[p.Phase] = true })
	if e != nil {
		t.Fatal(e)
	}
	if len(results) != 2 || results[0].Error != "" || results[1].Error == "" || disk.writes == 0 {
		t.Fatalf("results %+v, writes %d", results, disk.writes)
	}
	if !phases["Downloading hotfix"] || !phases["Verifying hotfix"] || !phases["Writing hotfixes"] {
		t.Fatalf("progress %v", phases)
	}
	boot, e := OpenBoot(disk, tab, true)
	if e != nil {
		t.Fatal(e)
	}
	if b, e := ReadFile(boot, "/hotfix/"+h.File, 1024); e != nil || string(b) != data {
		t.Fatalf("installed %q %v", b, e)
	}
	if _, e := ReadFile(boot, "/hotfix/"+bad.File, 1024); e == nil {
		t.Fatal("failed download placed on BOOT")
	}
	if b, _ := ReadFile(boot, "/keep.txt", 1024); string(b) != "unrelated" {
		t.Fatal("other file changed")
	}
	before := disk.writes
	// Reapplying a failed download must leave the existing same-named ZIP intact.
	corrupt := h
	corrupt.SHA256 = strings.Repeat("0", 64)
	failed := &HotfixOffer{Build: h.Build, BaseURL: endpoint + "/", Hotfixes: []Hotfix{corrupt}}
	failedResults, err := ApplyHotfixes(context.Background(), disk, tab.Size, failed, []string{h.File}, t.TempDir(), nil)
	if err != nil || len(failedResults) != 1 || failedResults[0].Error == "" || disk.writes != before {
		t.Fatalf("failed replacement: %+v, %v, writes %d", failedResults, err, disk.writes)
	}
	if b, err := ReadFile(boot, "/hotfix/"+h.File, 1024); err != nil || string(b) != data {
		t.Fatal("failed replacement changed installed ZIP")
	}
	offer.Build = "wrong|build"
	if _, e := ApplyHotfixes(context.Background(), disk, tab.Size, offer, nil, t.TempDir(), nil); e == nil || disk.writes != before {
		t.Fatal("mismatched build accepted")
	}
	offer.Build = h.Build
	if _, e := ApplyHotfixes(context.Background(), disk, tab.Size, offer, []string{"unknown.zip"}, t.TempDir(), nil); e == nil || disk.writes != before {
		t.Fatal("unoffered selection accepted")
	}
	if _, e := ApplyHotfixes(context.Background(), disk, tab.Size, offer, nil, t.TempDir(), nil); e != nil || disk.writes != before {
		t.Fatal("empty selection wrote card", e)
	}
}

func TestHotfixBuildIdentityRequiresBothProperties(t *testing.T) {
	for _, info := range []string{"", "format=2\nro.andr36oid.version=v1\nro.product.build.version.incremental=x\n", "format=1\nro.andr36oid.version=v1\n", "format=1\nro.product.build.version.incremental=x\n"} {
		files := map[string]string{}
		if info != "" {
			files[BuildInfoFile] = info
		}
		f, tab := bootOnlyImage(t, files)
		boot, e := OpenBoot(f, tab, true)
		if e != nil {
			t.Fatal(e)
		}
		if got := BuildIdentity(boot); got != "" {
			t.Fatalf("incomplete facts matched %q", got)
		}
	}
}

func TestFetchHotfixIndexErrors(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
		want   string
	}{
		{`{"format":2}`, 200, HotfixNewFormat},
		{`not json`, 200, HotfixFetchFailed},
		{`offline`, 503, HotfixFetchFailed},
	} {
		endpoint := mockHotfixHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
		_, e := FetchHotfixIndex(context.Background(), endpoint)

		if e == nil || e.Error() != tc.want {
			t.Fatalf("error %v, want %q", e, tc.want)
		}
	}
}

// Serve HTTP handlers in memory so verification tests also run without sockets.
type hotfixRoundTripper func(*http.Request) (*http.Response, error)

func (f hotfixRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func mockHotfixHTTP(t *testing.T, handler http.Handler) string {
	t.Helper()
	previous := hotfixHTTP
	hotfixHTTP = &http.Client{Transport: hotfixRoundTripper(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Result(), nil
	})}
	t.Cleanup(func() { hotfixHTTP = previous })
	return "https://hotfix.test"
}
