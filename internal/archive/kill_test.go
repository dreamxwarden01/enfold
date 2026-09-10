package archive

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

// Archive.Close kills every Reader from another goroutine while the
// Reader's own goroutine may be inside Read (the outside review of
// 2026-09-10, finding 4). The kill takes the Reader's mutex before it closes
// the decoders, so a Read in progress finishes on the live decoder and the
// next one fails with ErrClosed: an error, never a panic on the nil the
// closed decoder leaves behind. Run under -race.
func TestCloseUnderAReadEndsItWithAnErrorNeverAPanic(t *testing.T) {
	a, fx := newFixture(t, Options{})
	data := text(3<<20, 11)
	info := add(t, a, root, "big.txt", data)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	var early int
	for i := 0; i < 50; i++ {
		a := fx.open(t)
		r, err := a.OpenReader(info.ID)
		if err != nil {
			t.Fatalf("round %d: open reader: %v", i, err)
		}
		if r.cr == nil {
			t.Fatalf("round %d: the file is not compressed, which is what this proves", i)
		}
		ended := make(chan error, 1)
		go func() {
			buf := make([]byte, 32<<10)
			var got bytes.Buffer
			for {
				n, err := r.Read(buf)
				got.Write(buf[:n])
				if err != nil {
					if errors.Is(err, io.EOF) && !bytes.Equal(got.Bytes(), data) {
						err = errors.New("EOF with the wrong bytes")
					}
					ended <- err
					return
				}
			}
		}()
		// A different moment each round, from before the first chunk to a
		// good way into the file.
		time.Sleep(time.Duration(i%10) * 200 * time.Microsecond)
		if err := a.Close(); err != nil {
			t.Fatalf("round %d: close: %v", i, err)
		}
		select {
		case err := <-ended:
			switch {
			case errors.Is(err, io.EOF):
				early++ // the read finished before the close landed
			case errors.Is(err, ErrClosed):
			default:
				t.Fatalf("round %d: the read under the close ended with %v", i, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("round %d: the read never ended after the close", i)
		}
		if _, err := r.Read(make([]byte, 16)); !errors.Is(err, ErrClosed) {
			t.Fatalf("round %d: a read after the close: %v", i, err)
		}
		if err := r.Close(); err != nil {
			t.Fatalf("round %d: reader close after the archive's: %v", i, err)
		}
	}
	if early == 50 {
		t.Fatal("every read finished before its close landed: nothing was proved")
	}
	t.Logf("%d of 50 reads finished before the close landed", early)
}

// DropReaders is the kill without the close: every open Reader fails from
// then on, what it had decrypted is gone, and the handle itself stays open
// — a new Reader is served, the index answers — until Close. A killed Reader
// still owns its extent until its own Close, as any other.
func TestDropReadersKillsTheReadersAndKeepsTheHandle(t *testing.T) {
	a, _ := newFixture(t, Options{})
	data := text(64<<10, 12)
	info := add(t, a, root, "f.txt", data)
	r, err := a.OpenReader(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(make([]byte, 1024)); err != nil {
		t.Fatalf("read before the drop: %v", err)
	}
	a.DropReaders()
	if _, err := r.Read(make([]byte, 1024)); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after the drop: %v", err)
	}
	if _, err := r.Seek(0, io.SeekStart); !errors.Is(err, ErrClosed) {
		t.Fatalf("seek after the drop: %v", err)
	}
	a.mu.Lock()
	held := a.held[r.e]
	a.mu.Unlock()
	if held != 1 {
		t.Fatalf("the killed reader's extent is held %d times, want 1 until its own Close", held)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("closing the killed reader: %v", err)
	}
	a.mu.Lock()
	held = a.held[r.e]
	a.mu.Unlock()
	if held != 0 {
		t.Fatalf("the extent is still held %d times after the reader's Close", held)
	}
	// The handle is open: it lists and serves as before.
	if got := a.Files(); len(got) != 1 {
		t.Fatalf("files after the drop: %d", len(got))
	}
	var out bytes.Buffer
	if err := a.Extract(ctx, info.ID, &out); err != nil {
		t.Fatalf("extract after the drop: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatal("the bytes after the drop are not the file")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
}
