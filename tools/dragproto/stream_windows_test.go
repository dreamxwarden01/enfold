//go:build windows

package main

import (
	"bytes"
	"sync"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// These exercise the stream through its Go methods. Nothing here creates a COM
// object, opens a window or calls into OLE: the point is that the semantics
// IStream requires -- a short read at the end, the three seek origins, an
// honest Stat, a clone at the same position -- are settled before any of it is
// wired to a vtable.

func newTestStream(size int64) *fileStream {
	return newFileStream(&synthFile{name: `Proto\t.bin`, size: size, seed: 0xABCDEF}, 0)
}

func TestStreamReadMatchesGenerator(t *testing.T) {
	s := newTestStream(70000)
	defer s.comDestroy()

	got := make([]byte, 0, 70000)
	buf := make([]byte, 4096)
	for {
		n := s.read(buf)
		if n == 0 {
			break
		}
		got = append(got, buf[:n]...)
	}
	if int64(len(got)) != s.file.size {
		t.Fatalf("read %d bytes, want %d", len(got), s.file.size)
	}
	want := make([]byte, s.file.size)
	patternAt(s.file.seed, 0, want)
	if !bytes.Equal(got, want) {
		t.Fatalf("stream contents differ from the generator")
	}
}

// TestStreamShortReadAtEOF is the S_FALSE case: "The value returned in pcbRead
// is less than the number of bytes requested in cb. This indicates the end of
// the stream has been reached."
func TestStreamShortReadAtEOF(t *testing.T) {
	const size = 1000
	s := newTestStream(size)
	defer s.comDestroy()

	buf := make([]byte, 600)
	if n := s.read(buf); n != 600 {
		t.Fatalf("first read gave %d, want 600 (a full buffer, so S_OK)", n)
	}
	// 400 bytes left, 600 asked for: short, and that is EOF.
	n := s.read(buf)
	if n != 400 {
		t.Fatalf("second read gave %d, want 400 (short, so S_FALSE)", n)
	}
	if s.pos != size {
		t.Fatalf("position after EOF is %d, want %d", s.pos, size)
	}
	// Past the end: zero bytes, still S_FALSE, and the count must be set.
	if n := s.read(buf); n != 0 {
		t.Fatalf("read past EOF gave %d bytes, want 0", n)
	}
	// A zero-length read is legal and must not disturb the position.
	if n := s.read(nil); n != 0 || s.pos != size {
		t.Fatalf("zero-length read gave %d and moved to %d", n, s.pos)
	}
}

func TestStreamSeekVariants(t *testing.T) {
	const size = 10000
	s := newTestStream(size)
	defer s.comDestroy()

	for _, tc := range []struct {
		name    string
		move    int64
		origin  uint32
		wantPos int64
		wantErr uintptr // 0 for success
	}{
		{"set to 100", 100, streamSeekSet, 100, 0},
		{"cur +50", 50, streamSeekCur, 150, 0},
		{"cur -150", -150, streamSeekCur, 0, 0},
		{"end -1", -1, streamSeekEnd, size - 1, 0},
		{"end 0", 0, streamSeekEnd, size, 0},
		// "It is not, however, an error to seek past the end of the stream."
		{"past the end", 1 << 20, streamSeekSet, 1 << 20, 0},
		{"cur 0 reports position", 0, streamSeekCur, 1 << 20, 0},
		// "It is an error to seek before the beginning of the stream."
		{"before the beginning", -1, streamSeekSet, 1 << 20, stgEInvalidFunction},
		{"bad origin", 0, 99, 1 << 20, stgEInvalidFunction},
	} {
		pos, err := s.seek(tc.move, tc.origin)
		if tc.wantErr == 0 {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
				continue
			}
		} else {
			if err == nil {
				t.Errorf("%s: succeeded, want %s", tc.name, hrName(tc.wantErr))
				continue
			}
			if got := uintptr(err.(hresultError)); got != tc.wantErr {
				t.Errorf("%s: %s, want %s", tc.name, hrName(got), hrName(tc.wantErr))
			}
		}
		if pos != tc.wantPos || s.pos != tc.wantPos {
			t.Errorf("%s: position %d (returned %d), want %d", tc.name, s.pos, pos, tc.wantPos)
		}
	}

	// From a negative current position the "before the beginning" rule applies
	// to STREAM_SEEK_CUR too.
	if _, err := s.seek(0, streamSeekSet); err != nil {
		t.Fatal(err)
	}
	if _, err := s.seek(-1, streamSeekCur); err == nil {
		t.Errorf("seeking one byte before the start from position 0 succeeded")
	}
	// And from the end of a zero-length stream.
	if _, err := s.seek(-int64(size)-1, streamSeekEnd); err == nil {
		t.Errorf("seeking before the start from the end succeeded")
	}
}

func TestStreamSeekThenReadResumesCorrectly(t *testing.T) {
	const size = 200000
	s := newTestStream(size)
	defer s.comDestroy()

	// Read a little, jump forward, and check the bytes are the ones that belong
	// at the new offset: the producer has to be retired and restarted.
	buf := make([]byte, 1024)
	if n := s.read(buf); n != 1024 {
		t.Fatalf("first read gave %d", n)
	}
	if _, err := s.seek(150000, streamSeekSet); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 2048)
	if n := s.read(got); n != 2048 {
		t.Fatalf("read after seek gave %d", n)
	}
	want := make([]byte, 2048)
	patternAt(s.file.seed, 150000, want)
	if !bytes.Equal(got, want) {
		t.Fatalf("bytes after a forward seek are not the ones at that offset")
	}

	// And backwards, which the design promises ("Seek (to the start, and
	// forward) ... answered honestly" -- the start included).
	if _, err := s.seek(0, streamSeekSet); err != nil {
		t.Fatal(err)
	}
	if n := s.read(got); n != 2048 {
		t.Fatalf("read after rewind gave %d", n)
	}
	patternAt(s.file.seed, 0, want)
	if !bytes.Equal(got, want) {
		t.Fatalf("bytes after rewinding to the start are wrong")
	}
}

func TestStreamStat(t *testing.T) {
	s := newTestStream(5 << 30)
	defer s.comDestroy()

	if got := s.statName(); got != "t.bin" {
		t.Errorf("statName = %q, want the leaf name %q", got, "t.bin")
	}
	if s.file.size != 5<<30 {
		t.Errorf("size = %d", s.file.size)
	}
	// The size Stat reports must not depend on how far the stream has been
	// read: cbSize is the file's length, not what is left.
	buf := make([]byte, 4096)
	s.read(buf)
	if s.file.size != 5<<30 {
		t.Errorf("size changed after a read")
	}
}

func TestStreamClone(t *testing.T) {
	s := newTestStream(100000)
	defer s.comDestroy()

	if _, err := s.seek(4321, streamSeekSet); err != nil {
		t.Fatal(err)
	}
	c := s.clone()
	defer c.comDestroy()

	// "The initial setting of the seek pointer in the cloned stream instance is
	// the same as the current setting of the seek pointer in the original."
	if c.pos != s.pos {
		t.Fatalf("clone is at %d, original at %d", c.pos, s.pos)
	}
	if c.id == s.id {
		t.Fatalf("clone shares the original's identity")
	}
	if c.file != s.file {
		t.Fatalf("clone does not reference the same file")
	}

	// "a new stream object with its own seek pointer": moving one must not move
	// the other.
	buf := make([]byte, 1000)
	if n := c.read(buf); n != 1000 {
		t.Fatalf("clone read %d", n)
	}
	if s.pos != 4321 {
		t.Fatalf("reading the clone moved the original to %d", s.pos)
	}
	want := make([]byte, 1000)
	patternAt(s.file.seed, 4321, want)
	if !bytes.Equal(buf, want) {
		t.Fatalf("clone read the wrong bytes")
	}
}

// ---------------------------------------------------------------------------
// CopyTo.
//
// CopyTo is the one method that calls out through a vtable this package did not
// build: it reads its own bytes and pushes them into the destination's Write.
// The destination below is a COM object of this package's own kind whose Write
// is a Go callback, so the call leaves through syscall.SyscallN and arrives
// through syscall.NewCallback -- the same indirect call Explorer's stream would
// receive, and the same re-entry into Go that is the reason the out-parameter
// it writes through is heap-allocated. No OLE, no window and no drag: both ends
// are this test.

type copySink struct {
	got   []byte
	calls int
	// accept caps the total the sink will take, -1 for no cap. A destination
	// that writes short is how a full disk reports itself, and CopyTo has to
	// turn that into an HRESULT of its own.
	accept int
	// fail makes Write return hr and write nothing.
	fail bool
	hr   uintptr
}

func (c *copySink) comDestroy() {}

func copySinkWrite(this uintptr, pv *byte, cb uintptr, pcbWritten *uint32) uintptr {
	o, _, ok := comLookup(this)
	if !ok {
		return eUnexpected
	}
	c, ok := o.impl.(*copySink)
	if !ok {
		return eUnexpected
	}
	c.calls++
	if c.fail {
		if pcbWritten != nil {
			*pcbWritten = 0
		}
		return c.hr
	}
	n := int(uint32(cb))
	if c.accept >= 0 {
		if room := c.accept - len(c.got); room < n {
			n = room
		}
	}
	if n > 0 && pv != nil {
		c.got = append(c.got, unsafe.Slice(pv, n)...)
	}
	if pcbWritten != nil {
		*pcbWritten = uint32(n)
	}
	return sOK
}

// copySinkUnused fills the slots CopyTo never reaches. A vtable slot may not be
// zero -- pinVtbl refuses one, and COM would jump through a null function
// pointer -- so the methods this destination never sees still have to point
// somewhere real. Nothing calls them, which is why one signature serves them
// all.
func copySinkUnused(this uintptr) uintptr { return eNotImpl }

var (
	copySinkVtblOnce sync.Once
	copySinkVtblAddr uintptr
)

func copySinkVtbl() uintptr {
	copySinkVtblOnce.Do(func() {
		unused := syscall.NewCallback(copySinkUnused)
		copySinkVtblAddr = pinVtbl(iStreamVtbl{
			QueryInterface: cbQueryInterface,
			AddRef:         cbAddRef,
			Release:        cbRelease,
			Read:           unused,
			Write:          syscall.NewCallback(copySinkWrite),
			Seek:           unused,
			SetSize:        unused,
			CopyTo:         unused,
			Commit:         unused,
			Revert:         unused,
			LockRegion:     unused,
			UnlockRegion:   unused,
			Stat:           unused,
			Clone:          unused,
		})
	})
	return copySinkVtblAddr
}

func newCopySink(c *copySink) *comObject {
	return newCOMObject("copySink", c, comIface{
		name: "IStream",
		vtbl: copySinkVtbl(),
		iids: []windows.GUID{iidIStream, iidISequentialStream},
	})
}

// pstmOf is the IStream* CopyTo receives: a pointer to the cell holding the
// vtable address, which is all an interface pointer is.
func pstmOf(o *comObject) **uintptr {
	return (**uintptr)(unsafe.Pointer(&o.block[0]))
}

func TestStreamCopyTo(t *testing.T) {
	const size = 100000
	file := synthFile{name: `Proto\copy.bin`, size: size, seed: 0x5150}
	want := make([]byte, size)
	patternAt(file.seed, 0, want)

	// newPair builds a source stream and a destination, and hands back the
	// pieces CopyTo is called with.
	newPair := func(t *testing.T, sink *copySink) (src *comObject, dst *comObject) {
		t.Helper()
		f := file
		src = newStreamObject(&f, 0)
		dst = newCopySink(sink)
		t.Cleanup(func() {
			src.release()
			dst.release()
		})
		return src, dst
	}

	t.Run("the whole file", func(t *testing.T) {
		sink := &copySink{accept: -1}
		src, dst := newPair(t, sink)
		var read, wrote uint64
		if hr := streamCopyTo(src.unknown(), pstmOf(dst), size, &read, &wrote); hr != sOK {
			t.Fatalf("CopyTo -> %s, want S_OK", hrName(hr))
		}
		if read != size || wrote != size {
			t.Fatalf("CopyTo read %d and wrote %d, want %d of each", read, wrote, size)
		}
		if !bytes.Equal(sink.got, want) {
			t.Fatalf("the destination did not receive the file's bytes")
		}
		if sink.calls < 1 {
			t.Fatalf("the destination's Write was never called")
		}
	})

	t.Run("more than the file holds", func(t *testing.T) {
		// "If the number of bytes to copy exceeds the number remaining, the
		// copy stops at the end": a short count, not an error.
		sink := &copySink{accept: -1}
		src, dst := newPair(t, sink)
		var read, wrote uint64
		if hr := streamCopyTo(src.unknown(), pstmOf(dst), 4*size, &read, &wrote); hr != sOK {
			t.Fatalf("CopyTo -> %s, want S_OK", hrName(hr))
		}
		if read != size || wrote != size {
			t.Fatalf("CopyTo read %d and wrote %d, want %d of each", read, wrote, size)
		}
		if !bytes.Equal(sink.got, want) {
			t.Fatalf("the destination did not receive the file's bytes")
		}
	})

	t.Run("part of the file, from where the stream is", func(t *testing.T) {
		sink := &copySink{accept: -1}
		src, dst := newPair(t, sink)
		s, ok := streamOf(src.unknown())
		if !ok {
			t.Fatal("the source stream does not resolve")
		}
		if _, err := s.seek(50000, streamSeekSet); err != nil {
			t.Fatal(err)
		}
		var read, wrote uint64
		if hr := streamCopyTo(src.unknown(), pstmOf(dst), 1000, &read, &wrote); hr != sOK {
			t.Fatalf("CopyTo -> %s", hrName(hr))
		}
		if read != 1000 || wrote != 1000 {
			t.Fatalf("CopyTo read %d and wrote %d, want 1000 of each", read, wrote)
		}
		if !bytes.Equal(sink.got, want[50000:51000]) {
			t.Fatalf("CopyTo did not copy from the seek pointer")
		}
		// "the seek pointer in each stream instance is adjusted for the number
		// of bytes read or written".
		if s.pos != 51000 {
			t.Fatalf("the source is at %d after copying 1000 bytes from 50000", s.pos)
		}
	})

	t.Run("nothing at all", func(t *testing.T) {
		sink := &copySink{accept: -1}
		src, dst := newPair(t, sink)
		var read, wrote uint64
		if hr := streamCopyTo(src.unknown(), pstmOf(dst), 0, &read, &wrote); hr != sOK {
			t.Fatalf("CopyTo(0) -> %s, want S_OK", hrName(hr))
		}
		if read != 0 || wrote != 0 || sink.calls != 0 {
			t.Fatalf("CopyTo(0) read %d, wrote %d, called Write %d times", read, wrote, sink.calls)
		}
	})

	t.Run("the counts are optional", func(t *testing.T) {
		// pcbRead and pcbWritten may both be NULL; the copy still has to happen.
		sink := &copySink{accept: -1}
		src, dst := newPair(t, sink)
		if hr := streamCopyTo(src.unknown(), pstmOf(dst), size, nil, nil); hr != sOK {
			t.Fatalf("CopyTo with no out-parameters -> %s", hrName(hr))
		}
		if !bytes.Equal(sink.got, want) {
			t.Fatalf("the destination did not receive the file's bytes")
		}
	})

	t.Run("a destination that writes short", func(t *testing.T) {
		sink := &copySink{accept: 1000}
		src, dst := newPair(t, sink)
		var read, wrote uint64
		hr := streamCopyTo(src.unknown(), pstmOf(dst), size, &read, &wrote)
		// The destination reported success while taking less than it was
		// given, which is a full medium however politely it is said.
		if hr != stgEMediumFull {
			t.Fatalf("CopyTo into a short-writing destination -> %s, want STG_E_MEDIUMFULL", hrName(hr))
		}
		if wrote != 1000 {
			t.Fatalf("CopyTo reports %d bytes written, the destination took 1000", wrote)
		}
		if wrote >= read {
			t.Fatalf("CopyTo read %d and claims to have written %d", read, wrote)
		}
		if !bytes.Equal(sink.got, want[:1000]) {
			t.Fatalf("the bytes that did land are not the first 1000")
		}
	})

	t.Run("a destination that fails", func(t *testing.T) {
		sink := &copySink{accept: -1, fail: true, hr: stgEAccessDenied}
		src, dst := newPair(t, sink)
		var read, wrote uint64
		hr := streamCopyTo(src.unknown(), pstmOf(dst), size, &read, &wrote)
		// The destination's HRESULT is passed through rather than replaced:
		// the caller wants to know why, and "medium full" would be a guess.
		if hr != stgEAccessDenied {
			t.Fatalf("CopyTo -> %s, want the destination's STG_E_ACCESSDENIED", hrName(hr))
		}
		if wrote != 0 {
			t.Fatalf("CopyTo reports %d bytes written to a destination that refused them", wrote)
		}
	})

	t.Run("no destination", func(t *testing.T) {
		sink := &copySink{accept: -1}
		src, _ := newPair(t, sink)
		read, wrote := uint64(7), uint64(9)
		if hr := streamCopyTo(src.unknown(), nil, size, &read, &wrote); hr != stgEInvalidPointer {
			t.Fatalf("CopyTo(pstm=nil) -> %s, want STG_E_INVALIDPOINTER", hrName(hr))
		}
		if read != 0 || wrote != 0 {
			t.Fatalf("a refused CopyTo left read=%d wrote=%d, want both zeroed", read, wrote)
		}
	})

	t.Run("an unknown source", func(t *testing.T) {
		sink := &copySink{accept: -1}
		_, dst := newPair(t, sink)
		if hr := streamCopyTo(0xBADC0FFEE0, pstmOf(dst), size, nil, nil); hr != eUnexpected {
			t.Fatalf("CopyTo on an unknown this -> %s, want E_UNEXPECTED", hrName(hr))
		}
	})
}

func TestReadThrottle(t *testing.T) {
	var th readThrottle
	// The first ten calls are always logged, however small.
	for i := 1; i <= readThrottleFirst; i++ {
		log, calls, _ := th.want(64 << 10)
		if !log {
			t.Fatalf("call %d was throttled", i)
		}
		if calls != int64(i) {
			t.Fatalf("call count %d, want %d", calls, i)
		}
	}
	// After that, only on crossing a 64 MiB boundary.
	quiet := 0
	for th.total < readThrottleBytes*3 {
		log, _, _ := th.want(64 << 10)
		if !log {
			quiet++
		}
	}
	if quiet == 0 {
		t.Fatalf("nothing was throttled")
	}
	if th.calls < 3000 {
		t.Fatalf("expected thousands of calls to reach 192 MiB, got %d", th.calls)
	}
}
