package firmware

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
)

var setupNow = time.Now

const (
	SetupFile       = "andr36oid-setup.json"
	SetupHistoryDir = "andr36oid-setup-history"
)

type SetupSource struct {
	Repository string `json:"repository,omitempty"`
	Tag        string `json:"tag,omitempty"`
}
type BootloaderRecord struct {
	Choice     string `json:"choice"`
	Repository string `json:"repository,omitempty"`
	Tag        string `json:"tag,omitempty"`
	Version    string `json:"version,omitempty"`
	SHA256     string `json:"sha256"`
	Time       string `json:"time"`
}
type CardSetup struct {
	Format            int                `json:"format"`
	Release           string             `json:"release"`
	Build             string             `json:"build"`
	Revision          int                `json:"manifest_revision"`
	KernelLine        string             `json:"kernel_line"`
	KernelBuild       int                `json:"kernel_build,omitempty"`
	Profile           string             `json:"profile"`
	Parts             map[string]string  `json:"parts,omitempty"`
	Custom            bool               `json:"custom"`
	Time              string             `json:"time"`
	Source            *SetupSource       `json:"source,omitempty"`
	History           []string           `json:"history,omitempty"`
	Bootloader        *BootloaderRecord  `json:"bootloader,omitempty"`
	BootloaderHistory []BootloaderRecord `json:"bootloader_history,omitempty"`
}

// ReadCardSetup also works before the first setup record exists. BOOT's identity
// and selected profile remain authoritative; old cards have the 4.19 kernel.
func ReadCardSetup(boot filesystem.FileSystem) (CardSetup, error) {
	var s CardSetup
	entries, err := boot.ReadDir(".")
	if err != nil {
		return s, err
	}
	names := map[string]bool{}
	for _, entry := range entries {
		names[strings.ToLower(entry.Name())] = true
	}
	if names[strings.ToLower(SetupFile)] {
		b, e := ReadFile(boot, "/"+SetupFile, 1<<20)
		if e != nil {
			return s, e
		}
		if e = json.Unmarshal(b, &s); e != nil || s.Format != 1 {
			return s, errors.New("card setup record is not readable")
		}
	}
	build := BuildIdentity(boot)
	if build == "" {
		return s, errors.New("card has no readable andr36oid build information")
	}
	if s.Build != "" && s.Build != build {
		return s, errors.New("card setup record belongs to another build")
	}
	s.Format = 1
	s.Build = build
	s.Release = strings.SplitN(build, "|", 2)[0]
	if b, e := readBuildInfo(boot); e == nil {
		m, _ := ParseBuildInfo(b)
		s.KernelLine = "4.19"
		if names[strings.ToLower(BuildInfoV2File)] {
			s.KernelLine = m["kernel_line"]
		}
		if value := m["kernel_build"]; value != "" {
			number, e := strconv.Atoi(value)
			if e != nil || number < 1 {
				return s, errors.New("invalid recorded kernel build")
			}
			s.KernelBuild = number
		}
	}
	if s.KernelLine == "" {
		s.KernelLine = "4.19"
	}
	if names["andr36oid-profile.txt"] {
		b, e := ReadFile(boot, "/andr36oid-profile.txt", 4096)
		if e != nil {
			return s, e
		}
		s.Profile = strings.TrimSpace(string(b))
		s.Custom = false
		if s.Profile != "" {
			for _, n := range DTBRootFiles {
				root, e1 := ReadFile(boot, "/"+n, 4<<20)
				profile, e2 := ReadFile(boot, "/"+s.Profile+"/"+n, 4<<20)
				if e1 != nil || e2 != nil || !bytes.Equal(root, profile) {
					s.Custom = true
					break
				}
			}
		}
	}
	return s, nil
}

type setupStage struct {
	file         *os.File
	boot         *fat32.FileSystem
	disk         HotfixDisk
	tab          layout.Table
	offset, size int64
	record       []byte
}

func stageSetup(disk HotfixDisk, size int64, expected, dir string) (*setupStage, error) {
	build, tab, e := CardBuildIdentity(disk, size)
	if e != nil {
		return nil, e
	}
	if expected == "" || build != expected {
		return nil, errors.New("the card changed; choose it again")
	}
	p, _ := tab.Find("BOOT")
	f, e := os.CreateTemp(dir, "setup-boot-*.fat")
	if e != nil {
		return nil, e
	}
	stage := &setupStage{file: f, disk: disk, tab: tab, offset: p.Offset(), size: p.Size()}
	ok := false
	defer func() {
		if !ok {
			stage.Close()
		}
	}()
	if _, e = io.Copy(f, io.NewSectionReader(disk, p.Offset(), p.Size())); e != nil {
		return nil, e
	}
	stage.boot, e = fat32.Read(file.New(f, false), p.Size(), 0, 512)
	if e != nil {
		return nil, e
	}
	ok = true
	return stage, nil
}
func (s *setupStage) Close() { s.file.Close(); os.Remove(s.file.Name()) }
func (s *setupStage) Save(record CardSetup) error {
	record.Format = 1
	record.Time = setupNow().UTC().Format(time.RFC3339Nano)
	b, e := json.MarshalIndent(record, "", "  ")
	if e != nil {
		return e
	}
	s.record = append(b, '\n')
	if e = writeBootFile(s.boot, SetupFile, s.record); e != nil {
		return e
	}
	return s.file.Sync()
}
func (s *setupStage) Commit() error {
	if len(s.record) == 0 {
		return errors.New("no card setup record prepared")
	}
	if e := commitBoot(s.disk, s.file, s.offset, s.size, nil); e != nil {
		return e
	}
	check, e := OpenBoot(s.disk, s.tab, true)
	if e != nil {
		return e
	}
	return verifyBootFile(check, SetupFile, s.record)
}
