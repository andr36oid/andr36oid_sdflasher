// Package exfat lays out an empty exFAT filesystem: what a formatter would write,
// as a few byte ranges, so the flasher can make the games partition itself and the
// card is ready for games straight after flashing.
package exfat

import (
	_ "embed"
	"encoding/binary"
	"errors"
	"unicode/utf16"
)

const sector = 512

// Chunk is bytes to write at Offset from the start of the partition.
type Chunk struct {
	Offset int64
	Data   []byte
}

// boundary is where the allocation table and the first cluster start: on whole
// megabytes, the erase-block friendly places SD cards are formatted to.
const boundary = 1 << 20

// MinSize is the smallest partition Format accepts.
const MinSize = 64 << 20

// clusterShift picks the cluster size the way the usual formatters do:
// 4 KiB up to 256 MiB, 32 KiB up to 32 GiB, 128 KiB above.
func clusterShift(size int64) uint {
	switch {
	case size <= 256<<20:
		return 12
	case size <= 32<<30:
		return 15
	}
	return 17
}

// Format returns the ranges that make size bytes an empty exFAT volume named label.
// offset is where the partition starts on the card, in bytes; serial tells volumes
// apart. Everything outside the returned ranges may hold anything.
func Format(size, offset int64, label string, serial uint32) ([]Chunk, error) {
	if size < MinSize || size%sector != 0 || offset < 0 || offset%sector != 0 {
		return nil, errors.New("exfat: bad partition size or position")
	}
	name := utf16.Encode([]rune(label))
	if len(name) > 11 {
		return nil, errors.New("exfat: label longer than 11 characters")
	}
	shift := clusterShift(size)
	cluster := int64(1) << shift
	// The table has to hold one entry per cluster plus two; where the clusters start
	// depends on how long the table is. Size the table for the most clusters there
	// could be, then count the clusters that really fit behind it.
	fatOffset := int64(boundary)
	most := (size - fatOffset) / cluster
	fatLength := roundUp((most+2)*4, sector)
	heapOffset := roundUp(fatOffset+fatLength, boundary)
	count := (size - heapOffset) / cluster
	if count < 16 || count > 0xfffffff5 {
		return nil, errors.New("exfat: partition too small or too large")
	}

	bitmapBytes := (count + 7) / 8
	bitmapClusters := (bitmapBytes + cluster - 1) / cluster
	upcaseClusters := (int64(len(upcase)) + cluster - 1) / cluster
	bitmapFirst := int64(2)
	upcaseFirst := bitmapFirst + bitmapClusters
	rootFirst := upcaseFirst + upcaseClusters
	used := bitmapClusters + upcaseClusters + 1

	// Boot region, written twice: sectors 0-11 and 12-23
	region := make([]byte, 12*sector)
	b := region[:sector]
	copy(b, []byte{0xeb, 0x76, 0x90})
	copy(b[3:], "EXFAT   ")
	le := binary.LittleEndian
	le.PutUint64(b[64:], uint64(offset/sector))
	le.PutUint64(b[72:], uint64(size/sector))
	le.PutUint32(b[80:], uint32(fatOffset/sector))
	le.PutUint32(b[84:], uint32(fatLength/sector))
	le.PutUint32(b[88:], uint32(heapOffset/sector))
	le.PutUint32(b[92:], uint32(count))
	le.PutUint32(b[96:], uint32(rootFirst))
	le.PutUint32(b[100:], serial)
	le.PutUint16(b[104:], 0x0100)
	b[108] = 9
	b[109] = byte(shift - 9)
	b[110] = 1
	b[111] = 0x80
	b[112] = byte(used * 100 / count)
	b[510], b[511] = 0x55, 0xaa
	for s := 1; s <= 8; s++ {
		region[s*sector+510], region[s*sector+511] = 0x55, 0xaa
	}
	var sum uint32
	for i, v := range region[:11*sector] {
		if i == 106 || i == 107 || i == 112 {
			continue
		}
		sum = sum>>1 | sum<<31
		sum += uint32(v)
	}
	for i := 0; i < sector; i += 4 {
		le.PutUint32(region[11*sector+i:], sum)
	}
	boot := append(append([]byte(nil), region...), region...)

	// Allocation table: two reserved entries, then one chain each for the bitmap,
	// the upper-case table and the root folder. Every other entry is free.
	fat := make([]byte, fatLength)
	le.PutUint32(fat[0:], 0xfffffff8)
	le.PutUint32(fat[4:], 0xffffffff)
	chain := func(first, n int64) {
		for i := int64(0); i < n; i++ {
			next := uint32(first + i + 1)
			if i == n-1 {
				next = 0xffffffff
			}
			le.PutUint32(fat[(first+i)*4:], next)
		}
	}
	chain(bitmapFirst, bitmapClusters)
	chain(upcaseFirst, upcaseClusters)
	chain(rootFirst, 1)

	// The first clusters: bitmap (the clusters above marked used), upper-case table,
	// root folder with its three entries
	heap := make([]byte, used*cluster)
	for i := int64(0); i < used; i++ {
		heap[i/8] |= 1 << (i % 8)
	}
	copy(heap[bitmapClusters*cluster:], upcase)
	root := heap[(bitmapClusters+upcaseClusters)*cluster:]
	root[0] = 0x83
	root[1] = byte(len(name))
	for i, c := range name {
		le.PutUint16(root[2+2*i:], c)
	}
	e := root[32:]
	e[0] = 0x81
	le.PutUint32(e[20:], uint32(bitmapFirst))
	le.PutUint64(e[24:], uint64(bitmapBytes))
	e = root[64:]
	e[0] = 0x82
	var tsum uint32
	for _, v := range upcase {
		tsum = tsum>>1 | tsum<<31
		tsum += uint32(v)
	}
	le.PutUint32(e[4:], tsum)
	le.PutUint32(e[20:], uint32(upcaseFirst))
	le.PutUint64(e[24:], uint64(len(upcase)))

	return []Chunk{{0, boot}, {fatOffset, fat}, {heapOffset, heap}}, nil
}

func roundUp(v, to int64) int64 { return (v + to - 1) / to * to }

// The standard upper-case table in its compressed form, as every formatter writes it.
//
//go:embed upcase.bin
var upcase []byte
