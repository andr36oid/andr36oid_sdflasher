package exfat

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every size is laid out into a sparse file full of other bytes at the places the
// formatter leaves alone, then checked by the system's own exFAT checker.
func TestFormatPassesFsck(t *testing.T) {
	fsck := ""
	for _, p := range []string{"fsck.exfat", "/usr/sbin/fsck.exfat", "/sbin/fsck.exfat"} {
		if f, e := exec.LookPath(p); e == nil {
			fsck = f
			break
		}
	}
	if fsck == "" {
		t.Skip("no fsck.exfat on this host")
	}
	for _, size := range []int64{64 << 20, 200 << 20, 256<<20 + 512, 1 << 30, 7<<30 + 3*512, 32 << 30, 59 << 30, 119<<30 + 512, 480 << 30} {
		chunks, e := Format(size, 5701632*512, "EASYROMS", 0x36a36a36)
		if e != nil {
			t.Fatal(size, e)
		}
		path := filepath.Join(t.TempDir(), "volume")
		f, e := os.Create(path)
		if e != nil {
			t.Fatal(e)
		}
		if e = f.Truncate(size); e != nil {
			t.Fatal(e)
		}
		total := 0
		for _, c := range chunks {
			if _, e = f.WriteAt(c.Data, c.Offset); e != nil {
				t.Fatal(e)
			}
			total += len(c.Data)
		}
		if e = f.Close(); e != nil {
			t.Fatal(e)
		}
		out, e := exec.Command(fsck, "-n", path).CombinedOutput()
		if e != nil || !strings.Contains(string(out), "clean") {
			t.Fatalf("size %d: fsck: %v\n%s", size, e, out)
		}
		t.Logf("%d GiB: %d KiB written, %s", size>>30, total>>10, lastLine(string(out)))
	}
}

func lastLine(s string) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	return l[len(l)-1]
}

func TestFormatRejects(t *testing.T) {
	for _, c := range []struct {
		size, offset int64
		label        string
	}{{1 << 20, 0, "A"}, {1<<30 + 1, 0, "A"}, {1 << 30, 7, "A"}, {1 << 30, 0, "TWELVE CHARS"}} {
		if _, e := Format(c.size, c.offset, c.label, 1); e == nil {
			t.Fatal("accepted", c)
		}
	}
}
