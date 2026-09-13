package compress

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// FuzzReader feeds arbitrary bytes to a Reader capped at a 1 MiB window, so
// that a hostile header cannot make the decoder allocate the full MaxWindow
// during fuzzing (the size-derived limit already keeps it below the record's
// size). The Reader must refuse, fail, or produce exactly the number of
// bytes it was told; it must never panic.
func FuzzReader(f *testing.F) {
	f.Add([]byte{}, uint32(0))
	f.Add(emptyFrame, uint32(1))
	f.Add(compressWith(f, Params{Level: Fastest, Window: 1 << 16}, []byte("hello, hello, hello")), uint32(19))
	f.Add(compressWith(f, Params{Level: Fastest, Window: 1 << 16}, text(2000, 1)), uint32(2000))
	f.Add(append(compressWith(f, Params{Level: Fastest, Window: 1 << 16}, text(300, 2)), skippableFrame(3)...), uint32(300))
	f.Fuzz(func(t *testing.T, data []byte, size uint32) {
		r, err := newReader(nil, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if err := r.Reset(bytes.NewReader(data), uint64(size), false); err != nil {
			if !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrParams) {
				t.Fatalf("unexpected Reset error: %v", err)
			}
			return
		}
		got, err := io.ReadAll(r)
		if err != nil {
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("unexpected read error: %v", err)
			}
			return
		}
		if uint32(len(got)) != size {
			t.Fatalf("clean end with %d bytes, told %d", len(got), size)
		}
	})
}

// FuzzReaderDict is FuzzReader with the dictionary the plain target never
// has: the Reader is built over bytes the fuzzer owns, so a dictionary that
// does not parse, one whose ID the frame does not reference and one that
// parses but decodes to nothing are all reachable. NewReader must refuse a
// dictionary with ErrParams and never panic on one; from there the Reader is
// held to exactly what FuzzReader holds it to (R27).
func FuzzReaderDict(f *testing.F) {
	train := samples(200, 1)
	dict, err := BuildDict(train, 7, Default, 4096)
	if err != nil {
		f.Fatal(err)
	}
	// A second dictionary, different ID and different content: the frame
	// below references 7, so a reader holding this one must refuse it before
	// it decodes (ErrDictMismatch).
	other, err := BuildDict(samples(200, 2), 9, Default, 4096)
	if err != nil {
		f.Fatal(err)
	}
	in := samples(1, 99)[0]
	withDict := compressWith(f, Params{Level: Default, Dict: dict, Window: 1 << 16}, in)
	plain := compressWith(f, Params{Level: Default, Window: 1 << 16}, in)
	n := uint32(len(in))
	f.Add(dict, withDict, n, true)               // the valid pair
	f.Add(dict, withDict, n, false)              // record says no dictionary
	f.Add(dict, plain, n, true)                  // record says one, frame has none
	f.Add(other, withDict, n, true)              // dictionary ID mismatch
	f.Add(dict[:len(dict)/2], withDict, n, true) // a truncated dictionary
	f.Add([]byte("not a dictionary"), plain, n, false)
	f.Add([]byte(nil), withDict, n, true) // a record claiming a dictionary the reader has not
	f.Fuzz(func(t *testing.T, dict, data []byte, size uint32, withDict bool) {
		if len(dict) > MaxDictSize {
			return
		}
		r, err := newReader(dict, 1<<20)
		if err != nil {
			if !errors.Is(err, ErrParams) {
				t.Fatalf("unexpected NewReader error: %v", err)
			}
			return
		}
		defer r.Close()
		if err := r.Reset(bytes.NewReader(data), uint64(size), withDict); err != nil {
			if !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrParams) {
				t.Fatalf("unexpected Reset error: %v", err)
			}
			return
		}
		got, err := io.ReadAll(r)
		if err != nil {
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("unexpected read error: %v", err)
			}
			return
		}
		if uint32(len(got)) != size {
			t.Fatalf("clean end with %d bytes, told %d", len(got), size)
		}
	})
}

// FuzzRoundTrip compresses data at a level and padding picked by the fuzzer,
// with a small window, and reads it back.
func FuzzRoundTrip(f *testing.F) {
	f.Add([]byte("a"), uint8(0))
	f.Add(text(70<<10, 1), uint8(2))
	f.Add(noise(3000, 2), uint8(7))
	f.Add(text(300, 3), uint8(5))
	f.Fuzz(func(t *testing.T, data []byte, l uint8) {
		if len(data) == 0 {
			return
		}
		p := Params{Level: Level(l%4) + 1, Window: 1 << 16}
		if l&4 != 0 {
			p.Padding = 512
		}
		frame := compressWith(t, p, data)
		r, err := newReader(nil, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if err := r.Reset(bytes.NewReader(frame), uint64(len(data)), false); err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("round trip: %v", err)
		}
	})
}
