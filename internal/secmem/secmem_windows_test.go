//go:build windows

package secmem

import "testing"

// TestThreeSecretsLock is the measurement SCOPE.md's line rests on: the three
// retained secrets are one page each, and the default minimum working set (50
// pages) leaves VirtualLock room for them with no SetProcessWorkingSetSize.
func TestThreeSecretsLock(t *testing.T) {
	var bufs []*Buffer
	for range 3 {
		b := New(32)
		defer b.Free()
		bufs = append(bufs, b)
	}
	for i, b := range bufs {
		if !b.Locked() {
			t.Fatalf("secret %d of 3 is not locked: the working set refused three pages", i+1)
		}
	}
}
