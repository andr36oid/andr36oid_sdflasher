package firmware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type BootloaderChoice struct {
	Choice string           `json:"choice"`
	File   string           `json:"file,omitempty"`
	Build  *BootloaderBuild `json:"build,omitempty"`
}
type BootloaderOutcome struct {
	Record  *BootloaderRecord `json:"record,omitempty"`
	Warning string            `json:"warning,omitempty"`
}

// ResolveBootloader completes optional downloads/checks before a flash starts.
// A failed optional choice falls back to the verified image, never raw chain files.
func ResolveBootloader(ctx context.Context, choice BootloaderChoice, image *Image, dir string) ([]byte, *BootloaderOutcome, error) {
	if e := ctx.Err(); e != nil {
		return nil, nil, e
	}
	if choice.Choice == "" {
		choice.Choice = "image"
	}
	out := &BootloaderOutcome{}
	var data []byte
	var e error
	record := &BootloaderRecord{Choice: choice.Choice}
	switch choice.Choice {
	case "image":
		if image == nil {
			return nil, out, errors.New(BootloaderNeedImage)
		}
		data, e = ImageBootloader(image)
	case "file":
		data, e = ReadBootloaderFile(choice.File)
	case "standard", "new":
		build := choice.Build
		if build == nil {
			build, e = FetchBootloaderBuild(ctx, choice.Choice)
		}
		if e == nil && (build == nil || build.Choice != choice.Choice) {
			e = errors.New("bootloader offer belongs to another choice")
		}
		if e == nil {
			data, e = DownloadBootloader(ctx, build, dir)
		}
		if e == nil {
			record.Repository = build.Repository
			record.Tag = build.Tag
			record.Version = build.Version
		}
	default:
		return nil, nil, errors.New("unknown bootloader choice")
	}
	if e != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if choice.Choice == "image" {
			return nil, nil, e
		}
		out.Warning = BootloaderCheckFailed
		// Network failures have their own plain-language hint; malformed data stays a check failure.
		if errors.Is(e, errBootloaderFetch) {
			out.Warning = BootloaderFetchFailed
		}
		if image == nil {
			return nil, out, nil
		}
		data, e = ImageBootloader(image)
		if e != nil {
			return nil, nil, e
		}
		record = &BootloaderRecord{Choice: "image"}
	}
	sum := sha256.Sum256(data)
	record.SHA256 = hex.EncodeToString(sum[:])
	record.Time = time.Now().UTC().Format(time.RFC3339Nano)
	out.Record = record
	return data, out, nil
}

// ApplyBootloader rechecks the card and stages its history before changing the
// slot. Filesystem space/check errors therefore happen before any slot write.
func ApplyBootloader(disk HotfixDisk, size int64, data []byte, record BootloaderRecord, expected, dir string) error {
	if e := CheckBootloaderContainer(data); e != nil {
		return e
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(record.SHA256, hex.EncodeToString(sum[:])) {
		return errors.New("bootloader record hash differs")
	}
	stage, e := stageSetup(disk, size, expected, dir)
	if e != nil {
		return e
	}
	defer stage.Close()
	entries, e := stage.boot.ReadDir(".")
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), "andr36oid-datacard.txt") {
			return errors.New(BootloaderOnlySD)
		}
	}
	bootPart, _ := stage.tab.Find("BOOT")
	if bootPart.Offset() < BootloaderOffset+BootloaderSize || size < BootloaderOffset+BootloaderSize {
		return errors.New("card layout has no standard U-Boot slot")
	}
	for _, part := range stage.tab.Parts {
		if part.Offset() < BootloaderOffset+BootloaderSize && part.Offset()+part.Size() > BootloaderOffset {
			if !strings.EqualFold(part.Name, "uboot") || part.Offset() != BootloaderOffset || part.Size() != BootloaderSize {
				return errors.New("another partition overlaps the U-Boot slot")
			}
		}
	}
	setup, e := ReadCardSetup(stage.boot)
	if e != nil {
		return e
	}
	if setup.Bootloader != nil {
		setup.BootloaderHistory = append(setup.BootloaderHistory, *setup.Bootloader)
	}
	if len(setup.BootloaderHistory) > 5 {
		setup.BootloaderHistory = setup.BootloaderHistory[len(setup.BootloaderHistory)-5:]
	}
	record.Time = time.Now().UTC().Format(time.RFC3339Nano)
	setup.Bootloader = &record
	if e = stage.Save(setup); e != nil {
		return fmt.Errorf("prepare card history: %w", e)
	}
	if e = WriteBootloaderSlot(disk, data, nil); e != nil {
		return e
	}
	if e = stage.Commit(); e != nil {
		return fmt.Errorf("save card history: %w", e)
	}
	return nil
}
