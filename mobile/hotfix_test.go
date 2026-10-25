package mobile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
)

type hotfixUSB struct {
	*fakeDisk
	first, last int64
}

func (d *hotfixUSB) Write(off int64, b []byte) error {
	if off < d.first || off+int64(len(b)) > d.last || off%512 != 0 || len(b)%512 != 0 {
		return io.ErrShortWrite
	}
	d.writes++
	_, e := d.file.WriteAt(b, off)
	return e
}

type hotfixObserver struct{ events []flashEvent }
type flashEvent struct {
	Phase string `json:"phase"`
	Name  string `json:"name"`
}

func (o *hotfixObserver) Progress(raw string) {
	var p flashEvent
	json.Unmarshal([]byte(raw), &p)
	o.events = append(o.events, p)
}

type hotfixTransport func(*http.Request) (*http.Response, error)

func (f hotfixTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHotfixSessionListApplyAndStaleCard(t *testing.T) {
	session, e := NewSession(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	f, e := os.Create(filepath.Join(t.TempDir(), "card.img"))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	const capacity = 1 << 30
	if e = f.Truncate(capacity); e != nil {
		t.Fatal(e)
	}
	p := layout.Partition{Number: 1, Name: "BOOT", Start: 2048, End: 2048 + (32<<20)/512 - 1, Type: layout.LinuxType, ID: layout.GUID()}
	tab := layout.Table{GPT: true, Size: capacity, Parts: []layout.Partition{p}}
	head, tail, e := tab.Encode()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteAt(head, 0); e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteAt(tail, capacity-int64(len(tail))); e != nil {
		t.Fatal(e)
	}
	boot, e := fat32.Create(file.New(f, false), p.Size(), p.Offset(), 512, "BOOT", true)
	if e != nil {
		t.Fatal(e)
	}
	info := "format=1\nro.andr36oid.version=v1\nro.product.build.version.incremental=inc1\n"
	w, e := boot.OpenFile(firmware.BuildInfoFile, os.O_CREATE|os.O_RDWR)
	if e != nil {
		t.Fatal(e)
	}
	w.Write([]byte(info))
	w.Close()
	data := "verified hotfix"
	sum := sha256.Sum256([]byte(data))
	h := firmware.Hotfix{Name: "fix", Title: "Fix", File: "fix.zip", Build: "v1|inc1", Type: "bugfix", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
	index, _ := json.Marshal(firmware.HotfixIndex{Format: 1, BaseURL: "https://hotfix.test/", Hotfixes: []firmware.Hotfix{h}})
	previous := http.DefaultTransport
	http.DefaultTransport = hotfixTransport(func(r *http.Request) (*http.Response, error) {
		body := string(index)
		if strings.HasSuffix(r.URL.Path, "fix.zip") {
			body = data
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	disk := &hotfixUSB{fakeDisk: &fakeDisk{size: capacity, file: f}, first: p.Offset(), last: p.Offset() + p.Size()}
	observer := &hotfixObserver{}
	if _, e = session.ApplyHotfixes(disk, `["fix.zip"]`, observer); e == nil {
		t.Fatal("apply before fetch accepted")
	}
	raw, e := session.FetchHotfixes(disk, "v1|inc1", observer)
	if e != nil {
		t.Fatal(e)
	}
	var offer firmware.HotfixOffer
	if e = json.Unmarshal([]byte(raw), &offer); e != nil || len(offer.Hotfixes) != 1 || !offer.Hotfixes[0].Preselected() {
		t.Fatalf("offer %s, %v", raw, e)
	}
	if disk.writes != 0 {
		t.Fatal("fetch wrote card")
	}
	raw, e = session.ApplyHotfixes(disk, `["fix.zip"]`, observer)
	if e != nil {
		t.Fatal(e)
	}
	var results []firmware.HotfixResult
	if e = json.Unmarshal([]byte(raw), &results); e != nil || len(results) != 1 || results[0].Error != "" {
		t.Fatalf("results %s %v", raw, e)
	}
	if disk.writes == 0 || disk.flushes == 0 {
		t.Fatal("apply did not write and flush")
	}
	phases := map[string]bool{}
	for _, p := range observer.events {
		phases[p.Phase] = true
	}
	for _, phase := range []string{"Checking for hotfixes", "Downloading hotfix", "Verifying hotfix", "Writing hotfixes"} {
		if !phases[phase] {
			t.Fatalf("missing phase %q", phase)
		}
	}
	before := disk.writes
	if _, e = session.ApplyHotfixes(disk, `["unoffered.zip"]`, nil); e == nil || disk.writes != before {
		t.Fatal("unknown selection accepted")
	}
	if _, e = session.FetchHotfixes(disk, "other|build", nil); e == nil {
		t.Fatal("wrong post-flash build accepted")
	}
	if _, e = session.ApplyHotfixes(disk, `["fix.zip"]`, nil); e == nil || disk.writes != before {
		t.Fatal("stale offer retained after fetch failure")
	}
}
