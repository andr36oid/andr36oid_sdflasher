package firmware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBootChainRealContainers(t *testing.T) {
	dir := os.Getenv("A36_BOOT_CHAIN_FIXTURES")
	if dir == "" {
		t.Skip("set A36_BOOT_CHAIN_FIXTURES to report fixtures")
	}
	for _, v := range []struct {
		p     string
		check func([]byte) error
	}{{"vendor/trust.img", CheckTrust}, {"mainline/idbloader.img", CheckIDB}, {"mainline/u-boot.itb", CheckBootFIT}} {
		b, e := os.ReadFile(filepath.Join(dir, v.p))
		if e != nil {
			t.Fatal(e)
		}
		if e = v.check(b); e != nil {
			t.Fatal(v.p, e)
		}
		for _, size := range []int{0, 1, 39, 511, 1023} {
			if e = v.check(b[:size]); e == nil {
				t.Fatalf("accepted truncated %s %d", v.p, size)
			}
		}
		b[0] ^= 1
		if v.check(b) == nil {
			t.Fatal("bad signature accepted", v.p)
		}
	}
}
func TestBootCompatibilityPairs(t *testing.T) {
	t0 := BootTarget{Storage: "sd", KernelLine: "5.10", Console: "R36 Ultra", ImageAvailable: true}
	rules := BootRules()
	rules = append(rules, BootRule{Kind: "sleep", ID: "trial", Label: "Trial", WorksWith: BootWorksWith{Chains: []string{"standard"}, Storage: []string{"sd"}}, TestedOn: []string{"R36 Ultra"}, TestedBootloaders: []string{"standard"}, Broken: []BootBrokenPair{{Field: "bootloader", Value: "new", Reason: "known test pair"}}})
	for _, loader := range []string{"image", "standard", "new", "file"} {
		for _, sleep := range []string{"image", "trial"} {
			for _, chain := range []string{"standard", "mainline"} {
				for _, storage := range []string{"sd", "emmc"} {
					name := loader + "/" + sleep + "/" + chain + "/" + storage
					t.Run(name, func(t *testing.T) {
						target := t0
						target.Storage = storage
						c := BootloaderChoice{Choice: loader, Sleep: sleep, Chain: chain}
						issues, e := CheckBootOptions(c, target, rules)
						impossible := storage != "sd" || chain == "mainline" && (loader != "image" || sleep != "image")
						broken := !impossible && loader == "new" && sleep == "trial"
						if (e != nil) != (impossible || broken) {
							t.Fatalf("issues %+v err %v", issues, e)
						}
						c.Force = true
						_, e = CheckBootOptions(c, target, rules)
						if (e != nil) != impossible {
							t.Fatalf("force incorrectly bypassed: %v", e)
						}
					})
				}
			}
		}
	}
	c, notes := ResetBootOptions(BootloaderChoice{Choice: "new", Sleep: "trial", Chain: "mainline"}, "chain", t0, rules)
	if c.LoaderID() != "image" || c.SleepID() != "image" || c.ChainID() != "mainline" || len(notes) != 2 {
		t.Fatal(c, notes)
	}
	t0.Console = "unknown"
	issues, e := CheckBootOptions(BootloaderChoice{Choice: "standard"}, t0, rules)
	if e != nil || len(issues) != 1 || issues[0].Level != "untested" {
		t.Fatal(issues, e)
	}
}
func TestBootCatalog(t *testing.T) {
	c := BootCatalog{Format: 1, Entries: []BootFirmwareEntry{{BootRule: BootRule{Kind: "sleep", ID: "vendor", Label: "Vendor sleep firmware", WorksWith: BootWorksWith{Chains: []string{"standard"}, Storage: []string{"sd"}}, TestedOn: []string{"R36 Ultra"}, TestedBootloaders: []string{"standard"}}, Repository: StandardBootloaderRepo, Asset: "trust.img", SHA256Source: "SHA256SUMS"}}}
	b, _ := json.Marshal(c)
	if _, e := ParseBootCatalog(b); e != nil {
		t.Fatal(e)
	}
	c.Entries[0].Repository = "other/repo"
	b, _ = json.Marshal(c)
	if _, e := ParseBootCatalog(b); e == nil {
		t.Fatal("accepted unknown repo")
	}
}
func TestBootDefaultsPreserveLegacy(t *testing.T) {
	b := testBootloader()
	p := filepath.Join(t.TempDir(), "image")
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	f.Truncate(BootloaderOffset + BootloaderSize)
	f.WriteAt(b, BootloaderOffset)
	f.Close()
	im := &Image{Path: p, Size: BootloaderOffset + BootloaderSize}
	got, out, e := ResolveBootOptions(context.Background(), BootloaderChoice{Choice: "image"}, im, ImageBootTarget(im, ""), t.TempDir())
	if e != nil || !bytes.Equal(got, b) || out.plan != nil || out.Options != nil {
		t.Fatal(out, e)
	}
	h := sha256.Sum256(b)
	if out.Record.SHA256 != hex.EncodeToString(h[:]) {
		t.Fatal("legacy hash differs")
	}
}

func bootChainFixtureCard(t *testing.T) (*os.File, *Image) {
	t.Helper()
	dir := os.Getenv("A36_BOOT_CHAIN_FIXTURES")
	raw := os.Getenv("A36_BOOT_CHAIN_IMAGE")
	if dir == "" || raw == "" {
		t.Skip("release fixtures + immutable image required")
	}
	f, size := loaderCard(t, nil)
	tab, e := layout.Read(f, size)
	if e != nil {
		t.Fatal(e)
	}
	source, e := os.Open(raw)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	st, e := source.Stat()
	if e != nil {
		t.Fatal(e)
	}
	src, e := layout.Read(source, st.Size())
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"idbloader", "uboot", "trust"} {
		p, ok := src.Find(name)
		if !ok {
			t.Fatal(name)
		}
		p.Number = len(tab.Parts) + 1
		tab.Parts = append(tab.Parts, p)
		b := make([]byte, p.Size())
		if _, e = source.ReadAt(b, p.Offset()); e != nil {
			t.Fatal(e)
		}
		if _, e = f.WriteAt(b, p.Offset()); e != nil {
			t.Fatal(e)
		}
	}
	h, tail, e := tab.Encode()
	if e != nil {
		t.Fatal(e)
	}
	f.WriteAt(h, 0)
	f.WriteAt(tail, size-int64(len(tail)))
	build, _, e := CardBuildIdentity(f, size)
	if e != nil {
		t.Fatal(e)
	}
	return f, &Image{Path: f.Name(), Size: size, Table: tab, BuildIdentity: build, Facts: map[string]string{"kernel_line": "5.10"}}
}
func bootFixtureNetwork(t *testing.T) func() {
	t.Helper()
	dir := os.Getenv("A36_BOOT_CHAIN_FIXTURES")
	old := hotfixHTTP
	hotfixHTTP = &http.Client{Transport: loaderTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("authenticated fixture request")
		}
		var b []byte
		var e error
		if r.URL.String() == BootOptionsCatalogURL {
			b = []byte(`{"format":1,"entries":[{"kind":"sleep","id":"vendor","label_key":"Vendor sleep firmware","repo":"andr36oid/u-boot","asset":"trust.img","sha256_source":"SHA256SUMS","works_with":{"chains":["standard"],"storage":["sd"]},"tested_on":["R36 Ultra"],"tested_bootloaders":["standard"]}]}`)
		} else {
			folder, repo, tag := "vendor", StandardBootloaderRepo, "20261113-1535"
			if strings.Contains(r.URL.Path, "uboot-mainline") {
				folder = "mainline"
				repo = NewBootloaderRepo
				tag = "20261113-1313"
			}
			if r.URL.Path == "/repos/"+repo+"/releases/latest" {
				var assets []map[string]any
				files, e := os.ReadDir(filepath.Join(dir, folder))
				if e != nil {
					t.Fatal(e)
				}
				for _, f := range files {
					st, _ := f.Info()
					assets = append(assets, map[string]any{"name": f.Name(), "size": st.Size(), "browser_download_url": "https://github.com/" + repo + "/releases/download/" + tag + "/" + f.Name()})
				}
				b, _ = json.Marshal(map[string]any{"tag_name": tag, "assets": assets})
			} else if strings.HasPrefix(r.URL.Path, "/"+repo+"/releases/download/"+tag+"/") {
				b, e = os.ReadFile(filepath.Join(dir, folder, filepath.Base(r.URL.Path)))
			} else {
				return nil, fmt.Errorf("unexpected request %s", r.URL)
			}
		}
		if e != nil {
			return nil, e
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}, nil
	})}
	return func() { hotfixHTTP = old }
}
func TestBootChainRealApplyCanariesAndRestore(t *testing.T) {
	restore := bootFixtureNetwork(t)
	defer restore()
	for _, c := range []BootloaderChoice{{Choice: "image"}, {Choice: "standard", Chain: "standard"}, {Choice: "new", Chain: "standard"}, {Choice: "image", Sleep: "vendor"}, {Choice: "standard", Sleep: "vendor"}, {Choice: "new", Sleep: "vendor"}, {Choice: "image", Chain: "mainline"}} {
		t.Run(c.LoaderID()+"/"+c.SleepID()+"/"+c.ChainID(), func(t *testing.T) {
			f, im := bootChainFixtureCard(t)
			before, e := os.ReadFile(f.Name())
			if e != nil {
				t.Fatal(e)
			}
			target := ImageBootTarget(im, "")
			target.Console = "R36 Ultra"
			b, o, e := ResolveBootOptions(context.Background(), c, im, target, t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			if e = ApplyResolvedBootOptions(f, im.Size, b, o, im.BuildIdentity, t.TempDir()); e != nil {
				t.Fatal(e)
			}
			after, e := os.ReadFile(f.Name())
			if e != nil {
				t.Fatal(e)
			}
			slots, _ := bootSlots(im.Table)
			boot, _ := im.Table.Find("BOOT")
			for offset := int64(0); offset < im.Size; offset += 512 {
				allowed := offset >= boot.Offset() && offset < boot.Offset()+boot.Size()
				for _, s := range slots {
					if offset >= s.Offset && offset < s.Offset+s.Size {
						allowed = true
					}
				}
				if !allowed && !bytes.Equal(before[offset:offset+512], after[offset:offset+512]) {
					t.Fatal("canary changed", offset)
				}
			}
			// No BOOT file other than the explicitly required history record changes.
			fs, e := OpenBoot(f, im.Table, true)
			if e != nil {
				t.Fatal(e)
			}
			keep, e := ReadFile(fs, "/keep.txt", 100)
			if e != nil || string(keep) != "preserve this" {
				t.Fatal("unrelated BOOT file", e)
			}
			if c.ChainID() == "mainline" {
				source := filepath.Join(t.TempDir(), "original.img")
				if e = os.WriteFile(source, before, 0600); e != nil {
					t.Fatal(e)
				}
				original := *im
				original.Path = source
				target, e = CardBootTarget(f, im.Size, &original)
				if e != nil {
					t.Fatal(e)
				}
				if _, _, e = ResolveBootOptions(context.Background(), BootloaderChoice{Chain: "standard"}, nil, BootTarget{Storage: "sd", CurrentChain: "mainline"}, t.TempDir()); e == nil {
					t.Fatal("restore without source image accepted")
				}
				b, o, e = ResolveBootOptions(context.Background(), BootloaderChoice{Choice: "image", Chain: "standard"}, &original, target, t.TempDir())
				if e != nil {
					t.Fatal(e)
				}
				if e = ApplyResolvedBootOptions(f, im.Size, b, o, im.BuildIdentity, t.TempDir()); e != nil {
					t.Fatal(e)
				}
				restored, _ := os.ReadFile(f.Name())
				for _, s := range slots {
					if !bytes.Equal(before[s.Offset:s.Offset+s.Size], restored[s.Offset:s.Offset+s.Size]) {
						t.Fatal("restored slot differs", s.Name)
					}
				}
			}
		})
	}
}

func TestBootDefaultsByteIdenticalApply(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1794570000") // diskfs FAT directory timestamps, as well as setupNow below
	f, im := bootChainFixtureCard(t)
	before, e := os.ReadFile(f.Name())
	if e != nil {
		t.Fatal(e)
	}
	other, e := os.CreateTemp(t.TempDir(), "old-path")
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	other.Write(before)
	old := setupNow
	setupNow = func() time.Time { return time.Unix(1794570000, 0) }
	defer func() { setupNow = old }()
	oldData, oldOut, e := ResolveBootloader(context.Background(), BootloaderChoice{Choice: "image"}, im, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	newData, newOut, e := ResolveBootOptions(context.Background(), BootloaderChoice{Choice: "image"}, im, ImageBootTarget(im, ""), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = ApplyBootloader(other, im.Size, oldData, *oldOut.Record, im.BuildIdentity, t.TempDir()); e != nil {
		t.Fatal(e)
	}
	if e = ApplyResolvedBootOptions(f, im.Size, newData, newOut, im.BuildIdentity, t.TempDir()); e != nil {
		t.Fatal(e)
	}
	a, _ := os.ReadFile(other.Name())
	b, _ := os.ReadFile(f.Name())
	if !bytes.Equal(a, b) {
		t.Fatal("default changed card bytes")
	}
}
func TestBootTableRestrictionsAndReset(t *testing.T) {
	rule := BootRule{Kind: "sleep", ID: "limited", WorksWith: BootWorksWith{Chains: []string{"standard"}, Bootloaders: []string{"standard"}, KernelLines: []string{"5.10"}, Profiles: []string{"Experimental/R36-Ultra"}, Storage: []string{"sd"}}, TestedOn: []string{"R36 Ultra"}, TestedBootloaders: []string{"standard"}}
	rules := append(BootRules(), rule)
	c := BootloaderChoice{Choice: "standard", Sleep: "limited"}
	base := BootTarget{KernelLine: "5.10", Profile: "Experimental/R36-Ultra", Console: "R36 Ultra", Storage: "sd", ImageAvailable: true}
	if issues, e := CheckBootOptions(c, base, rules); e != nil || len(issues) != 0 {
		t.Fatal(issues, e)
	}
	for _, field := range []string{"kernel_line", "profile", "storage", "bootloader"} {
		t.Run(field, func(t *testing.T) {
			target, choice := base, c
			switch field {
			case "kernel_line":
				target.KernelLine = "4.19"
			case "profile":
				target.Profile = "other"
			case "storage":
				target.Storage = "emmc"
			case "bootloader":
				choice.Choice = "new"
			}
			if _, e := CheckBootOptions(choice, target, rules); e == nil {
				t.Fatal("restriction ignored")
			}
			choice, notes := ResetBootOptions(choice, "target", target, rules)
			if choice.SleepID() != "image" || len(notes) == 0 {
				t.Fatal("invalid sleep not reset", choice, notes)
			}
		})
	}
}

func TestBootInvalidPlanWritesNothing(t *testing.T) {
	restore := bootFixtureNetwork(t)
	defer restore()
	f, im := bootChainFixtureCard(t)
	target := ImageBootTarget(im, "")
	target.Console = "R36 Ultra"
	data, out, e := ResolveBootOptions(context.Background(), BootloaderChoice{Chain: "mainline"}, im, target, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(f.Name())
	if e != nil {
		t.Fatal(e)
	}
	// The second asset is invalid: even the first valid slot must not be written.
	out.plan.Data[1][0] ^= 1
	if e = ApplyResolvedBootOptions(f, im.Size, data, out, im.BuildIdentity, t.TempDir()); e == nil {
		t.Fatal("invalid later asset accepted")
	}
	after, e := os.ReadFile(f.Name())
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("invalid set wrote card", e)
	}
}

func TestBootSlotFenceAndHistorylessMainline(t *testing.T) {
	f, im := bootChainFixtureCard(t)
	bad := im.Table
	bad.Parts = append([]layout.Partition{}, im.Table.Parts...)
	for i, p := range bad.Parts {
		if p.Name == "trust" {
			bad.Parts[i].Start = 112000
			bad.Parts[i].End = 120191
		}
	}
	if _, e := bootSlots(bad); e == nil {
		t.Fatal("trust outside pre-BOOT area accepted")
	}
	fit, e := os.ReadFile(filepath.Join(os.Getenv("A36_BOOT_CHAIN_FIXTURES"), "mainline/u-boot.itb"))
	if e != nil {
		t.Fatal(e)
	}
	b := make([]byte, BootloaderSize)
	copy(b, fit)
	f.WriteAt(b, BootloaderOffset)
	target, e := CardBootTarget(f, im.Size, nil)
	if e != nil || target.CurrentChain != "mainline" {
		t.Fatal("actual FIT ignored without history", target, e)
	}
	if _, e = CheckBootOptions(BootloaderChoice{Chain: "standard"}, target, BootRules()); e == nil {
		t.Fatal("historyless restore without image accepted")
	}
}

func TestRetainedCustomSleepCannotBypassPairChecks(t *testing.T) {
	target := BootTarget{Storage: "sd", Console: "R36 Ultra", CurrentChain: "standard", CurrentSleep: "custom"}
	c := BootloaderChoice{Choice: "new", Force: true}
	issues, e := CheckBootOptions(c, target, BootRules())
	if e == nil || len(issues) == 0 || issues[0].Row != "sleep" {
		t.Fatal("unknown retained sleep accepted as image default", issues, e)
	}
	// Mainline carries its own firmware; the unused trust slot is irrelevant.
	if _, e = CheckBootOptions(BootloaderChoice{Chain: "mainline"}, target, BootRules()); e != nil {
		t.Fatal(e)
	}
}
