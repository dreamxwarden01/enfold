package compress

import (
	"errors"
	"fmt"

	"github.com/klauspost/compress/zstd"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// Level is one of the four speed presets the pure-Go zstd offers
// (DECISIONS.md 2026-09-05). The mapping to reference zstd levels is the
// library's and may drift between versions; only the presets are stable.
type Level uint8

const (
	Fastest Level = iota + 1 // ≈ zstd 1–2
	Default                  // ≈ zstd 3
	Better                   // ≈ zstd 7–8, two to three times the CPU
	Best                     // the best the library has, whatever it costs
)

func (l Level) String() string {
	switch l {
	case Fastest:
		return "fastest"
	case Default:
		return "default"
	case Better:
		return "better"
	case Best:
		return "best"
	}
	return fmt.Sprintf("level(%d)", uint8(l))
}

func (l Level) zstd() (zstd.EncoderLevel, bool) {
	switch l {
	case Fastest:
		return zstd.SpeedFastest, true
	case Default:
		return zstd.SpeedDefault, true
	case Better:
		return zstd.SpeedBetterCompression, true
	case Best:
		return zstd.SpeedBestCompression, true
	}
	return 0, false
}

const (
	// MinWindow and MaxWindow bound the match window (what RAR calls the
	// dictionary size): a power of two from 1 KiB to 512 MiB. MaxWindow is
	// part of the format (R27): the largest window this program ever writes
	// is the largest any reader of the format will ever decode, so it is
	// pinned here rather than taken from the library.
	MinWindow = 1 << 10
	MaxWindow = 1 << 29

	// MaxParallelWindow bounds Window when Concurrency is above 1: the
	// parallel encoder buffers jobs of four times the window, several of
	// them at once (see Params.Concurrency), and 128 MiB jobs are as far as
	// that is worth taking.
	MaxParallelWindow = 32 << 20

	// MaxDictSize is the index's bound on a dictionary (format.MaxDictSize,
	// R27).
	MaxDictSize = format.MaxDictSize

	// MaxPadding bounds Params.Padding (the library's own limit).
	MaxPadding = 1 << 30
)

// The library must be able to write and decode the format's window range.
const (
	_ uint = zstd.MaxWindowSize - MaxWindow
	_ uint = MinWindow - zstd.MinWindowSize
)

var (
	// ErrParams reports a Params, size or dictionary the package refuses.
	ErrParams = errors.New("compress: invalid parameters")

	// ErrCorrupt is the root of every decoding failure that is the frame's
	// fault: malformed data, a failed checksum, a second frame or trailing
	// bytes, or a frame that contradicts the index record it was decoded
	// against. ErrSizeMismatch, ErrDictMismatch and ErrWindow all wrap it.
	// Errors from the underlying reader are returned as they are, not
	// wrapped.
	ErrCorrupt = errors.New("compress: corrupt data")
	// ErrSizeMismatch: the frame declares, or decodes to, a length other than
	// the record's.
	ErrSizeMismatch = fmt.Errorf("%w: decompressed size differs from the record", ErrCorrupt)
	// ErrDictMismatch: the frame references a dictionary the record does not
	// give it, or none when the record requires one.
	ErrDictMismatch = fmt.Errorf("%w: frame dictionary differs from the record", ErrCorrupt)
	// ErrWindow: the frame demands a window larger than the record's size
	// can need, or larger than MaxWindow.
	ErrWindow = fmt.Errorf("%w: window exceeds the limit", ErrCorrupt)

	// ErrClosed is returned by a Writer after Release and by a Reader after
	// Close, both of which make the value unusable. Writer.Close only ends
	// the stream; the Writer stays usable through Reset.
	ErrClosed = errors.New("compress: closed")
	// ErrNoStream is returned when no stream is active: by Write before the
	// first Reset and after Close ended the stream, and by Read before the
	// first Reset or after a Reset that failed. Read past the end of a
	// finished stream keeps returning io.EOF.
	ErrNoStream = errors.New("compress: no active stream")
)

// Params configures a Writer. The zero value is invalid: pick a Level.
type Params struct {
	// Level is the speed preset.
	Level Level
	// Window is the match window in bytes: a power of two in
	// [MinWindow, MaxWindow], or 0 for the level's default (at most 8 MiB).
	// The encoder's memory grows with it, and above the default so does
	// encoding time; a reader's memory does not, beyond the file's own size,
	// because a frame's window is bounded by the file's length (R27).
	Window int
	// Dict, when set, is the archive's trained dictionary (BuildDict): every
	// frame written references it, and a reader must be given the same
	// dictionary. Files written with it are storage 3 in the index.
	Dict []byte
	// Concurrency is the number of goroutines a stream may compress on. 0 or
	// 1 compresses synchronously on the caller's goroutine; larger values
	// split the input into jobs compressed in parallel, except with a
	// dictionary, where the library compresses sequentially.
	//
	// Parallel jobs are what makes Window expensive: each job buffers four
	// times the window (at least 512 KiB), and about 2·Concurrency + 2 such
	// buffers are live at once — Window 8 MiB with Concurrency 4 is on the
	// order of 320 MiB. Window is therefore limited to MaxParallelWindow when
	// Concurrency is above 1, and a Writer with Concurrency above 1 must be
	// Released (see Writer).
	Concurrency int
	// Padding, when non-zero, pads every stream's output to a multiple of
	// this many bytes with a skippable frame of random content, blunting
	// the compressed-size side channel of DESIGN.md §11 trap 8. At most
	// MaxPadding.
	Padding int
}

// Validate reports the first thing wrong with p. It parses p.Dict in full,
// which is O(len(p.Dict)): validate a Params once, not per file.
func (p Params) Validate() error {
	if _, ok := p.Level.zstd(); !ok {
		return fmt.Errorf("%w: level %d", ErrParams, uint8(p.Level))
	}
	if p.Window != 0 && (p.Window < MinWindow || p.Window > MaxWindow || p.Window&(p.Window-1) != 0) {
		return fmt.Errorf("%w: window %d is not a power of two in [%d, %d]", ErrParams, p.Window, MinWindow, MaxWindow)
	}
	if p.Concurrency < 0 {
		return fmt.Errorf("%w: concurrency %d", ErrParams, p.Concurrency)
	}
	if p.Concurrency > 1 && p.Window > MaxParallelWindow {
		return fmt.Errorf("%w: window %d with concurrency %d exceeds %d", ErrParams, p.Window, p.Concurrency, MaxParallelWindow)
	}
	if p.Dict != nil {
		if _, err := DictID(p.Dict); err != nil {
			return err
		}
	}
	if p.Padding < 0 || p.Padding > MaxPadding {
		return fmt.Errorf("%w: padding %d", ErrParams, p.Padding)
	}
	return nil
}

func (p Params) options() ([]zstd.EOption, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	level, _ := p.Level.zstd()
	conc := max(p.Concurrency, 1)
	opts := []zstd.EOption{
		zstd.WithEncoderLevel(level),
		zstd.WithEncoderConcurrency(conc),
		zstd.WithEncoderCRC(true),
	}
	if p.Window != 0 {
		opts = append(opts, zstd.WithWindowSize(p.Window))
	}
	if p.Dict != nil {
		opts = append(opts, zstd.WithEncoderDict(p.Dict))
	} else if conc > 1 {
		opts = append(opts, zstd.WithConcurrentBlocks(true))
	}
	if p.Padding != 0 {
		opts = append(opts, zstd.WithEncoderPadding(p.Padding))
	}
	return opts, nil
}
