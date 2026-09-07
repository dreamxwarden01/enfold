//go:build windows

package keystore

import (
	"errors"
	"path/filepath"
	"testing"
)

// The OS lock, not the in-process table, is what refuses a second process
// (the single-instance guard is per logon session). A `\\?\`-spelled path
// canonicalises differently, so the table lets it through and only
// LockFileEx can say no.
func TestOSLockRefusesAnotherHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.eks")
	rk := recoveryKey(t)
	u := mustCreate(t, path, PasswordSlot{Password: "p", Argon2: fast}, RecoverySlot{Key: rk})
	defer u.k.Close()
	defer u.Close()
	alt := `\\?\` + path
	if canonical(alt) == canonical(path) {
		t.Skip("the two spellings canonicalise alike; the table would answer first")
	}
	if _, err := Open(alt); !errors.Is(err, ErrBusy) {
		t.Fatalf("second handle through the OS lock: %v", err)
	}
}
