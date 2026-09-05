package compress

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/klauspost/compress/zstd"
)

const (
	// SampleSize is how much of a file each probe sample compresses.
	SampleSize = 256 << 10
	// RawThreshold is the compressed-to-sampled ratio above which a file is
	// stored raw (DESIGN.md §9: "if the ratio exceeds ~0.95, store raw").
	RawThreshold = 0.95
)

// Decision is Probe's answer.
type Decision struct {
	// Compressible is false when Ratio exceeds RawThreshold — or when there
	// was nothing to sample.
	Compressible bool
	// Ratio is compressed bytes over sampled bytes: of the whole file when it
	// was sampled whole, else the median of the three samples' ratios, so
	// that one unrepresentative sample — a text header on an image body, or
	// the reverse — cannot swing the decision. 1 when nothing was sampled.
	Ratio float64
	// Sampled is how many bytes were compressed to decide.
	Sampled int64
}

// probeEncoders pools fastest-level encoders with a sample-sized window, one
// per goroutine probing at a time, so that the archive layer's per-file
// goroutines do not queue on a single encoder. Each is small (a 256 KiB
// window and the fastest level's tables), and the pool lets idle ones go.
var probeEncoders = sync.Pool{
	New: func() any {
		enc, err := zstd.NewWriter(nil,
			zstd.WithEncoderLevel(zstd.SpeedFastest),
			zstd.WithWindowSize(SampleSize),
			zstd.WithEncoderConcurrency(1),
			zstd.WithEncoderCRC(false),
			zstd.WithLowerEncoderMem(true),
		)
		if err != nil {
			// The options are constants; this cannot fail.
			panic(err)
		}
		return enc
	},
}

// Probe decides whether a file of size bytes readable through r is worth
// compressing, by the policy of DESIGN.md §9: a file up to three samples
// long is compressed whole and judged on that one ratio; a longer one is
// judged from the median ratio of three SampleSize samples — its start, its
// middle, and a point two thirds in — so that a compressible header on an
// incompressible body does not get it backwards. The probe runs at the
// fastest level and costs on the order of a millisecond. A short read is an
// error; nothing else is.
func Probe(r io.ReaderAt, size int64) (Decision, error) {
	if size < 0 {
		return Decision{}, fmt.Errorf("%w: size %d", ErrParams, size)
	}
	if size == 0 {
		return Decision{Compressible: false, Ratio: 1, Sampled: 0}, nil
	}

	var ranges [][2]int64 // offset, length
	if size <= 3*SampleSize {
		ranges = [][2]int64{{0, size}}
	} else {
		mid := size/2 - SampleSize/2
		third := max(size/3*2-SampleSize/2, mid+SampleSize)
		ranges = [][2]int64{{0, SampleSize}, {mid, SampleSize}, {third, SampleSize}}
	}

	var longest int64
	for _, rg := range ranges {
		longest = max(longest, rg[1])
	}
	in := make([]byte, longest)
	out := make([]byte, 0, longest+longest/16+64)
	enc := probeEncoders.Get().(*zstd.Encoder)
	defer probeEncoders.Put(enc)

	var sampled int64
	ratios := make([]float64, 0, len(ranges))
	for _, rg := range ranges {
		in = in[:rg[1]]
		n, err := r.ReadAt(in, rg[0])
		if int64(n) < rg[1] {
			if err == nil || errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return Decision{}, fmt.Errorf("compress: probe at offset %d: %w", rg[0], err)
		}
		out = enc.EncodeAll(in, out[:0])
		sampled += rg[1]
		ratios = append(ratios, float64(len(out))/float64(rg[1]))
	}
	slices.Sort(ratios)
	ratio := ratios[len(ratios)/2]
	return Decision{Compressible: ratio <= RawThreshold, Ratio: ratio, Sampled: sampled}, nil
}

// ProbeBytes is Probe over an in-memory file.
func ProbeBytes(b []byte) Decision {
	d, err := Probe(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		// bytes.Reader cannot short-read within its length.
		panic(err)
	}
	return d
}
