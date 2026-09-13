package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// What the format rulings of 2026-09-13 ask of this package (DECISIONS;
// FORMAT.md §7, §10, §11, R31, R33, R36): a superblock's seq is in the AAD of
// the index it names, so a promoted copy opens nothing; seq continues across a
// compaction rather than starting again at 1; an intact envelope of an
// unsupported format_version is refused rather than recovered from; and no
// writer wraps the sequence at 2^64 − 1.

// sbAtOffset decodes the superblock copy at off.
func sbAtOffset(t testing.TB, img []byte, off uint64) *format.ArchiveSuperblock {
	t.Helper()
	sb, err := format.DecodeArchiveSuperblock(img[off : off+format.SuperblockSize])
	if err != nil {
		t.Fatalf("the superblock copy at 0x%x: %v", off, err)
	}
	return sb
}

// The forgery the AAD's seq exists to stop: keep the index that is there,
// raise the winning copy's plaintext seq — the superblock is checksummed and
// not authenticated, so the checksum is recomputed for free — and the copy
// would have passed as one commit newer than it is. With seq in the AAD the
// index authenticates under no other number, so the file opens nothing at all
// rather than serving older contents under a current-looking number (§11,
// R36).
func TestARaisedSeqOpensNothing(t *testing.T) {
	a, fx := newFixture(t, Options{})
	want := text(4000, 61)
	info := add(t, a, root, "f", want)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	good := snapshot(t, fx.path)

	live, _, _ := copies(t, good)
	forged := bytes.Clone(good)
	sb := sbAtOffset(t, forged, live)
	real := sb.Seq
	sb.Seq++
	enc, err := sb.Encode()
	if err != nil {
		t.Fatal(err)
	}
	copy(forged[live:], enc)
	if sbAtOffset(t, forged, live).Seq != real+1 {
		t.Fatal("the forged copy does not carry the raised seq")
	}
	restore(t, fx.path, forged)
	if _, err := Open(fx.path, []Key{fx.key}, fx.opts); !errors.Is(err, ErrKey) {
		t.Fatalf("a superblock promoted by one opened the archive: %v", err)
	}
	// The tolerance runs one way: a copy may name a state one commit newer
	// than its own number, which is R31's retirement — this state's
	// superblock written into the losing copy at one less, since two valid
	// copies are never equal (§4) — and that copy opens on the state its
	// index holds, not on an older one.
	_, loser, _ := copies(t, good)
	retired := bytes.Clone(good)
	sb = sbAtOffset(t, retired, live)
	sb.Seq--
	if enc, err = sb.Encode(); err != nil {
		t.Fatal(err)
	}
	copy(retired[loser:], enc)
	clear(retired[live : live+format.SuperblockSize])
	restore(t, fx.path, retired)
	if b, err := Open(fx.path, []Key{fx.key}, fx.opts); err != nil {
		t.Fatalf("a copy retired one below the index it names did not open: %v", err)
	} else {
		if got := extract(t, b, info.ID); !bytes.Equal(got, want) {
			t.Error("the retired copy read the wrong contents")
		}
		b.Close()
	}

	// Untouched, it opens and reads as it always did.
	restore(t, fx.path, good)
	b := fx.open(t)
	if b.Seq() != real {
		t.Fatalf("the restored file is at seq %d, want %d", b.Seq(), real)
	}
	if got := extract(t, b, info.ID); !bytes.Equal(got, want) {
		t.Error("the restored file read the wrong contents")
	}
	b.Close()
}

// A compaction writes a fresh file and still continues the sequence: copy A at
// the source's committed seq plus one, copy B the same superblock one lower,
// which is §4's A/B rule at n and n − 1. One archive_id therefore never names
// two different contents by the same number (R33, R36), and the fresh file's
// losing copy is a fallback that works.
func TestCompactionContinuesTheSequence(t *testing.T) {
	a, fx := newFixture(t, Options{})
	want := text(3000, 71)
	keep := add(t, a, root, "keep", want)
	gone := add(t, a, root, "gone", noise(9000, 72))
	if _, err := a.Delete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	before := a.Seq()
	if before < 2 {
		t.Fatalf("the fixture committed only %d times", before)
	}
	if _, _, err := a.Compact(ctx, nil); err != nil {
		t.Fatal(err)
	}

	b := fx.open(t)
	if b.Seq() != before+1 {
		t.Fatalf("the compacted file is at seq %d, the source had committed %d", b.Seq(), before)
	}
	if got := extract(t, b, keep.ID); !bytes.Equal(got, want) {
		t.Error("the compacted file read the wrong contents")
	}
	b.Close()

	img := snapshot(t, fx.path)
	live, loser, ok := copies(t, img)
	if !ok {
		t.Fatal("the compacted file's losing copy does not decode")
	}
	if got := sbAtOffset(t, img, loser).Seq; got != before {
		t.Fatalf("the losing copy is at seq %d, want %d", got, before)
	}
	// R31 one level down: with the live copy lost, what the losing copy names
	// still opens and reads.
	torn := bytes.Clone(img)
	clear(torn[live : live+format.SuperblockSize])
	c := openImage(t, fx, torn, "compacted-fallback")
	if c.Stale() == nil {
		t.Error("the zeroed live copy of the compacted file went unreported")
	}
	if got := extract(t, c, keep.ID); !bytes.Equal(got, want) {
		t.Error("the compacted file's fallback read the wrong contents")
	}
	c.Close()
}

// An envelope that decodes and whose checksum holds is what a writer meant to
// leave: if its format_version is not one this reader supports, the file is
// refused outright — no key tried, nothing recovered, nothing repaired. A
// torn envelope is the other case and recovers as it always did (§10, R33).
func TestAnIntactEnvelopeOfAnotherVersionIsRefused(t *testing.T) {
	a, fx := newFixture(t, Options{})
	want := text(2000, 81)
	info := add(t, a, root, "f", want)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	good := snapshot(t, fx.path)
	// R33's way in when the envelope is absent: the archive_id the caller's
	// own registry record holds.
	opts := fx.opts
	opts.ArchiveID = fx.archiveID

	future := bytes.Clone(good)
	binary.LittleEndian.PutUint16(future[format.EnvelopeOff+8:], format.FormatVersion+1)
	sum := sha256.Sum256(future[format.EnvelopeOff : format.EnvelopeOff+format.SuperblockSize-32])
	copy(future[format.EnvelopeOff+format.SuperblockSize-32:], sum[:])
	restore(t, fx.path, future)
	_, err := Open(fx.path, []Key{fx.key}, opts)
	if !errors.Is(err, format.ErrVersion) {
		t.Fatalf("an intact envelope of format_version %d: %v", format.FormatVersion+1, err)
	}
	if !errors.Is(err, format.ErrInvalid) {
		t.Error("the version refusal is not a decoding verdict")
	}
	if got := snapshot(t, fx.path); !bytes.Equal(got, future) {
		t.Error("the refused open wrote to the file")
	}

	torn := bytes.Clone(good)
	for i := format.EnvelopeOff + format.SuperblockSize - 32; i < format.EnvelopeOff+format.SuperblockSize; i++ {
		torn[i] ^= 0xff
	}
	restore(t, fx.path, torn)
	c, err := Open(fx.path, []Key{fx.key}, opts)
	if err != nil {
		t.Fatalf("a torn envelope no longer recovers: %v", err)
	}
	if !c.EnvelopeStale() {
		t.Error("the torn envelope was not reported as owed a rewrite")
	}
	if got := extract(t, c, info.ID); !bytes.Equal(got, want) {
		t.Error("the recovered file read the wrong contents")
	}
	c.Close()
}

// A read that leaned on the tolerance adopts the number it authenticated
// under — the state's, not the copy's — so the next commit seals at that
// number plus one. Otherwise the writer would seal a second state under a
// number an older copy of the file already holds, and the file it replaced
// could be replayed as the current copy: valid, openable, and at the seq the
// registry recorded (§11, "the state's number").
func TestATolerantOpenContinuesFromTheStatesNumber(t *testing.T) {
	// Both shapes where a file's two copies name one state: a fresh archive
	// (copy A at 1, copy B at 0, both naming the index sealed at 1, §4) and a
	// compacted one (A at n, B at n − 1, R33). In each, losing the winner
	// leaves a copy whose own number is one below the state it holds, which
	// is where the tolerance and the adoption are exercised.
	replay := func(t *testing.T, a *Archive, fx *fixture, id [16]byte, want []byte) {
		t.Helper()
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, fx.path)
		liveOff, _, _ := copies(t, before)
		state := sbAtOffset(t, before, liveOff).Seq

		// The winning copy is lost; the other one opens through the
		// tolerance, on the state it names rather than on its own number.
		lost := bytes.Clone(before)
		clear(lost[liveOff : liveOff+format.SuperblockSize])
		restore(t, fx.path, lost)
		b := fx.open(t)
		if b.Stale() == nil {
			t.Error("the lost copy went unreported")
		}
		if b.Seq() != state {
			t.Fatalf("the fallback reads as seq %d, the state it holds is %d", b.Seq(), state)
		}
		if want != nil {
			if got := extract(t, b, id); !bytes.Equal(got, want) {
				t.Error("the fallback read the wrong contents")
			}
		}
		// A write from that handle, and the receipt a registry would record.
		add(t, b, root, "written-after", text(900, 103))
		receipt := b.Seq()
		if receipt <= state {
			t.Fatalf("the commit left the sequence at %d, from %d", receipt, state)
		}
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}

		// The replay: the bytes from before that write, put back in place.
		// They must not read as the copy the registry last saw.
		restore(t, fx.path, before)
		c := fx.open(t)
		if c.Seq() >= receipt {
			t.Fatalf("a replayed copy reads as current: seq %d, the receipt's %d", c.Seq(), receipt)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a fresh file's copy B", func(t *testing.T) {
		a, fx := newFixture(t, Options{})
		replay(t, a, fx, [16]byte{}, nil)
	})

	// The same shape one commit wider: a compacted file, whose copy B carries
	// the state's superblock at n − 1 (R33).
	t.Run("a compacted file's copy B", func(t *testing.T) {
		a, fx := newFixture(t, Options{})
		want := text(2500, 102)
		info := add(t, a, root, "kept", want)
		gone := add(t, a, root, "gone", noise(8000, 104))
		if _, err := a.Delete(ctx, gone.ID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.Compact(ctx, nil); err != nil {
			t.Fatal(err)
		}
		replay(t, fx.open(t), fx, info.ID, want)
	})
}

// seq counts commits and never wraps: a writer that would pass 2^64 − 1
// refuses the commit rather than wrap to 0, since a wrapped counter would make
// the new state lose to the old one (§4). No file reaches it, so every writer
// is put there by hand.
func TestNoWriterWrapsTheSequence(t *testing.T) {
	// A transaction writes file content into free extents as it goes, and
	// Abort gives back only what it appended — an interior extent it filled
	// stays filled. So the refusal is at the point the transaction begins,
	// not at the flip: an exhausted archive opens no writable transaction,
	// and an Add leaves the file byte for byte as it was.
	t.Run("a transaction never begins", func(t *testing.T) {
		a, fx := newFixture(t, Options{})
		add(t, a, root, "front", noise(20000, 94))
		hole := add(t, a, root, "hole", noise(20000, 95))
		add(t, a, root, "back", noise(20000, 96))
		if _, err := a.Delete(ctx, hole.ID); err != nil {
			t.Fatal(err)
		}
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, fx.path)

		b := fx.open(t)
		if b.free.total() == 0 {
			t.Fatal("the fixture left no free extent to write into")
		}
		b.mu.Lock()
		b.sb.Seq = math.MaxUint64
		b.mu.Unlock()
		if _, err := b.Begin(); !errors.Is(err, ErrSeqExhausted) {
			t.Fatalf("a transaction at 2^64 − 1: %v", err)
		}
		if _, _, err := b.Add(ctx, root, "new", bytes.NewReader(noise(15000, 97)), 15000); !errors.Is(err, ErrSeqExhausted) {
			t.Fatalf("an Add at 2^64 − 1: %v", err)
		}
		move := Move{From: extent{Off: format.ArchiveDataStart, Len: 16}, To: extent{Off: format.ArchiveDataStart, Len: 16}}
		if _, err := b.MoveExtents(ctx, []Move{move}, nil); !errors.Is(err, ErrSeqExhausted) {
			t.Fatalf("a move at 2^64 − 1: %v", err)
		}
		if _, err := b.Publish(ctx); !errors.Is(err, ErrSeqExhausted) {
			t.Fatalf("a publish at 2^64 − 1: %v", err)
		}
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
		if got := snapshot(t, fx.path); !bytes.Equal(got, before) {
			t.Error("the refused writes changed the file")
		}
	})

	t.Run("a commit", func(t *testing.T) {
		a, fx := newFixture(t, Options{})
		add(t, a, root, "f", text(1000, 91))
		real := a.Seq()
		size, _, _ := a.Stat()

		a.mu.Lock()
		a.sb.Seq = math.MaxUint64
		a.mu.Unlock()
		if _, err := a.Publish(ctx); !errors.Is(err, ErrSeqExhausted) {
			t.Fatalf("a commit at 2^64 − 1: %v", err)
		}
		if err := a.Broken(); err != nil {
			t.Fatalf("the refusal broke the archive: %v", err)
		}
		if got, _, _ := a.Stat(); got != size {
			t.Errorf("the refused commit changed the file size: %d, was %d", got, size)
		}

		// Refused, and nothing else is: the handle commits again once the
		// sequence is where a real file's would be.
		a.mu.Lock()
		a.sb.Seq = real
		a.mu.Unlock()
		if _, err := a.Publish(ctx); err != nil {
			t.Fatalf("the archive did not commit after the refusal: %v", err)
		}
		// One further on, or two when that commit gave the tail back (R31).
		after := a.Seq()
		if after <= real {
			t.Fatalf("the commit after the refusal left the sequence at %d, from %d", after, real)
		}
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		img := snapshot(t, fx.path)
		live, _, _ := copies(t, img)
		if got := sbAtOffset(t, img, live).Seq; got != after {
			t.Fatalf("the file is at seq %d, want %d", got, after)
		}
	})

	// The follow-up commit that gives the tail back (R31 as amended,
	// trim.go). A commit refuses at 2^64 − 1 before this one is ever
	// reached, so the guard is exercised where it sits.
	t.Run("the follow-up commit", func(t *testing.T) {
		a, _ := newFixture(t, Options{})
		add(t, a, root, "keep", text(1000, 92))
		plain, err := a.index.Encode()
		if err != nil {
			t.Fatal(err)
		}
		a.mu.Lock()
		size := a.size
		a.sb.Seq = math.MaxUint64
		err = a.reclaimTail(plain, a.kid, a.indexKey)
		unchanged := a.size == size && a.sb.Seq == math.MaxUint64
		a.mu.Unlock()
		if !errors.Is(err, ErrSeqExhausted) {
			t.Fatalf("the follow-up commit at 2^64 − 1: %v", err)
		}
		if !unchanged {
			t.Error("the refused follow-up commit moved the archive on")
		}
		if err := a.Broken(); err != nil {
			t.Fatalf("the refusal broke the archive: %v", err)
		}
	})

	t.Run("a compaction", func(t *testing.T) {
		a, fx := newFixture(t, Options{})
		add(t, a, root, "f", text(1000, 93))
		real := a.Seq()

		a.mu.Lock()
		a.sb.Seq = math.MaxUint64
		a.mu.Unlock()
		if _, _, err := a.Compact(ctx, nil); !errors.Is(err, ErrSeqExhausted) {
			t.Fatalf("a compaction at 2^64 − 1: %v", err)
		}
		// Nothing was written, not even the file it compacts into.
		entries, err := os.ReadDir(filepath.Dir(fx.path))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != filepath.Base(fx.path) {
			t.Fatalf("the refused compaction left %d files beside the archive", len(entries)-1)
		}
		a.mu.Lock()
		a.sb.Seq = real
		a.mu.Unlock()
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
