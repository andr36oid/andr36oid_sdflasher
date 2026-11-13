package firmware

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	BootloaderOffset      int64 = 16384 * 512
	BootloaderSize              = 4 << 20
	bootloaderCopySize          = 1 << 20
	bootloaderHeaderSize        = 2048
	BootloaderCheckFailed       = "This bootloader build failed its check and was not written. The card keeps the image's bootloader."
	BootloaderOnlySD            = "Only for SD cards for now."
	BootloaderNeedImage         = "Choose the image file to put its bootloader back."
)

// crc32_rk uses polynomial 0x04c10db7, seed zero and no final XOR.
var rockchipCRC = func() [256]uint32 {
	var table [256]uint32
	for n := range table {
		v := uint32(n) << 24
		for bit := 0; bit < 8; bit++ {
			if v&0x80000000 != 0 {
				v = v<<1 ^ 0x04c10db7
			} else {
				v <<= 1
			}
		}
		table[n] = v
	}
	return table
}()

func loaderCRC(payload []byte) uint32 {
	var crc uint32
	for _, b := range payload {
		crc = rockchipCRC[byte(crc>>24)^b] ^ (crc << 8)
	}
	return crc
}

// CheckBootloaderContainer follows Rockchip loaderimage.c: the SHA includes the
// aligned payload, optional version/reserved words, address, size and hash length.
func checkLoaderCopy(block []byte) error {
	if len(block) != bootloaderCopySize || !bytes.Equal(block[:8], []byte("LOADER  ")) {
		return errors.New("no LOADER header")
	}
	size, hashLen := binary.LittleEndian.Uint32(block[20:24]), binary.LittleEndian.Uint32(block[28:32])
	if binary.LittleEndian.Uint32(block[16:20]) != 0x00200000 || size == 0 || size%4 != 0 || size > bootloaderCopySize-bootloaderHeaderSize {
		return errors.New("invalid load address or payload size")
	}
	payload := block[bootloaderHeaderSize : bootloaderHeaderSize+int(size)]
	if loaderCRC(payload) != binary.LittleEndian.Uint32(block[24:28]) {
		return errors.New("payload CRC differs")
	}
	var digest []byte
	switch hashLen {
	case sha1.Size:
		h := sha1.New()
		h.Write(payload)
		if binary.LittleEndian.Uint32(block[8:12]) > 0 {
			h.Write(block[8:16])
		}
		h.Write(block[16:24])
		h.Write(block[28:32])
		digest = h.Sum(nil)
	case sha256.Size:
		h := sha256.New()
		h.Write(payload)
		if binary.LittleEndian.Uint32(block[8:12]) > 0 {
			h.Write(block[8:16])
		}
		h.Write(block[16:24])
		h.Write(block[28:32])
		digest = h.Sum(nil)
	default:
		return errors.New("no supported payload hash")
	}
	if !bytes.Equal(digest, block[32:32+int(hashLen)]) {
		return errors.New("payload SHA differs")
	}
	return nil
}
func CheckBootloaderContainer(data []byte) error {
	if len(data) != BootloaderSize {
		return errors.New("bootloader must be exactly 4194304 bytes")
	}
	for copy := 0; copy < 4; copy++ {
		block := data[copy*bootloaderCopySize : (copy+1)*bootloaderCopySize]
		if err := checkLoaderCopy(block); err != nil {
			return fmt.Errorf("U-Boot copy %d: %w", copy+1, err)
		}
		if copy > 0 && !bytes.Equal(block, data[:bootloaderCopySize]) {
			return errors.New("the four U-Boot copies differ")
		}
	}
	return nil
}

func ReadBootloaderFile(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, BootloaderSize+1))
	if err != nil {
		return nil, err
	}
	if err = CheckBootloaderContainer(data); err != nil {
		return nil, err
	}
	return data, nil
}
func ImageBootloader(image *Image) ([]byte, error) {
	if image == nil || image.Path == "" || image.Size < BootloaderOffset+BootloaderSize {
		return nil, errors.New(BootloaderNeedImage)
	}
	f, err := os.Open(image.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data := make([]byte, BootloaderSize)
	if _, err = io.ReadFull(io.NewSectionReader(f, BootloaderOffset, BootloaderSize), data); err != nil {
		return nil, err
	}
	if err = CheckBootloaderContainer(data); err != nil {
		return nil, err
	}
	return data, nil
}

// WriteBootloaderSlot changes only the four-MiB slot. Reverse copy order keeps
// complete copies until each replacement has been synced and read back.
// A media write error still requires checking/re-writing the card on a computer.
func WriteBootloaderSlot(disk HotfixDisk, data []byte, progress func(int64, int64)) error {
	if err := CheckBootloaderContainer(data); err != nil {
		return err
	}
	old := make([]byte, BootloaderSize)
	if _, err := disk.ReadAt(old, BootloaderOffset); err != nil {
		return err
	}
	var bad, good []int
	for copy := 3; copy >= 0; copy-- {
		if checkLoaderCopy(old[copy*bootloaderCopySize:(copy+1)*bootloaderCopySize]) == nil {
			good = append(good, copy)
		} else {
			bad = append(bad, copy)
		}
	}
	order := append(bad, good...)
	check := make([]byte, bootloaderCopySize)
	for done, copy := range order {
		chunk := data[copy*bootloaderCopySize : (copy+1)*bootloaderCopySize]
		off := BootloaderOffset + int64(copy*bootloaderCopySize)
		n, err := disk.WriteAt(chunk, off)
		if err != nil {
			return err
		}
		if n != len(chunk) {
			return io.ErrShortWrite
		}
		if err = disk.Sync(); err != nil {
			return err
		}
		if _, err = disk.ReadAt(check, off); err != nil {
			return err
		}
		if !bytes.Equal(check, chunk) {
			return errors.New("written U-Boot copy did not verify")
		}
		if progress != nil {
			progress(int64((done+1)*bootloaderCopySize), BootloaderSize)
		}
	}
	return nil
}
