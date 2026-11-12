package flash

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
)

func TestCompressedUserdataKernelGuard(t *testing.T) {
	p := &Plan{Mode: "update", Table: layout.Table{Parts: []layout.Partition{{Name: "userdata", Start: 2, End: 40}}}}
	for _, base := range []int{1024, 5120} {
		b := make([]byte, 24000)
		binary.LittleEndian.PutUint32(b[1024+base:], 0xf2f52010)
		binary.LittleEndian.PutUint32(b[1024+base+2180:], 0x2008)
		for _, value := range []string{"", "none", "zstd"} {
			if checkCompressedUserdata(bytes.NewReader(b), p, value) == nil {
				t.Fatal("unsafe target accepted", base, value)
			}
		}
		if err := checkCompressedUserdata(bytes.NewReader(b), p, "lz4"); err != nil {
			t.Fatal(err)
		}
	}
	if err := checkCompressedUserdata(bytes.NewReader(make([]byte, 24000)), p, ""); err != nil {
		t.Fatal(err)
	}
	p.Mode = "install"
	if err := checkCompressedUserdata(bytes.NewReader(nil), p, ""); err != nil {
		t.Fatal(err)
	}
}
