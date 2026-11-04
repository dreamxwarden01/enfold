package archive

import (
	"testing"
	"time"
)

// SetNow puts its clock in place of the wall clock for every timestamp the
// archive writes, a file's modified_at among them (R32: the time of the add,
// never the source file's), and its restore puts back exactly the clock it
// replaced, so that nested settings unwind and the last one leaves the wall
// clock again.
func TestSetNowReplacesTheClockAndRestores(t *testing.T) {
	a, _ := newFixture(t, Options{})
	const first, second = int64(1_700_000_000), int64(1_800_000_000)

	restoreFirst := SetNow(func() int64 { return first })
	if got := add(t, a, root, "one.txt", []byte("one")).ModifiedAt; got != first {
		t.Fatalf("under the first clock: modified_at %d, want %d", got, first)
	}
	restoreSecond := SetNow(func() int64 { return second })
	if got := add(t, a, root, "two.txt", []byte("two")).ModifiedAt; got != second {
		t.Fatalf("under the second clock: modified_at %d, want %d", got, second)
	}
	restoreSecond()
	if got := now(); got != first {
		t.Fatalf("the second restore left %d, want the first clock's %d", got, first)
	}
	restoreFirst()
	if clock != nil {
		t.Fatal("the last restore left a clock set; outside a test it is nil")
	}
	before := time.Now().Unix()
	got := add(t, a, root, "three.txt", []byte("three")).ModifiedAt
	if after := time.Now().Unix(); got < before || got > after {
		t.Fatalf("back on the wall clock: modified_at %d, want within [%d, %d]", got, before, after)
	}
}
