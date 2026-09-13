package secmem

import (
	"bytes"
	"errors"
	"testing"
)

// fill writes a recognisable pattern, so that a later read proves the erasure
// rather than reading memory that was zero all along.
func fill(b []byte) {
	for i := range b {
		b[i] = byte(i) | 0x80
	}
}

func TestAllocZeroFree(t *testing.T) {
	b := New(32)
	defer b.Free()

	s := b.Bytes()
	if len(s) != 32 {
		t.Fatalf("Bytes() is %d bytes, want 32", len(s))
	}
	fill(s)
	if bytes.Equal(s, make([]byte, 32)) {
		t.Fatal("the pattern did not land in the buffer")
	}

	b.Zero()
	if !bytes.Equal(b.Bytes(), make([]byte, 32)) {
		t.Fatalf("Zero left %x", b.Bytes())
	}
	if b.Zeros() != 1 {
		t.Fatalf("Zeros() is %d after one Zero, want 1", b.Zeros())
	}
	// Usable afterwards: Zero erases, it does not release.
	fill(b.Bytes())
	if b.Bytes()[0] == 0 {
		t.Fatal("the buffer is not usable after Zero")
	}

	b.Free()
	if b.Bytes() != nil {
		t.Fatal("Bytes() is not nil after Free")
	}
	if b.Zeros() != 2 {
		t.Fatalf("Zeros() is %d after Zero and Free, want 2", b.Zeros())
	}
	if b.Locked() {
		t.Fatal("Locked() is true after Free")
	}
}

func TestBytesLength(t *testing.T) {
	// The region is whole pages on Windows; what the caller sees is what it
	// asked for.
	for _, n := range []int{1, 16, 32, 4096, 5000} {
		b := New(n)
		if got := len(b.Bytes()); got != n {
			t.Errorf("New(%d).Bytes() is %d bytes", n, got)
		}
		fill(b.Bytes()) // every byte of it is writable
		b.Free()
	}
}

func TestDoubleFree(t *testing.T) {
	b := New(32)
	fill(b.Bytes())
	b.Free()
	zeros := b.Zeros()
	b.Free() // harmless: no second release, no second erasure
	b.Free()
	if b.Zeros() != zeros {
		t.Fatalf("Zeros() moved from %d to %d over two more Frees", zeros, b.Zeros())
	}
	if b.Bytes() != nil {
		t.Fatal("Bytes() is not nil after Free")
	}
	// Zero on a freed buffer is a no-op, not a fault on released pages.
	b.Zero()
}

// TestLockRefusedFallback takes the path a working-set refusal leaves: the
// buffer is unpinned and every other promise holds.
func TestLockRefusedFallback(t *testing.T) {
	oldLock, oldLog := lockPages, Log
	lockPages = func(*region) error { return errors.New("forced refusal") }
	Log = func(string, ...any) {} // the refusal is the test's, not the log's
	defer func() { lockPages, Log = oldLock, oldLog }()

	b := New(32)
	defer b.Free()
	if b.Locked() {
		t.Fatal("Locked() is true after the lock was refused")
	}
	s := b.Bytes()
	if len(s) != 32 {
		t.Fatalf("Bytes() is %d bytes, want 32", len(s))
	}
	fill(s)
	b.Zero()
	if !bytes.Equal(b.Bytes(), make([]byte, 32)) {
		t.Fatalf("Zero left %x on an unpinned buffer", b.Bytes())
	}
}
