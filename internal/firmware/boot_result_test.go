package firmware

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"os"
	"testing"
)

func TestBootOptionsOnCardNothingToDo(t *testing.T) {
	f, size := loaderCard(t, nil)
	data := testBootloader()
	same, e := BootOptionsOnCard(f, size, data, &BootloaderOutcome{})
	if e != nil || !same {
		t.Fatal("same U-Boot not recognised", same, e)
	}
	other := append([]byte{}, data...)
	other[BootloaderSize-1] ^= 1
	if same, e = BootOptionsOnCard(f, size, other, &BootloaderOutcome{}); e != nil || same {
		t.Fatal("different U-Boot taken as the same", same, e)
	}
	if same, _ = BootOptionsOnCard(f, size, data, nil); same {
		t.Fatal("no outcome taken as the same")
	}
}

type failingSlotDisk struct {
	*os.File
	left int
}

func (d *failingSlotDisk) WriteAt(b []byte, off int64) (int, error) {
	if off >= BootloaderOffset && off < BootloaderOffset+BootloaderSize {
		if d.left == 0 {
			return 0, errors.New("simulated reader error")
		}
		d.left--
	}
	return d.File.WriteAt(b, off)
}

func TestBootApplyStoppedPartWayIsMarked(t *testing.T) {
	f, size := loaderCard(t, nil)
	build, _, e := CardBuildIdentity(f, size)
	if e != nil {
		t.Fatal(e)
	}
	data := otherBootloader()
	e = ApplyBootloader(&failingSlotDisk{File: f, left: 2}, size, data, loaderRecord(data, "new"), build, t.TempDir())
	if BootErrorKind(e) != "uboot-stopped" {
		t.Fatal("part-way stop not marked", e)
	}
	// The marker survives the trip through the helper as plain text.
	if BootErrorKind(errors.New(e.Error())) != "uboot-stopped" {
		t.Fatal("marker lost as text")
	}
	if CheckBootloaderContainer(readSlot(t, f)) != nil {
		ok := false
		b := readSlot(t, f)
		for c := 0; c < 4; c++ {
			if checkLoaderCopy(b[c*bootloaderCopySize:(c+1)*bootloaderCopySize]) == nil {
				ok = true
			}
		}
		if !ok {
			t.Fatal("no complete U-Boot copy left")
		}
	}
	if BootErrorKind(bootRefused(errors.New("x"))) != "refused" || BootErrorKind(bootStageStopped(errors.New("x"))) != "stage-stopped" || BootErrorKind(errors.New("other")) != "" || BootErrorKind(nil) != "" {
		t.Fatal("error kinds")
	}
}

func readSlot(t *testing.T, f *os.File) []byte {
	t.Helper()
	b := make([]byte, BootloaderSize)
	if _, e := f.ReadAt(b, BootloaderOffset); e != nil {
		t.Fatal(e)
	}
	return b
}

// otherBootloader is a valid container that differs from testBootloader.
func otherBootloader() []byte {
	b := make([]byte, bootloaderCopySize)
	copy(b, []byte("LOADER  "))
	payload := []byte("a newer fixture payload, 32 b!!!")
	binary.LittleEndian.PutUint32(b[16:], 0x200000)
	binary.LittleEndian.PutUint32(b[20:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(b[24:], loaderCRC(payload))
	binary.LittleEndian.PutUint32(b[28:], 32)
	copy(b[bootloaderHeaderSize:], payload)
	h := sha256.New()
	h.Write(payload)
	h.Write(b[16:24])
	h.Write(b[28:32])
	copy(b[32:], h.Sum(nil))
	return bytes.Repeat(b, 4)
}
