//go:build windows

// Package piv is the hardware-token layer: keystore.Token over a YubiKey's
// PIV application, and what enrolling one needs — finding the key a slot
// record names, generating one, reading the management key the token keeps
// PIN-protected. It is built on github.com/go-piv/piv-go, which speaks to
// winscard.dll without cgo, plus a PC/SC layer of its own for the two
// things piv-go cannot do: probe a reader before connecting, and reset the
// card afterwards.
//
// The package builds only on Windows, and so must every package that
// imports it (piv-go needs cgo elsewhere, and this product is Windows-only):
// an untagged importer fails to build under GOOS=linux with "build
// constraints exclude all Go files", whereas an all-tagged package is
// skipped by ./... patterns.
//
// # What it may do to a token, and what it may not
//
// Key operations — generation and key agreement — target slot 9d and the
// retired slots 82–95, and nothing else. 9a, 9c and 9e (which on the user's
// own token holds a BitLocker key) are refused by every method. The
// package's other traffic, all read-only and none of it a key slot of the
// user's: the PIN (VERIFY, and the empty VERIFY that reads the retries),
// the management-key authentication piv-go performs inside Generate (GET
// METADATA and two GENERAL AUTHENTICATE halves naming the management key
// object 9b), the PRINTED object that ykman stores the management key in,
// the certificate objects of allowlisted slots (occupancy), the ATTEST
// instruction — signed by the token's attestation key f9 — and f9's
// certificate object, which verifies it.
//
// Never: PIV reset, PIN or PUK changes, unblocking, management-key changes,
// certificate or key import. They are not wrapped and cannot be reached. A
// slot that holds a key or a certificate is never overwritten unless the
// caller names that slot with Overwrite. No PIN is ever tried by the
// package; each comes from the caller's Prompter, one attempt per
// operation, and the retries are read and shown first. Keys the package
// generates have touch policy always; keys it reuses must have it too
// (KeyInfo.Usable): the card never releases a key without the user's hand
// on it.
//
// # The ceremony
//
// Token.ECDH is where the user meets the token (DESIGN.md §10). The card is
// asked whether it is already PIN-verified and how many retries remain —
// an empty VERIFY, free of cost. If the key's policy needs it, the Prompter
// is asked for the PIN with that status, the package verifies it, and a
// wrong PIN comes back as *PINError with the count, with no second attempt
// and no touch prompt having appeared. Then the Prompter is told to show
// "touch the key now", and the token computes the agreement. piv-go is told
// the key needs no PIN, so it never prompts or verifies on its own.
//
// # Sessions: two mechanisms, the policy still open
//
// How long a verification stands and whether the app holds the card for
// the session are the user's decision once the unlock UI exists (SCOPE.md).
// The package provides both mechanisms and decides neither. A Card is an
// exclusive connection: holding it open holds the card for the session,
// and every other program is refused meanwhile. Close resets the card
// whenever a PIN went through it or a verified state was used, through
// the package's own connection, so the state Windows would otherwise keep
// for 10 s after the last disconnect (DESIGN.md §11 trap 14) does not
// survive the Card. Open resets it too, so a Card starts unverified
// whatever any program left behind.
//
// # Secrets
//
// A PIN is a Go string from the Prompter; it cannot be zeroed, and piv-go
// pads a copy of it for the wire that is garbage afterwards. The package
// never logs or retains one. A management key read from the token is
// cloned for the caller and zeroed in piv-go's decoded response; the one
// handed to Generate is the caller's, and piv-go's AES key schedule of it
// cannot be reached. The ECDH result is piv-go's own buffer, returned as
// is, so the keystore's zeroing reaches it; piv-go's transmit buffers are
// heap copies that cannot be.
//
// # Testing
//
// Unit tests run against a fake device and never open a card. They see
// exactly what crosses the device interface: which slots the package
// names, the order of prompt, VERIFY and touch, the no-retry rule, the
// error mapping. What only the hardware can show — piv-go's 9b traffic,
// management-key algorithm detection, the real PC/SC and status-word
// texts, attestation against Yubico's roots, the reset — is covered by the
// tests behind ENFOLD_PIV_HW=1, which are read-only unless
// ENFOLD_PIV_HW_WRITE=1 also allows one generation into an empty 9d, and
// by tools/pivtool for the steps that need the user's PIN.
package piv
