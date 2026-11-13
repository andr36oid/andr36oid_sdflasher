package firmware

import (
	"bytes"
	"testing"
)

func TestReadFileFATTailBound(t *testing.T) {
	for _, n := range []int{0, 10, 511, 512, 513, 816, 8193} {
		data := bytes.Repeat([]byte{'x'}, n)
		f, tab := bootOnlyImage(t, map[string]string{"tail.bin": string(data)})
		boot, e := OpenBoot(f, tab, true)
		if e != nil {
			t.Fatal(e)
		}
		got, e := ReadFile(boot, "/tail.bin", int64(n))
		if e != nil || !bytes.Equal(got, data) {
			t.Fatalf("size%d got%d: %v", n, len(got), e)
		}
		if n > 0 {
			if _, e = ReadFile(boot, "/tail.bin", int64(n-1)); e == nil {
				t.Fatal("over-limit file accepted", n)
			}
		}
	}
}
