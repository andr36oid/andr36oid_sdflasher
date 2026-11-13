package firmware

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/andr36oid/andr36oid-sdflasher/internal/dtbconv"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func packFixture(t *testing.T) *DTBPack {
	t.Helper()
	base := dtbconv.EmbeddedBases()
	tr, e := dtbconv.Parse(base.Genuine)
	if e != nil {
		t.Fatal(e)
	}
	tr.Root.SetString("model", "updated test panel")
	next := tr.Bytes()
	p := &DTBPack{Manifest: DTBManifest{Format: 1, Version: "v2026-10-18-release", Revision: 1, CompatibleBuilds: []string{"eng.kenny.20261018.0100"}, Lines: map[string]DTBLine{}}, Files: map[string][]byte{}}
	for _, kernel := range []string{"4.19", "5.10"} {
		line := DTBLine{KernelBuild: 31, PictureSets: map[string]string{"Panels/Panel4": "640x480", "Panels/Panel3": "640x480"}, Files: map[string]DTBFile{}}
		files := map[string][]byte{ProfileList: []byte("Panels/Panel4|R36S|Panel 4|\nPanels/Panel3|R36S|Panel 3|\n")}
		for _, name := range DTBRootFiles {
			files["root/"+name] = base.Genuine
			files["Panels/Panel4/"+name] = next
			files["Panels/Panel3/"+name] = base.Genuine
		}
		for name, b := range files {
			sum := sha256.Sum256(b)
			line.Files[name] = DTBFile{Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])}
			p.Files[kernel+"/"+name] = b
		}
		p.Manifest.Lines[kernel] = line
	}
	return p
}
func packZip(t *testing.T, p *DTBPack, extra map[string][]byte) string {
	t.Helper()
	f, e := os.CreateTemp(t.TempDir(), "dtbs-*.zip")
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	m, _ := json.Marshal(p.Manifest)
	w, _ := z.Create("manifest.json")
	w.Write(m)
	for n, b := range p.Files {
		w, e := z.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		w.Write(b)
	}
	for n, b := range extra {
		w, e := z.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		w.Write(b)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	f.Close()
	return f.Name()
}
func dtbWritable(f *os.File, size int64) (*fat32.FileSystem, error) {
	_, tab, e := CardBuildIdentity(f, size)
	if e != nil {
		return nil, e
	}
	p, _ := tab.Find("BOOT")
	return fat32.Read(file.New(f, false), p.Size(), p.Offset(), 512)
}
func dtbCard(t *testing.T) (*os.File, int64, string) {
	t.Helper()
	f, size := loaderCard(t, map[string]string{BuildInfoFile: "format=1\nro.andr36oid.version=v2026-10-18-release\nro.product.build.version.incremental=eng.kenny.20261018.0100\n", ProfileList: "Panels/Panel4|R36S|Panel 4|\n", "andr36oid-profile.txt": "Panels/Panel4\n"})
	build, _, e := CardBuildIdentity(f, size)
	if e != nil {
		t.Fatal(e)
	}
	boot, e := dtbWritable(f, size)
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range []string{"Panels/Panel4", "Pictures/640x480"} {
		if e = boot.Mkdir(d); e != nil {
			t.Fatal(e)
		}
	}
	for _, n := range DTBRootFiles {
		for _, prefix := range []string{"", "Panels/Panel4/"} {
			if e = writeBootFile(boot, prefix+n, dtbconv.EmbeddedBases().Genuine); e != nil {
				t.Fatal(e)
			}
		}
	}
	for _, n := range PictureFiles {
		for _, prefix := range []string{"", "Pictures/640x480/"} {
			if e = writeBootFile(boot, prefix+n, []byte("picture:"+n)); e != nil {
				t.Fatal(e)
			}
		}
	}
	writeBootFile(boot, "Panels/Panel4/pictures.txt", []byte("640x480\n"))
	writeBootFile(boot, "Panels/Panel4/keep.txt", []byte("keep"))
	f.Sync()
	return f, size, build
}
func TestDTBPackValidation(t *testing.T) {
	p := packFixture(t)
	name := packZip(t, p, nil)
	parsed, e := ReadDTBPack(name)
	if e != nil {
		t.Fatal(e)
	}
	if len(parsed.Files) != len(p.Files)+1 {
		t.Fatal("file set")
	}
	for _, bad := range []string{"../x.dtb", "4.19/../x.dtb", "4.19/Devices/X/x.exe", "4.19/root/logo.bmp", "4.19/root/rg351mp-kernel.dtb", "4.19/root/RG351MP-KERNEL.DTB", "4.19/root/../../x.dtb", "4.19/root/x.dtb/"} {
		t.Run(bad, func(t *testing.T) {
			if _, e := ReadDTBPack(packZip(t, p, map[string][]byte{bad: []byte("bad")})); e == nil {
				t.Fatal("accepted")
			}
		})
	}
	p.Files["4.19/root/rg351mp-kernel.dtb"] = []byte("bad")
	if _, e := ReadDTBPack(packZip(t, p, nil)); e == nil {
		t.Fatal("bad hash accepted")
	}
	p = packFixture(t)
	delete(p.Manifest.Lines, "5.10")
	if _, e := ReadDTBPack(packZip(t, p, nil)); e == nil {
		t.Fatal("missing line accepted")
	}
	p = packFixture(t)
	m := p.Manifest
	m.Revision = 2
	b, _ := json.Marshal(m)
	got, e := ReadDTBPackManifest(name, b)
	if e != nil || got.Manifest.Revision != 2 {
		t.Fatal("online revision", e)
	}
}
func TestDTBPackApplyUndoAndGuards(t *testing.T) {
	p := packFixture(t)
	f, size, build := dtbCard(t)
	dir := t.TempDir()
	plan, e := PlanDTBPack(f, size, p, build, dir, DTBOptions{})
	if e != nil || plan.Manual || plan.Added != 1 || !plan.Changed {
		t.Fatal(plan, e)
	}
	out, e := ApplyDTBPack(f, size, p, build, dir, DTBOptions{ScreenAccepted: true})
	if e != nil {
		t.Fatal(e)
	}
	if out.Already || out.Setup.Revision != 1 || len(out.Setup.History) != 1 {
		t.Fatal(out)
	}
	_, tab, _ := CardBuildIdentity(f, size)
	boot, _ := OpenBoot(f, tab, true)
	for _, n := range DTBRootFiles {
		if !bytes.Equal(dtbRead(boot, n, 4<<20), p.Files["4.19/Panels/Panel4/"+n]) {
			t.Fatal("root differs", n)
		}
	}
	if string(dtbRead(boot, "Panels/Panel4/keep.txt", 100)) != "keep" || string(dtbRead(boot, "Panels/Panel4/pictures.txt", 100)) != "640x480\n" {
		t.Fatal("non-DTB changed")
	}
	snapshot := make([]byte, 32<<20)
	f.ReadAt(snapshot, 16<<20)
	again, e := ApplyDTBPack(f, size, p, build, dir, DTBOptions{ScreenAccepted: true})
	if e != nil || !again.Already {
		t.Fatal(again, e)
	}
	after := make([]byte, len(snapshot))
	f.ReadAt(after, 16<<20)
	if !bytes.Equal(snapshot, after) {
		t.Fatal("idempotence changed bytes")
	}
	_, e = ApplyDTBPack(f, size, p, build, dir, DTBOptions{Profile: "Panels/Panel3", ScreenAccepted: true})
	if e != nil {
		t.Fatal("switch", e)
	}
	previous, e := RestoreDTBSetup(f, size, build, dir, DTBOptions{ScreenAccepted: true})
	if e != nil || previous.Setup.Profile != "Panels/Panel4" {
		t.Fatal("undo", previous, e)
	}
	boot, _ = dtbWritable(f, size)
	writeBootFile(boot, DTBRootFiles[0], dtbconv.EmbeddedBases().EE)
	observed, e := ReadCardSetup(boot)
	if e != nil || !observed.Custom {
		t.Fatal("custom root not reflected in card setup", observed.Custom, e)
	}
	if _, e = ApplyDTBPack(f, size, p, build, dir, DTBOptions{ScreenAccepted: true}); !errors.Is(e, ErrDTBManual) {
		t.Fatal("manual not detected", e)
	}
	if _, e = ApplyDTBPack(f, size, p, build, dir, DTBOptions{ReplaceAnyway: true, ScreenAccepted: true}); e != nil {
		t.Fatal("manual confirm", e)
	}
	if _, e = ApplyDTBPack(f, size, p, "changed", dir); e == nil {
		t.Fatal("identity accepted")
	}
	p.Manifest.CompatibleBuilds = []string{"wrong"}
	if _, e = PlanDTBPack(f, size, p, build, dir, DTBOptions{}); e == nil {
		t.Fatal("wrong build accepted")
	}
}
func TestDTBPackMissingProfileAndSpace(t *testing.T) {
	p := packFixture(t)
	f, size, build := dtbCard(t)
	_, tab, _ := CardBuildIdentity(f, size)
	boot, _ := dtbWritable(f, size)
	writeBootFile(boot, "andr36oid-profile.txt", []byte("Panels/Missing\n"))
	if _, e := ApplyDTBPack(f, size, p, build, t.TempDir(), DTBOptions{ScreenAccepted: true}); e == nil {
		t.Fatal("missing selected profile accepted")
	}
	writeBootFile(boot, "andr36oid-profile.txt", []byte("Panels/Panel4\n"))
	fat, _ := tab.Find("BOOT")
	zero := []byte{0xff, 0xff, 0xff, 0x0f}
	header := make([]byte, 512)
	f.ReadAt(header, fat.Offset())
	reserved := int64(header[14]) + int64(header[15])*256
	// Force preflight free count to zero in a duplicate, without changing files.
	fatSize := int64(binary.LittleEndian.Uint32(header[36:]))
	clusters := (int64(binary.LittleEndian.Uint32(header[32:])) - reserved - int64(header[16])*fatSize) / int64(header[13])
	for n := int64(2); n < clusters+2; n++ {
		v := make([]byte, 4)
		at := fat.Offset() + reserved*512 + n*4
		f.ReadAt(v, at)
		if binary.LittleEndian.Uint32(v)&0xfffffff == 0 {
			for copy := int64(0); copy < int64(header[16]); copy++ {
				f.WriteAt(zero, at+copy*fatSize*512)
			}
		}
	}

	if _, e := ApplyDTBPack(f, size, p, build, t.TempDir(), DTBOptions{ScreenAccepted: true}); e == nil || !strings.Contains(e.Error(), "too little free space") {
		t.Fatal("full card was not refused for space", e)
	}
}

type dtbFaultDisk struct {
	*os.File
	fail, calls  int
	offset, size int64
}

func (d *dtbFaultDisk) WriteAt(b []byte, off int64) (int, error) {
	if off < d.offset || off+int64(len(b)) > d.offset+d.size {
		return 0, errors.New("outside BOOT")
	}
	d.calls++
	if d.calls == d.fail {
		return 0, io.ErrShortWrite
	}
	return d.File.WriteAt(b, off)
}
func TestDTBPackEveryReturnedWriteFailureRollsBack(t *testing.T) {
	oldNow := setupNow
	setupNow = func() time.Time { return time.Date(2026, 11, 13, 12, 0, 0, 123456789, time.UTC) }
	defer func() { setupNow = oldNow }()
	p := packFixture(t)
	f, size, build := dtbCard(t)
	original := make([]byte, 32<<20)
	f.ReadAt(original, 16<<20)
	d := &dtbFaultDisk{File: f, offset: 16 << 20, size: 32 << 20}
	if _, e := ApplyDTBPack(d, size, p, build, t.TempDir(), DTBOptions{ScreenAccepted: true}); e != nil {
		t.Fatal(e)
	}
	count := d.calls
	for fail := 1; fail <= count; fail++ {
		f.WriteAt(original, 16<<20)
		d.calls = 0
		d.fail = fail
		if _, e := ApplyDTBPack(d, size, p, build, t.TempDir(), DTBOptions{ScreenAccepted: true}); e == nil {
			t.Fatal("fault ignored", fail)
		}
		after := make([]byte, len(original))
		f.ReadAt(after, 16<<20)
		if !bytes.Equal(after, original) {
			t.Fatal("rollback differs", fail)
		}
	}
	t.Logf("all %d changed-sector writes faulted and rolled back", count)
}
func TestDTBPackRealCandidate(t *testing.T) {
	name := os.Getenv("A36_DTB_PACK")
	if name == "" {
		t.Skip("candidate pack external")
	}
	p, e := ReadDTBPack(name)
	if e != nil {
		t.Fatal(e)
	}
	for _, kernel := range []string{"4.19", "5.10"} {
		if len(p.Manifest.Lines[kernel].PictureSets) != 56 {
			t.Fatal("candidate profile coverage")
		}
	}
}

func TestDTBHistoryKeepsOnlyFiveAndUndoPrunes(t *testing.T) {
	p := packFixture(t)
	f, size, build := dtbCard(t)
	dir := t.TempDir()
	for n := 1; n <= 7; n++ {
		p.Manifest.Revision = n
		if _, e := ApplyDTBPack(f, size, p, build, dir, DTBOptions{ScreenAccepted: true}); e != nil {
			t.Fatal(n, e)
		}
	}
	_, tab, _ := CardBuildIdentity(f, size)
	boot, _ := OpenBoot(f, tab, true)
	setup, e := ReadCardSetup(boot)
	if e != nil || len(setup.History) != 5 {
		t.Fatal(setup.History, e)
	}
	entries, e := boot.ReadDir(SetupHistoryDir)
	if e != nil || len(entries) != 5 {
		t.Fatal("history folders", len(entries), e)
	}
	if _, e = RestoreDTBSetup(f, size, build, dir, DTBOptions{ScreenAccepted: true}); e != nil {
		t.Fatal(e)
	}
	boot, _ = OpenBoot(f, tab, true)
	entries, e = boot.ReadDir(SetupHistoryDir)
	if e != nil || len(entries) != 4 {
		t.Fatal("undo left orphan history", len(entries), e)
	}
}

func TestDTBScreenChangeRequiresExplicitAckAndSkipsIdentical(t *testing.T) {
	p := packFixture(t)
	f, size, build := dtbCard(t)
	plan, e := PlanDTBPack(f, size, p, build, t.TempDir(), DTBOptions{})
	if e != nil || !plan.ScreenChanged {
		t.Fatal(plan, e)
	}
	before := make([]byte, 32<<20)
	f.ReadAt(before, 16<<20)
	if _, e = ApplyDTBPack(f, size, p, build, t.TempDir()); !errors.Is(e, ErrDTBScreenConfirmation) {
		t.Fatal("missing warning acknowledgment accepted", e)
	}
	after := make([]byte, len(before))
	f.ReadAt(after, 16<<20)
	if !bytes.Equal(before, after) {
		t.Fatal("unacknowledged screen change wrote card")
	}
	// Only another profile is added; every file used by this console is identical.
	for kernel, line := range p.Manifest.Lines {
		for _, n := range DTBRootFiles {
			key := "Panels/Panel4/" + n
			b := dtbconv.EmbeddedBases().Genuine
			p.Files[kernel+"/"+key] = b
			h := sha256.Sum256(b)
			line.Files[key] = DTBFile{Size: int64(len(b)), SHA256: hex.EncodeToString(h[:])}
		}
		p.Manifest.Lines[kernel] = line
	}
	plan, e = PlanDTBPack(f, size, p, build, t.TempDir(), DTBOptions{})
	if e != nil || plan.ScreenChanged {
		t.Fatal("other-folder-only update wants warning", plan, e)
	}
	if _, e = ApplyDTBPack(f, size, p, build, t.TempDir()); e != nil {
		t.Fatal(e)
	}
	plan, e = PlanDTBPack(f, size, p, build, t.TempDir(), DTBOptions{Profile: "Panels/Panel3"})
	if e != nil || plan.ScreenChanged {
		t.Fatal("identical-screen profile switch wants warning", plan, e)
	}
	if _, e = ApplyDTBPack(f, size, p, build, t.TempDir(), DTBOptions{Profile: "Panels/Panel3"}); e != nil {
		t.Fatal("identical switch", e)
	}
}
func TestDTBUndoScreenChangeRequiresAck(t *testing.T) {
	p := packFixture(t)
	f, size, build := dtbCard(t)
	if _, e := ApplyDTBPack(f, size, p, build, t.TempDir(), DTBOptions{ScreenAccepted: true}); e != nil {
		t.Fatal(e)
	}
	plan, e := PlanRestoreDTB(f, size, build, t.TempDir())
	if e != nil || !plan.ScreenChanged {
		t.Fatal(plan, e)
	}
	if _, e = RestoreDTBSetup(f, size, build, t.TempDir()); !errors.Is(e, ErrDTBScreenConfirmation) {
		t.Fatal("undo bypassed warning", e)
	}
	if _, e = RestoreDTBSetup(f, size, build, t.TempDir(), DTBOptions{ScreenAccepted: true}); e != nil {
		t.Fatal(e)
	}
}

type dtbFlushFault struct {
	*os.File
	flush, fail            int
	readAfterPayload, used bool
}

func (d *dtbFlushFault) Sync() error {
	d.flush++
	if d.flush == d.fail {
		return io.ErrUnexpectedEOF
	}
	return d.File.Sync()
}
func (d *dtbFlushFault) ReadAt(b []byte, off int64) (int, error) {
	if d.readAfterPayload && d.flush == 2 && !d.used {
		d.used = true
		return 0, io.ErrUnexpectedEOF
	}
	return d.File.ReadAt(b, off)
}
func TestDTBFlushAndReadbackFailuresRestoreOriginal(t *testing.T) {
	for fault := 1; fault <= 4; fault++ {
		f, size, build := dtbCard(t)
		before := make([]byte, 32<<20)
		f.ReadAt(before, 16<<20)
		d := &dtbFlushFault{File: f, fail: fault}
		if fault == 4 {
			d.fail = 0
			d.readAfterPayload = true
		}
		if _, e := ApplyDTBPack(d, size, packFixture(t), build, t.TempDir(), DTBOptions{ScreenAccepted: true}); e == nil {
			t.Fatal("fault ignored", fault)
		}
		after := make([]byte, len(before))
		f.ReadAt(after, 16<<20)
		if !bytes.Equal(before, after) {
			t.Fatal("rollback bytes differ", fault)
		}
	}
}

func TestDTBPackArchiveOversizeRejected(t *testing.T) {
	name := packZip(t, packFixture(t), map[string][]byte{"4.19/Panels/Panel4/huge.dtb": make([]byte, (4<<20)+1)})
	if _, e := ReadDTBPack(name); e == nil {
		t.Fatal("oversize entry accepted")
	}
	f, e := os.CreateTemp(t.TempDir(), "oversize.zip")
	if e != nil {
		t.Fatal(e)
	}
	f.Truncate((256 << 20) + 1)
	f.Close()
	if _, e = ReadDTBPack(f.Name()); e == nil {
		t.Fatal("oversize archive accepted")
	}
}
func TestDTBRecordedKernelBuildMismatch(t *testing.T) {
	f, size, build := dtbCard(t)
	boot, _ := dtbWritable(f, size)
	writeBootFile(boot, BuildInfoV2File, []byte("format=2\nro.andr36oid.version=v2026-10-18-release\nro.product.build.version.incremental=eng.kenny.20261018.0100\nkernel_line=4.19\nkernel_build=32\n"))
	if _, e := PlanDTBPack(f, size, packFixture(t), build, t.TempDir(), DTBOptions{}); e == nil || !strings.Contains(e.Error(), "kernel build") {
		t.Fatal("mismatched recorded kernel accepted", e)
	}
}

func TestDTBMissingPictureSetRefusesBeforeWrite(t *testing.T) {
	p := packFixture(t)
	line := p.Manifest.Lines["4.19"]
	line.PictureSets["Panels/Panel3"] = "999x999"
	p.Manifest.Lines["4.19"] = line
	f, size, build := dtbCard(t)
	before := make([]byte, 32<<20)
	f.ReadAt(before, 16<<20)
	if _, e := ApplyDTBPack(f, size, p, build, t.TempDir(), DTBOptions{ScreenAccepted: true}); e == nil || !strings.Contains(e.Error(), "matching pictures") {
		t.Fatal(e)
	}
	after := make([]byte, len(before))
	f.ReadAt(after, 16<<20)
	if !bytes.Equal(before, after) {
		t.Fatal("missing art set changed card")
	}
}

func TestDTBPreservesPicturePointerAndUserTemporaryFile(t *testing.T) {
	p := packFixture(t)
	f, size, build := dtbCard(t)
	boot, _ := dtbWritable(f, size)
	pointer := []byte("640x480  \n")
	sentinel := "Panels/Panel4/rk3326-r36s-android.dtb.a36-new"
	writeBootFile(boot, "Panels/Panel4/pictures.txt", pointer)
	writeBootFile(boot, sentinel, []byte("user's file"))
	if _, e := ApplyDTBPack(f, size, p, build, t.TempDir(), DTBOptions{ScreenAccepted: true}); e != nil {
		t.Fatal(e)
	}
	_, tab, _ := CardBuildIdentity(f, size)
	boot, _ = OpenBoot(f, tab, true)
	if !bytes.Equal(dtbRead(boot, "Panels/Panel4/pictures.txt", 256), pointer) {
		t.Fatal("existing pictures.txt changed")
	}
	if string(dtbRead(boot, sentinel, 256)) != "user's file" {
		t.Fatal("user temporary file removed")
	}
}
func TestDTBLegacyWithoutV2Uses419(t *testing.T) {
	f, size, _ := dtbCard(t)
	boot, _ := dtbWritable(f, size)
	writeBootFile(boot, BuildInfoFile, []byte("format=1\nro.andr36oid.version=v2026-10-18-release\nro.product.build.version.incremental=eng.kenny.20261018.0100\nkernel_line=5.10\n"))
	setup, e := ReadCardSetup(boot)
	if e != nil || setup.KernelLine != "4.19" {
		t.Fatal("legacy line policy", setup.KernelLine, e)
	}
}
