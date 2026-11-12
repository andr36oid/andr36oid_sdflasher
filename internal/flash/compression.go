package flash

import (
	"encoding/binary"
	"errors"
	"io"
)

// A kernel without the F2FS backend may mount the volume yet fail compressed
// files. Run on the actual destination again immediately before any writes.
func checkCompressedUserdata(dst io.ReaderAt, p *Plan, capability string) error {
	if p.Mode != "update" {
		return nil
	}
	data, ok := p.Table.Find("userdata")
	if !ok {
		return errors.New("Cannot update: userdata partition is missing.")
	}
	for _, base := range []int64{1024, 5120} {
		b := make([]byte, 2184)
		if _, err := dst.ReadAt(b, data.Offset()+base); err != nil {
			return err
		}
		if binary.LittleEndian.Uint32(b) == 0xf2f52010 && binary.LittleEndian.Uint32(b[2180:])&0x2000 != 0 && capability != "lz4" {
			return errors.New("Cannot keep data: userdata uses F2FS compression and this image has no LZ4-capable kernel. Choose a compression-capable image or back up data and make a fresh installation.")
		}
	}
	return nil
}
