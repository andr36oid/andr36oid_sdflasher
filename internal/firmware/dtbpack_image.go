package firmware

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/diskfs/go-diskfs/filesystem"
	"os"
	"sort"
	"strings"
)

func ImageDTBSetup(im *Image) (CardSetup, error) {
	f, e := os.Open(im.Path)
	if e != nil {
		return CardSetup{}, e
	}
	defer f.Close()
	boot, e := OpenBoot(f, im.Table, true)
	if e != nil {
		return CardSetup{}, e
	}
	setup, e := ReadCardSetup(boot)
	if e != nil {
		return setup, e
	}
	setup.Source = sourceFromImage(im.Source)
	return setup, nil
}
func sourceFromImage(raw string) *SetupSource {
	for _, repo := range []string{DTBBetaRepository, DTBReleaseRepository} {
		prefix := "https://github.com/" + repo + "/releases/download/"
		if strings.HasPrefix(raw, prefix) {
			parts := strings.Split(strings.TrimPrefix(raw, prefix), "/")
			if len(parts) == 2 && parts[0] != "" {
				return &SetupSource{Repository: repo, Tag: parts[0]}
			}
		}
	}
	return nil
}
func writeImageDTBPack(boot filesystem.FileSystem, p *DTBPack, line string) error {
	l, ok := p.Manifest.Lines[line]
	if !ok {
		return errors.New(DTBPackInvalid)
	}
	for id, set := range l.PictureSets {
		for _, n := range PictureFiles {
			if _, e := ReadFile(boot, "/Pictures/"+set+"/"+n, 16<<20); e != nil {
				return errors.New("matching screen pictures are missing; use a complete release image")
			}
		}
		if e := boot.Mkdir(id); e != nil {
			return e
		}
	}
	names := make([]string, 0, len(l.Files))
	for n := range l.Files {
		if !strings.HasPrefix(n, "root/") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		if e := writeBootFile(boot, n, p.Files[line+"/"+n]); e != nil {
			return e
		}
	}
	for id, set := range l.PictureSets {
		if e := writeBootFile(boot, id+"/pictures.txt", []byte(set+"\n")); e != nil {
			return e
		}
	}
	return nil
}

// ImageWithDTBPack changes only the in-memory profile choices. The source image
// and its hash remain untouched; PrepareBoot overlays the verified set in its
// private FAT staging file before selecting the requested profile.
func ImageWithDTBPack(im *Image, p *DTBPack, dir string) (*Image, error) {
	f, e := os.Open(im.Path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	stage, e := stageSetup(f, im.Size, im.BuildIdentity, dir)
	if e != nil {
		return nil, e
	}
	defer stage.Close()
	setup, e := ReadCardSetup(stage.boot)
	if e != nil {
		return nil, e
	}
	if e = p.compatible(setup); e != nil {
		return nil, e
	}
	if e = writeImageDTBPack(stage.boot, p, setup.KernelLine); e != nil {
		return nil, e
	}
	profiles, e := Profiles(stage.boot)
	if e != nil {
		return nil, e
	}
	original, e := OpenBoot(f, im.Table, true)
	if e != nil {
		return nil, e
	}
	for i := range profiles {
		profile := &profiles[i]
		for _, n := range DTBRootFiles {
			old, _ := ReadFile(original, "/"+profile.ID+"/"+n, 4<<20)
			if !bytes.Equal(old, p.Files[setup.KernelLine+"/"+profile.ID+"/"+n]) {
				profile.DTBScreenChanged = true
			}
		}
		oldPictures, e := profilePictures(original, "/"+profile.ID, false)
		if e != nil || len(oldPictures) != len(PictureFiles) {
			profile.DTBScreenChanged = true
		} else {
			for j, n := range PictureFiles {
				old, _ := ReadFile(original, oldPictures[j], 16<<20)
				next, _ := ReadFile(original, "/Pictures/"+p.Manifest.Lines[setup.KernelLine].PictureSets[profile.ID]+"/"+n, 16<<20)
				if !bytes.Equal(old, next) {
					profile.DTBScreenChanged = true
				}
			}
		}
	}
	out := *im
	out.Profiles = profiles
	out.DTBPack = p
	out.DTBVersion = fmt.Sprintf("%s · r%d", p.Manifest.Version, p.Manifest.Revision)
	return &out, nil
}
