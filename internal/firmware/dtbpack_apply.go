package firmware

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/diskfs/go-diskfs/filesystem"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

type DTBOptions struct {
	Progress       func(int64, int64) `json:"-"`
	ScreenAccepted bool               `json:"screen_accepted,omitempty"`
	Profile        string             `json:"profile,omitempty"`
	ReplaceAnyway  bool               `json:"replace_anyway,omitempty"`
}
type DTBPlan struct {
	ScreenChanged bool      `json:"screen_changed"`
	Setup         CardSetup `json:"setup"`
	Profile       string    `json:"profile"`
	Profiles      []Profile `json:"profiles"`
	Changed       bool      `json:"changed"`
	Manual        bool      `json:"manual"`
	Updated       int       `json:"updated"`
	Added         int       `json:"added"`
	Needed        int64     `json:"needed"`
	Free          int64     `json:"free"`
	Already       bool      `json:"already"`
	files         map[string][]byte
}
type DTBResult struct {
	ScreenChanged bool      `json:"screen_changed"`
	Already       bool      `json:"already"`
	Setup         CardSetup `json:"setup"`
}

var ErrDTBManual = errors.New("The console files on this card were changed by hand")
var ErrDTBRollback = errors.New("device tree write and rollback failed; keep the card connected for recovery")

func fatFree(file *os.File) (int64, int64, error) {
	header := make([]byte, 512)
	if _, e := file.ReadAt(header, 0); e != nil {
		return 0, 0, e
	}
	bps := int64(binary.LittleEndian.Uint16(header[11:]))
	spc := int64(header[13])
	reserved := int64(binary.LittleEndian.Uint16(header[14:]))
	count := int64(header[16])
	fat := int64(binary.LittleEndian.Uint32(header[36:]))
	total := int64(binary.LittleEndian.Uint32(header[32:]))
	if bps != 512 || spc < 1 || spc&(spc-1) != 0 || reserved < 1 || count < 1 || count > 2 || fat < 1 || fat > 1<<20 || total <= reserved+count*fat {
		return 0, 0, errors.New("invalid FAT32 geometry")
	}
	clusters := (total - reserved - count*fat) / spc
	if (clusters+2)*4 > fat*bps {
		return 0, 0, errors.New("invalid FAT32 table size")
	}
	table := make([]byte, (clusters+2)*4)
	if _, e := file.ReadAt(table, reserved*bps); e != nil {
		return 0, 0, e
	}
	free := int64(0)
	for n := int64(2); n < clusters+2; n++ {
		if binary.LittleEndian.Uint32(table[n*4:])&0x0fffffff == 0 {
			free++
		}
	}
	return free * spc * bps, spc * bps, nil
}
func dtbRead(boot filesystem.FileSystem, n string, limit int64) []byte {
	b, _ := ReadFile(boot, "/"+n, limit)
	return b
}
func rootDTBs(boot filesystem.FileSystem) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, n := range DTBRootFiles {
		b, e := ReadFile(boot, "/"+n, 4<<20)
		if e != nil || !validDTB(b) {
			return nil, fmt.Errorf("missing or invalid current root device tree: %s", n)
		}
		out[n] = b
	}
	return out, nil
}
func planDTBs(stage *setupStage, p *DTBPack, opts DTBOptions) (*DTBPlan, error) {
	if p == nil {
		return nil, errors.New(DTBPackInvalid)
	}
	setup, e := ReadCardSetup(stage.boot)
	if e != nil {
		return nil, e
	}
	if e = p.compatible(setup); e != nil {
		return nil, e
	}
	if setup.Revision > p.Manifest.Revision {
		return nil, errors.New("this card already has a newer device tree set")
	}
	line := p.Manifest.Lines[setup.KernelLine]
	prefix := setup.KernelLine + "/"
	profiles := parseProfileList(p.Files[prefix+ProfileList])
	profile := setup.Profile
	if profile != "" {
		if _, ok := profiles[profile]; !ok {
			return nil, fmt.Errorf("This set has no files for %s, the console chosen on this card. Nothing was changed.", profile)
		}
	}
	if opts.Profile != "" {
		profile = opts.Profile
	}
	if profile != "" && !dtbProfileID.MatchString(profile) {
		return nil, errors.New("invalid console profile")
	}
	if profile != "" {
		if _, ok := profiles[profile]; !ok {
			return nil, fmt.Errorf("This set has no files for %s, the console chosen on this card. Nothing was changed.", profile)
		}
	}
	root, e := rootDTBs(stage.boot)
	if e != nil {
		return nil, e
	}
	plan := &DTBPlan{Setup: setup, Profile: profile, files: map[string][]byte{}}
	if setup.Profile != "" {
		for _, n := range DTBRootFiles {
			if !bytes.Equal(root[n], dtbRead(stage.boot, setup.Profile+"/"+n, 4<<20)) {
				plan.Manual = true
			}
		}
	}
	ids := make([]string, 0, len(profiles))
	for id := range profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		info := profiles[id]
		plan.Profiles = append(plan.Profiles, Profile{ID: id, Name: info.Name, Console: info.Console, Experimental: info.Experimental, Hidden: info.Hidden, AlsoFor: info.AlsoFor})
		changed := false
		added := false
		if _, e := stage.boot.ReadDir(id); e != nil {
			added = true
		}
		for _, n := range DTBRootFiles {
			data := p.Files[prefix+id+"/"+n]
			if !bytes.Equal(data, dtbRead(stage.boot, id+"/"+n, 4<<20)) {
				changed = true
			}
		}
		if id != profile {
			if added {
				plan.Added++
			} else if changed {
				plan.Updated++
			}
		}
	}
	for name, desc := range line.Files {
		data, ok := p.Files[prefix+name]
		sum := sha256.Sum256(data)
		if !ok || desc.Size != int64(len(data)) || hex.EncodeToString(sum[:]) != strings.ToLower(desc.SHA256) {
			return nil, errors.New(DTBPackInvalid)
		}
		if strings.HasPrefix(name, "root/") {
			continue
		}
		plan.files[name] = data
	}
	for _, n := range DTBRootFiles {
		from := "root/" + n
		if profile != "" {
			from = profile + "/" + n
		}
		b := p.Files[prefix+from]
		if !validDTB(b) {
			return nil, errors.New(DTBPackInvalid)
		}
		plan.files[n] = b
		if !bytes.Equal(root[n], b) {
			plan.Changed = true
		}
	}
	// No pictures in the pack. A new pointer is usable only if the matching set
	// already exists on this compatible card; never borrow another panel's art.
	effectivePictures := map[string]string{}
	for id, set := range line.PictureSets {
		existing, e := ReadFile(stage.boot, "/"+id+"/pictures.txt", 256)
		preserve := e == nil
		if preserve {
			value := strings.TrimSpace(string(existing))
			if !pictureSet.MatchString(value) {
				return nil, fmt.Errorf("invalid existing picture set for %s", id)
			}
			if strings.SplitN(value, "-", 2)[0] != strings.SplitN(set, "-", 2)[0] {
				return nil, fmt.Errorf("picture dimensions for %s changed; use a complete release image", id)
			}
			set = value
		}
		for _, n := range PictureFiles {
			if _, e := ReadFile(stage.boot, "/Pictures/"+set+"/"+n, 16<<20); e != nil {
				return nil, fmt.Errorf("matching pictures for %s are not on this card; write the complete image instead", id)
			}
		}
		effectivePictures[id] = set
		if !preserve {
			plan.files[id+"/pictures.txt"] = []byte(set + "\n")
		}
	}

	if profile != setup.Profile {
		if profile == "" {
			return nil, errors.New("choose a console profile")
		}
		set := effectivePictures[profile]
		for _, n := range PictureFiles {
			b, e := ReadFile(stage.boot, "/Pictures/"+set+"/"+n, 16<<20)
			if e != nil {
				return nil, fmt.Errorf("matching pictures for %s are not on this card; write the complete image instead", profile)
			}
			plan.files[n] = b
		}
		plan.files["andr36oid-profile.txt"] = []byte(profile + "\n")
	}
	for n, b := range plan.files {
		if screenChangeInFiles(plan.files, n) && !bytes.Equal(b, dtbRead(stage.boot, n, int64(len(b)))) {
			plan.ScreenChanged = true
			plan.Changed = true
		}
	}
	allSame := profile == setup.Profile
	for n, b := range plan.files {
		if !bytes.Equal(b, dtbRead(stage.boot, n, int64(len(b)))) {
			allSame = false
		}
	}
	plan.Already = allSame && setup.Revision == p.Manifest.Revision
	var cluster int64
	plan.Free, cluster, e = fatFree(stage.file)
	if e != nil {
		return nil, e
	}
	round := func(size int64) int64 { return (size + cluster - 1) / cluster * cluster }
	// Conservative preflight: all changed files can coexist with their old versions,
	// one full root/picture/catalogue history plus filesystem/JSON directory margin.
	plan.Needed = 256 << 10
	for n, b := range plan.files {
		old := dtbRead(stage.boot, n, int64(len(b)))
		if !bytes.Equal(old, b) {
			plan.Needed += round(int64(len(b)))
		}
	}
	for _, n := range append(append([]string{}, DTBRootFiles...), append(PictureFiles, ProfileList)...) {
		plan.Needed += round(int64(len(dtbRead(stage.boot, n, 16<<20))))
	}
	if plan.Already {
		plan.Needed = 0
	}
	return plan, nil
}
func PlanDTBPack(disk HotfixDisk, size int64, p *DTBPack, expected, dir string, opts DTBOptions) (*DTBPlan, error) {
	s, e := stageSetup(disk, size, expected, dir)
	if e != nil {
		return nil, e
	}
	defer s.Close()
	return planDTBs(s, p, opts)
}

func stageDTBFile(stage *setupStage, n string, b []byte) error {
	if path.Dir(n) != "." {
		if e := stage.boot.Mkdir(path.Dir(n)); e != nil {
			return e
		}
	}
	temp := n + ".a36-new"
	for index := 0; ; index++ {
		if _, e := stage.boot.Stat(temp); e != nil {
			break
		}
		if index >= 32 {
			return errors.New("no free temporary filename in profile folder")
		}
		temp = fmt.Sprintf("%s.a36-new-%d", n, index)
	}
	if e := writeBootFile(stage.boot, temp, b); e != nil {
		return e
	}
	if _, e := stage.boot.Stat(n); e == nil {
		if e = stage.boot.Remove(n); e != nil {
			return e
		}
	}
	if e := stage.boot.Rename(temp, n); e != nil {
		return e
	}
	return verifyBootFile(stage.boot, n, b)
}
func removeSetupTree(boot filesystem.FileSystem, dir string) error {
	if !strings.HasPrefix(dir, SetupHistoryDir+"/") || len(strings.Split(dir, "/")) > 5 {
		return errors.New("invalid history path")
	}
	entries, e := boot.ReadDir(dir)
	if e != nil {
		return e
	}
	for _, ent := range entries {
		n := path.Join(dir, ent.Name())
		if ent.IsDir() {
			e = removeSetupTree(boot, n)
		} else {
			e = boot.Remove(n)
		}
		if e != nil {
			return e
		}
	}
	return boot.Remove(dir)
}
func saveDTBHistory(stage *setupStage, setup *CardSetup, targets map[string][]byte) error {
	id := fmt.Sprintf("%020d", setupNow().UTC().UnixNano())
	directory := SetupHistoryDir + "/" + id
	if !historyID.MatchString(id) {
		return errors.New("computer clock cannot produce a valid history timestamp")
	}
	if _, e := stage.boot.ReadDir(directory); e == nil {
		return errors.New("history timestamp already exists; try again")
	}
	for _, n := range append(append(append([]string{}, DTBRootFiles...), PictureFiles...), ProfileList) {
		b, e := ReadFile(stage.boot, "/"+n, 16<<20)
		if e != nil {
			return e
		}
		if e = stageDTBFile(stage, directory+"/"+n, b); e != nil {
			return e
		}
	}
	// Remember only newly introduced DTBs, so undo can remove them without
	// deleting folders or the user's non-DTB files when restoring the old catalog.
	introduced := map[string]string{}
	for n, b := range targets {
		if !strings.HasSuffix(n, ".dtb") || !dtbProfileID.MatchString(path.Dir(n)) {
			continue
		}
		if _, e := ReadFile(stage.boot, "/"+n, 4<<20); e != nil {
			sum := sha256.Sum256(b)
			introduced[n] = hex.EncodeToString(sum[:])
		}
	}
	receipt, _ := json.Marshal(introduced)
	if e := stageDTBFile(stage, directory+"/introduced.json", receipt); e != nil {
		return e
	}
	b, e := json.Marshal(setup)
	if e != nil {
		return e
	}
	if e = stageDTBFile(stage, directory+"/setup.json", b); e != nil {
		return e
	}
	setup.History = append(setup.History, id)
	for len(setup.History) > 5 {
		old := setup.History[0]
		if !historyID.MatchString(old) {
			return errors.New("invalid history entry")
		}
		if e = removeSetupTree(stage.boot, SetupHistoryDir+"/"+old); e != nil {
			return e
		}
		setup.History = setup.History[1:]
	}
	return nil
}

var historyID = regexp.MustCompile(`^[0-9]{20}$`)

func dtbTransaction(stage *setupStage, work func() error) (err error) {
	original, e := os.CreateTemp(path.Dir(stage.file.Name()), "dtbs-original-*.fat")
	if e != nil {
		return e
	}
	defer func() { original.Close(); os.Remove(original.Name()) }()
	if _, e = io.Copy(original, io.NewSectionReader(stage.file, 0, stage.size)); e != nil {
		return e
	}
	if e = original.Sync(); e != nil {
		return e
	}
	err = work()
	if err == nil {
		return nil
	}
	if restore := commitBoot(stage.disk, original, stage.offset, stage.size, nil); restore != nil {
		return fmt.Errorf("%w: write=%v; restore=%v", ErrDTBRollback, err, restore)
	}
	oldBytes, newBytes := make([]byte, 1<<20), make([]byte, 1<<20)
	for off := int64(0); off < stage.size; off += int64(len(oldBytes)) {
		n := min(int64(len(oldBytes)), stage.size-off)
		if _, e := original.ReadAt(oldBytes[:n], off); e != nil {
			return fmt.Errorf("%w: %v", ErrDTBRollback, e)
		}
		if _, e := stage.disk.ReadAt(newBytes[:n], stage.offset+off); e != nil {
			return fmt.Errorf("%w: %v", ErrDTBRollback, e)
		}
		if !bytes.Equal(oldBytes[:n], newBytes[:n]) {
			return fmt.Errorf("%w: original BOOT readback differs", ErrDTBRollback)
		}
	}
	return err
}
func ApplyDTBPack(disk HotfixDisk, size int64, p *DTBPack, expected, dir string, options ...DTBOptions) (*DTBResult, error) {
	opts := DTBOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	stage, e := stageSetup(disk, size, expected, dir)
	if e != nil {
		return nil, e
	}
	defer stage.Close()
	plan, e := planDTBs(stage, p, opts)
	if e != nil {
		return nil, e
	}
	if plan.Manual && !opts.ReplaceAnyway {
		return nil, ErrDTBManual
	}
	if plan.ScreenChanged && !opts.ScreenAccepted {
		return nil, ErrDTBScreenConfirmation
	}
	if plan.Already {
		return &DTBResult{Already: true, Setup: plan.Setup}, nil
	}
	if plan.Free < plan.Needed {
		return nil, fmt.Errorf("The first partition of the card has too little free space (%s needed, %s free). Nothing was changed.", fmt.Sprintf("%d bytes", plan.Needed), fmt.Sprintf("%d bytes", plan.Free))
	}
	advance := func(n int64) {
		if opts.Progress != nil {
			opts.Progress(n, 3)
		}
	}
	advance(0)
	setup := plan.Setup
	setup.Custom = plan.Manual
	e = dtbTransaction(stage, func() error {
		if e := saveDTBHistory(stage, &setup, plan.files); e != nil {
			return e
		}
		// Make the undo set durable before changing any files used by the console.
		if e := stage.Save(setup); e != nil {
			return e
		}
		if e := stage.Commit(); e != nil {
			return e
		}
		durable, e := OpenBoot(stage.disk, stage.tab, true)
		if e != nil {
			return e
		}
		history := SetupHistoryDir + "/" + setup.History[len(setup.History)-1]
		entries, e := stage.boot.ReadDir(history)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			n := history + "/" + entry.Name()
			b, e := ReadFile(stage.boot, "/"+n, 16<<20)
			if e != nil {
				return e
			}
			if e = verifyBootFile(durable, n, b); e != nil {
				return e
			}
		}
		advance(1)
		names := make([]string, 0, len(plan.files))
		for n := range plan.files {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if bytes.Equal(plan.files[n], dtbRead(stage.boot, n, int64(len(plan.files[n])))) {
				continue
			}
			if e := stageDTBFile(stage, n, plan.files[n]); e != nil {
				return e
			}
		}
		if e := commitBoot(stage.disk, stage.file, stage.offset, stage.size, nil); e != nil {
			return e
		}
		check, e := OpenBoot(stage.disk, stage.tab, true)
		if e != nil {
			return e
		}
		for n, b := range plan.files {
			if e = verifyBootFile(check, n, b); e != nil {
				return e
			}
		}
		advance(2)
		setup.Profile = plan.Profile
		setup.Revision = p.Manifest.Revision
		setup.KernelBuild = p.Manifest.Lines[setup.KernelLine].KernelBuild
		setup.Custom = false
		if p.Source != nil {
			setup.Source = p.Source
		}
		marker, _ := json.Marshal(map[string]any{"format": 1, "version": p.Manifest.Version, "revision": p.Manifest.Revision, "line": setup.KernelLine, "time": setupNow().UTC().Format(time.RFC3339)})
		if e = stageDTBFile(stage, "dtbs-applied.txt", append(marker, '\n')); e != nil {
			return e
		}
		if e = stage.Save(setup); e != nil {
			return e
		}
		if e = stage.Commit(); e != nil {
			return e
		}
		final, e := OpenBoot(stage.disk, stage.tab, true)
		if e != nil {
			return e
		}
		if e = verifyBootFile(final, "dtbs-applied.txt", append(marker, '\n')); e != nil {
			return e
		}
		for n, b := range plan.files {
			if e = verifyBootFile(final, n, b); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	advance(3)
	return &DTBResult{Setup: setup, ScreenChanged: plan.ScreenChanged}, nil
}
func RestoreDTBSetup(disk HotfixDisk, size int64, expected, dir string, options ...DTBOptions) (*DTBResult, error) {
	plan, e := PlanRestoreDTB(disk, size, expected, dir)
	if e != nil {
		return nil, e
	}
	opts := DTBOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	if plan.ScreenChanged && !opts.ScreenAccepted {
		return nil, ErrDTBScreenConfirmation
	}

	stage, e := stageSetup(disk, size, expected, dir)
	if e != nil {
		return nil, e
	}
	defer stage.Close()
	setup, e := ReadCardSetup(stage.boot)
	if e != nil {
		return nil, e
	}
	if len(setup.History) == 0 {
		return nil, errors.New("no previous console files on this card")
	}
	id := setup.History[len(setup.History)-1]
	if !historyID.MatchString(id) {
		return nil, errors.New("invalid history entry")
	}
	directory := SetupHistoryDir + "/" + id
	b, e := ReadFile(stage.boot, "/"+directory+"/setup.json", 1<<20)
	if e != nil {
		return nil, e
	}
	var previous CardSetup
	if e = json.Unmarshal(b, &previous); e != nil || previous.Build != setup.Build {
		return nil, errors.New("previous files belong to another build")
	}
	files := map[string][]byte{}
	for _, n := range append(append(append([]string{}, DTBRootFiles...), PictureFiles...), ProfileList) {
		b, e := ReadFile(stage.boot, "/"+directory+"/"+n, 16<<20)
		if e != nil {
			return nil, e
		}
		if strings.HasSuffix(n, ".dtb") && !validDTB(b) {
			return nil, errors.New("damaged previous device tree")
		}
		files[n] = b
	}
	introduced := map[string]string{}
	receipt, e := ReadFile(stage.boot, "/"+directory+"/introduced.json", 1<<20)
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(receipt, &introduced); e != nil {
		return nil, e
	}
	for n, want := range introduced {
		if !dtbPath(n) || !dtbProfileID.MatchString(path.Dir(n)) {
			return nil, errors.New("invalid previous file list")
		}
		b, e := ReadFile(stage.boot, "/"+n, 4<<20)
		if e != nil {
			return nil, e
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != want {
			return nil, errors.New("a new profile was edited after the update; keep its files before undoing")
		}
	}
	// Keep bootloader choices independent of the console profile undo operation.
	previous.Bootloader = setup.Bootloader
	previous.BootloaderHistory = setup.BootloaderHistory
	previous.History = setup.History[:len(setup.History)-1]
	e = dtbTransaction(stage, func() error {
		for n := range introduced {
			if e := stage.boot.Remove(n); e != nil {
				return e
			}
		}
		for n, b := range files {
			if e := stageDTBFile(stage, n, b); e != nil {
				return e
			}
		}
		if previous.Profile != "" && !previous.Custom {
			// A normal undo also restores the selected profile's canonical copies, so
			// the next inspection does not mislabel our own undo as a manual edit.
			for _, n := range DTBRootFiles {
				if e := stageDTBFile(stage, previous.Profile+"/"+n, files[n]); e != nil {
					return e
				}
			}
		}
		if previous.Profile != "" {
			if e := stageDTBFile(stage, "andr36oid-profile.txt", []byte(previous.Profile+"\n")); e != nil {
				return e
			}
		} else {
			_ = stage.boot.Remove("andr36oid-profile.txt")
		}
		_ = stage.boot.Remove("dtbs-applied.txt")
		if e := removeSetupTree(stage.boot, directory); e != nil {
			return e
		}
		if e := stage.Save(previous); e != nil {
			return e
		}
		if e := stage.Commit(); e != nil {
			return e
		}
		check, e := OpenBoot(stage.disk, stage.tab, true)
		if e != nil {
			return e
		}
		for n, b := range files {
			if e = verifyBootFile(check, n, b); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return &DTBResult{Setup: previous, ScreenChanged: plan.ScreenChanged}, nil
}
