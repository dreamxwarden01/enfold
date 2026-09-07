package format

import (
	"bytes"
	"testing"
)

func TestWrapAADs(t *testing.T) {
	a := ArchiveKeyAAD(fill16(0xA1), fill16(0xB2))
	if len(a) != 25+32 || !bytes.HasPrefix(a, []byte("Enfold/v1/aad/archive-key")) || a[25] != 0xA1 || a[41] != 0xB2 {
		t.Fatalf("archive key AAD %x", a)
	}
	d := DEKAAD(fill16(0xA1), fill16(0xC3), 0x01020304)
	if len(d) != 17+32+4 || !bytes.HasPrefix(d, []byte("Enfold/v1/aad/dek")) || d[17] != 0xA1 || d[33] != 0xC3 ||
		!bytes.Equal(d[49:], []byte{0x04, 0x03, 0x02, 0x01}) {
		t.Fatalf("DEK AAD %x", d)
	}
	i := IdentityKeyAAD(fill16(0x55), fill16(0xD1))
	if len(i) != 22+32 || !bytes.HasPrefix(i, []byte("Enfold/v1/aad/identity")) || i[22] != 0x55 || i[38] != 0xD1 {
		t.Fatalf("identity AAD %x", i)
	}
	e := RecoveryEscrowAAD(fill16(0x55), fill16(0xE2))
	if len(e) != 29+32 || !bytes.HasPrefix(e, []byte("Enfold/v1/aad/recovery-escrow")) || e[29] != 0x55 || e[45] != 0xE2 {
		t.Fatalf("recovery escrow AAD %x", e)
	}
	// The domains never collide, even with identical identities.
	if bytes.Equal(ArchiveKeyAAD(fill16(1), fill16(1))[:17], DEKAAD(fill16(1), fill16(1), 0)[:17]) {
		t.Fatal("domains collide")
	}
	if bytes.Equal(IdentityKeyAAD(fill16(1), fill16(1))[:22], RecoveryEscrowAAD(fill16(1), fill16(1))[:22]) {
		t.Fatal("identity and escrow domains collide")
	}
}
