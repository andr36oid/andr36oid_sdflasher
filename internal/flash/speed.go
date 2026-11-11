package flash

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"io"
	"sort"
	"time"
)

// CardSpeed is measured while writing. Android runs from the card, so slow small reads and
// writes make the console slow; the numbers are only a hint because card readers differ.
type CardSpeed struct {
	WriteMBps    float64 `json:"write_mbps"`
	ReadLatency  float64 `json:"read_latency_ms"`  // median 4 KiB random read
	WriteLatency float64 `json:"write_latency_ms"` // median 4 KiB random write, synced
	Slow         bool    `json:"slow"`
}

// Loose limits: a card reader adds its own delay, so only clearly slow cards are flagged.
// A1/A2 cards stay far below the latencies; old or fake cards are far above.
const (
	slowWriteMBps    = 8
	slowReadLatency  = 5.0
	slowWriteLatency = 25.0
	speedBlock       = 4096
	speedReads       = 128
	speedWrites      = 64
)

func (s *CardSpeed) judge() {
	s.Slow = (s.WriteMBps > 0 && s.WriteMBps < slowWriteMBps) || s.ReadLatency > slowReadLatency || s.WriteLatency > slowWriteLatency
}

func median(d []time.Duration) float64 {
	if len(d) == 0 {
		return 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return float64(d[len(d)/2]) / float64(time.Millisecond)
}

func randomOffset(start, length int64) int64 {
	var b [8]byte
	rand.Read(b[:])
	blocks := length / speedBlock
	return start + int64(binary.LittleEndian.Uint64(b[:])%uint64(blocks))*speedBlock
}

// unreadRange is the biggest stretch of the card this operation neither reads nor writes
// (the preflight reads every write range), so random reads there come from the card.
func unreadRange(p *Plan) (int64, int64) {
	var used []Range
	for _, w := range p.Writes {
		used = append(used, w.Range)
	}
	used = append(used, Range{"head", 0, 34 * 512}, Range{"tail", p.Size - 34*512, 34 * 512})
	sort.Slice(used, func(i, j int) bool { return used[i].Offset < used[j].Offset })
	var best, bestLen, at int64
	for _, r := range used {
		if r.Offset-at > bestLen {
			best, bestLen = at, r.Offset-at
		}
		if r.End() > at {
			at = r.End()
		}
	}
	return best, bestLen
}

// measureLatency reads at random in untouched space and writes at random inside the largest
// planned write before it is written; that write later replaces and verifies those bytes.
func measureLatency(ctx context.Context, dst Device, p *Plan) (*CardSpeed, error) {
	s := &CardSpeed{}
	buf := make([]byte, speedBlock)
	if start, length := unreadRange(p); length >= 64<<20 {
		var times []time.Duration
		for i := 0; i < speedReads; i++ {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			t := time.Now()
			if _, e := dst.ReadAt(buf, randomOffset(start, length)); e != nil && e != io.EOF {
				return nil, e
			}
			times = append(times, time.Since(t))
		}
		s.ReadLatency = median(times)
	}
	var target *Write
	for i, w := range p.Writes {
		if w.Data == nil && !w.Boot && !w.Zero && !w.Guest && (target == nil || w.Length > target.Length) {
			target = &p.Writes[i]
		}
	}
	if target != nil && target.Length >= 64<<20 {
		rand.Read(buf)
		var times []time.Duration
		for i := 0; i < speedWrites; i++ {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			t := time.Now()
			if _, e := dst.WriteAt(buf, randomOffset(target.Offset, target.Length)); e != nil {
				return nil, e
			}
			if e := dst.Sync(); e != nil {
				return nil, e
			}
			times = append(times, time.Since(t))
		}
		s.WriteLatency = median(times)
	}
	return s, nil
}
