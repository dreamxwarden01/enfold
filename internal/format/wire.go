package format

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

// ErrInvalid is the root of every decoding failure: anything that is not a
// well-formed v1 structure. ErrTruncated and ErrTrailing are ErrInvalid too, so
// errors.Is(err, ErrInvalid) answers "was this well-formed" for all of them,
// while the narrower sentinels distinguish the two shape faults. Every error
// carries the structure being decoded and the offset at which it failed.
var (
	ErrInvalid   = errors.New("format: invalid")
	ErrTruncated = fmt.Errorf("%w: truncated", ErrInvalid)
	ErrTrailing  = fmt.Errorf("%w: trailing bytes", ErrInvalid)
	// ErrVersion: the structure decoded and, where it carries one, its
	// checksum holds, but its format_version is not one this reader supports.
	// It is an ErrInvalid like the rest — a v2 structure is not a well-formed
	// v1 one — and the distinction matters where a reader recovers from
	// damage: an envelope of an unsupported version is intact, not torn, so
	// it is refused rather than treated as absent (docs/FORMAT.md §10, R33).
	ErrVersion = fmt.Errorf("%w: unsupported format_version", ErrInvalid)
)

func invalidf(msg string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(msg, a...))
}

// versionf reports a format_version this reader does not support.
func versionf(what string, got uint16) error {
	return fmt.Errorf("%w: %s: format_version %d, want %d", ErrVersion, what, got, FormatVersion)
}

// reader decodes little-endian values from a byte slice. Errors are sticky:
// after the first failure every accessor returns a zero value and the error is
// reported by done(). This keeps decoders linear and lets them be checked
// against hostile input without a branch per field. ctx names the structure
// for error messages.
type reader struct {
	b   []byte
	off int
	ctx string
	err error
}

func newReader(b []byte, ctx string) *reader { return &reader{b: b, ctx: ctx} }

func (r *reader) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

func (r *reader) invalidf(msg string, a ...any) {
	r.fail(fmt.Errorf("%w: %s at offset %d: %s", ErrInvalid, r.ctx, r.off, fmt.Sprintf(msg, a...)))
}

// truncatedf records a truncation with context: a length field that claims
// more bytes than the input holds is a truncation, not a malformed value.
func (r *reader) truncatedf(msg string, a ...any) {
	r.fail(fmt.Errorf("%w: %s at offset %d: %s", ErrTruncated, r.ctx, r.off, fmt.Sprintf(msg, a...)))
}

func (r *reader) remaining() int { return len(r.b) - r.off }

// take returns the next n bytes as a sub-slice of the input (no copy) or nil
// after recording a truncation error.
func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > r.remaining() {
		r.fail(fmt.Errorf("%w: %s at offset %d needs %d bytes, %d left", ErrTruncated, r.ctx, r.off, n, r.remaining()))
		return nil
	}
	s := r.b[r.off : r.off+n : r.off+n]
	r.off += n
	return s
}

func (r *reader) u8() uint8 {
	s := r.take(1)
	if s == nil {
		return 0
	}
	return s[0]
}

func (r *reader) u16() uint16 {
	s := r.take(2)
	if s == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(s)
}

func (r *reader) u32() uint32 {
	s := r.take(4)
	if s == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(s)
}

func (r *reader) u64() uint64 {
	s := r.take(8)
	if s == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(s)
}

func (r *reader) i64() int64 { return int64(r.u64()) }

// fixed copies exactly len(dst) bytes into dst.
func (r *reader) fixed(dst []byte) {
	if s := r.take(len(dst)); s != nil {
		copy(dst, s)
	}
}

// skip discards n bytes (reserved fields: ignored on read, §1).
func (r *reader) skip(n int) { r.take(n) }

// bytes16 reads a u16 length prefix and that many bytes, returning a copy.
// A zero-length field decodes to nil.
func (r *reader) bytes16() []byte {
	n := int(r.u16())
	s := r.take(n)
	if s == nil || n == 0 {
		return nil
	}
	return append([]byte(nil), s...)
}

// bytes32 is bytes16 with a u32 prefix. The prefix is checked against the
// remaining input before any allocation.
func (r *reader) bytes32() []byte {
	n := r.u32()
	if r.err != nil {
		return nil
	}
	if uint64(n) > uint64(r.remaining()) {
		r.fail(fmt.Errorf("%w: %s at offset %d declares %d bytes, %d left", ErrTruncated, r.ctx, r.off, n, r.remaining()))
		return nil
	}
	s := r.take(int(n))
	if s == nil || n == 0 {
		return nil
	}
	return append([]byte(nil), s...)
}

// str reads a u16-prefixed UTF-8 string and rejects invalid UTF-8.
func (r *reader) str() string {
	start := r.off
	n := int(r.u16())
	s := r.take(n)
	if s == nil {
		return ""
	}
	if !utf8.Valid(s) {
		r.fail(fmt.Errorf("%w: %s at offset %d: string is not valid UTF-8", ErrInvalid, r.ctx, start))
		return ""
	}
	return string(s)
}

// sub returns a reader over the next n bytes with its own context. The parent
// advances past them.
func (r *reader) sub(n int, ctx string) *reader {
	s := r.take(n)
	if s == nil {
		return &reader{err: r.err, ctx: ctx}
	}
	return newReader(s, ctx)
}

// count validates a record count against the bytes left, given the smallest
// possible encoded record, so that a hostile count cannot drive allocation.
func (r *reader) count(n uint32, minRecord int) int {
	if r.err != nil {
		return 0
	}
	if uint64(n)*uint64(minRecord) > uint64(r.remaining()) {
		r.invalidf("count %d needs at least %d bytes, %d left", n, uint64(n)*uint64(minRecord), r.remaining())
		return 0
	}
	return int(n)
}

// done reports the sticky error, or ErrTrailing if input remains.
func (r *reader) done() error {
	if r.err != nil {
		return r.err
	}
	if r.remaining() != 0 {
		return fmt.Errorf("%w: %s has %d bytes left after offset %d", ErrTrailing, r.ctx, r.remaining(), r.off)
	}
	return nil
}

// writer appends little-endian values. Its only failure modes are fields too
// long for their length prefix and invalid UTF-8, both programming errors on
// the encoding side, reported through done().
type writer struct {
	b   []byte
	err error
}

func (w *writer) fail(err error) {
	if w.err == nil {
		w.err = err
	}
}

func (w *writer) u8(v uint8)     { w.b = append(w.b, v) }
func (w *writer) u16(v uint16)   { w.b = binary.LittleEndian.AppendUint16(w.b, v) }
func (w *writer) u32(v uint32)   { w.b = binary.LittleEndian.AppendUint32(w.b, v) }
func (w *writer) u64(v uint64)   { w.b = binary.LittleEndian.AppendUint64(w.b, v) }
func (w *writer) i64(v int64)    { w.u64(uint64(v)) }
func (w *writer) fixed(b []byte) { w.b = append(w.b, b...) }
func (w *writer) zeros(n int)    { w.b = append(w.b, make([]byte, n)...) }

func (w *writer) bytes16(b []byte) {
	if len(b) > 0xFFFF {
		w.fail(invalidf("field of %d bytes exceeds the u16 length prefix", len(b)))
		return
	}
	w.u16(uint16(len(b)))
	w.b = append(w.b, b...)
}

func (w *writer) bytes32(b []byte) {
	if uint64(len(b)) > 0xFFFFFFFF {
		w.fail(invalidf("field of %d bytes exceeds the u32 length prefix", len(b)))
		return
	}
	w.u32(uint32(len(b)))
	w.b = append(w.b, b...)
}

func (w *writer) str(s string) {
	if !utf8.ValidString(s) {
		w.fail(invalidf("string is not valid UTF-8"))
		return
	}
	w.bytes16([]byte(s))
}

func (w *writer) done() ([]byte, error) {
	if w.err != nil {
		return nil, w.err
	}
	return w.b, nil
}
