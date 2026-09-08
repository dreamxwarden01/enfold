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
	e := SecretAAD(fill16(0x55), SecretRecoveryEscrow, fill16(0xE2))
	if len(e) != 53 || !bytes.HasPrefix(e, []byte("Enfold/v1/aad/secret")) || e[20] != 0x55 || e[36] != 1 || e[37] != 0xE2 {
		t.Fatalf("secret AAD %x", e)
	}
	// The domains never collide, even with identical identities.
	if bytes.Equal(ArchiveKeyAAD(fill16(1), fill16(1))[:17], DEKAAD(fill16(1), fill16(1), 0)[:17]) {
		t.Fatal("domains collide")
	}
	if bytes.Equal(IdentityKeyAAD(fill16(1), fill16(1))[:20], SecretAAD(fill16(1), SecretEntangledKey, [16]byte{})[:20]) {
		t.Fatal("identity and secret domains collide")
	}
}

// TestSecretAADPinned pins the 53 bytes of §7.6 by offset: the 20-byte ASCII
// prefix, vault_id at 20, kind at 36, id at 37. Two records that differ only
// in kind, or only in id, must not share an AAD — that is what stops an
// escrowed recovery key being opened as K_P inside one authenticated registry.
func TestSecretAADPinned(t *testing.T) {
	vault := fill16(0x55)
	id := fill16(0xE2)
	a := SecretAAD(vault, SecretRecoveryEscrow, id)
	if len(a) != 53 {
		t.Fatalf("secret AAD is %d bytes, want 53", len(a))
	}
	if string(a[:20]) != "Enfold/v1/aad/secret" {
		t.Fatalf("prefix %q", a[:20])
	}
	if !bytes.Equal(a[20:36], vault[:]) {
		t.Fatalf("vault_id not at 20: %x", a[20:36])
	}
	if a[36] != byte(SecretRecoveryEscrow) {
		t.Fatalf("kind not at 36: %d", a[36])
	}
	if !bytes.Equal(a[37:53], id[:]) {
		t.Fatalf("id not at 37: %x", a[37:])
	}
	for _, k := range []SecretKind{SecretEntangledKey, SecretVMKHistory} {
		if bytes.Equal(a, SecretAAD(vault, k, id)) {
			t.Fatalf("kind %d shares an AAD with kind 1", k)
		}
	}
	if bytes.Equal(a, SecretAAD(vault, SecretRecoveryEscrow, fill16(0xE3))) {
		t.Fatal("two ids share an AAD")
	}
	if bytes.Equal(a, SecretAAD(fill16(0x56), SecretRecoveryEscrow, id)) {
		t.Fatal("two vaults share an AAD")
	}
}
