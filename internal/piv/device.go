//go:build windows

package piv

import (
	"crypto"
	"crypto/ecdh"
	"crypto/x509"

	pivgo "github.com/go-piv/piv-go/v2/piv"
)

// device is the slice of piv-go the package uses — exactly these methods
// and no other, so that what the package can do to a token is listed in one
// place, and so that tests can stand in a fake. *pivgo.YubiKey satisfies it.
// Reset, SetPIN, SetPUK, Unblock, SetManagementKey, SetRetries, SetMetadata,
// SetCertificate and SetPrivateKeyInsecure are not here and cannot be
// reached through the package.
type device interface {
	Version() pivgo.Version
	Serial() (uint32, error)
	// Retries sends an empty VERIFY: the retries left on an unverified
	// card, an error on a verified one (piv-go reports 9000 as "expected
	// error code from empty pin"). Consumes no retry.
	Retries() (int, error)
	VerifyPIN(pin string) error
	KeyInfo(slot pivgo.Slot) (pivgo.KeyInfo, error)
	Certificate(slot pivgo.Slot) (*x509.Certificate, error)
	Attest(slot pivgo.Slot) (*x509.Certificate, error)
	AttestationCertificate() (*x509.Certificate, error)
	GenerateKey(key []byte, slot pivgo.Slot, opts pivgo.Key) (crypto.PublicKey, error)
	PrivateKey(slot pivgo.Slot, public crypto.PublicKey, auth pivgo.KeyAuth) (crypto.PrivateKey, error)
	Metadata(pin string) (*pivgo.Metadata, error)
	Close() error
}

var _ device = (*pivgo.YubiKey)(nil)

// ecdher is what PrivateKey must return for a P-256 slot:
// *pivgo.ECDSAPrivateKey has it, and so does the test fake.
type ecdher interface {
	ECDH(peer *ecdh.PublicKey) ([]byte, error)
}

// The seams tests replace. Production uses winscard directly for the probe
// and the reset, and piv-go for everything in between.
var (
	listReaders  = readersReal
	preflight    = preflightReal
	openDevice   = func(reader string) (device, error) { return pivgo.Open(reader) }
	prepareReset = prepareResetReal
)
