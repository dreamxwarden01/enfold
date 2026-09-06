//go:build windows

package piv

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	pivgo "github.com/go-piv/piv-go/v2/piv"

	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// Version is the token's firmware version.
type Version struct {
	Major, Minor, Patch int
}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

func (v Version) atLeast(major, minor int) bool {
	return v.Major > major || (v.Major == major && v.Minor >= minor)
}

// PINStatus is the card's answer to an empty VERIFY, which consumes no
// retry and needs no touch.
type PINStatus struct {
	// Verified: the card is already PIN-verified. The retry count cannot be
	// read in this state (the card answers 9000, not a count); it is at the
	// configured maximum, because a correct PIN restores it. A prompt must
	// then say the count is unreadable, never "0".
	Verified bool
	// Retries left before the PIN blocks; meaningful only when RetriesKnown.
	Retries int
	// RetriesKnown is false exactly when Verified is true.
	RetriesKnown bool
}

// Blocked: no retries remain; a prompt cannot succeed.
func (s PINStatus) Blocked() bool { return s.RetriesKnown && s.Retries == 0 }

// Card is an exclusive connection to one token's PIV application. While
// open, no other program can use the card — that is what makes "hold the
// connection for the session" one of the two session mechanisms (the
// package documentation). Close releases it and, when anything was verified
// through it, resets the card so the PIN-verified state does not outlive
// the Card (DESIGN.md §11 trap 14).
//
// One operation at a time: a second concurrent operation is refused with
// ErrInUse rather than queued. Close waits for an operation in flight.
type Card struct {
	reader  string
	dev     device
	version Version
	serial  uint32

	op sync.Mutex // held for the duration of one operation

	st           sync.Mutex // guards the fields below
	closed       bool
	dirty        dirtyReason // what this Card left on the card, to reset on Close
	verifiedHere bool        // this Card sent the VERIFY that verified the card
	resetFailed  bool
}

// dirtyReason is what a Card may have left on the card that a Close must
// clear: a PIN verification, or the management-key authentication piv-go
// performs inside Generate. Both are card state that survives a plain
// disconnect; only the first can be asked about afterwards.
type dirtyReason uint8

const (
	dirtyPIN  dirtyReason = 1 << iota // a VERIFY was sent, or a verified state was used
	dirtyMgmt                         // the management key authenticated
)

// Readers lists the PC/SC readers whose name says YubiKey. Nothing attached
// is an empty list; ErrNoService when the Smart Card service is down.
func Readers() ([]string, error) {
	return listReaders()
}

// Open connects to the token in the reader. The reader is probed first over
// the package's own PC/SC connection — SELECT PIV, GET VERSION, then a
// disconnect that resets the card — so a missing PIV application, a busy
// card or an old firmware is reported (ErrNoPIVApplet, ErrBusy, ErrNoCard,
// ErrUnsupported) before piv-go connects, whose Open would leak an
// exclusive connection on those failures. The Card starts unverified.
func Open(reader string) (*Card, error) {
	v, err := preflight(reader)
	if err != nil {
		return nil, err
	}
	if !v.atLeast(5, 3) {
		return nil, fmt.Errorf("%w: firmware %s is older than 5.3", ErrUnsupported, v)
	}
	dev, err := openDevice(reader)
	if err != nil {
		return nil, mapErr(err)
	}
	ok := false
	defer func() {
		if !ok {
			dev.Close()
		}
	}()
	if pv := dev.Version(); (Version{pv.Major, pv.Minor, pv.Patch}) != v {
		return nil, fmt.Errorf("%w: the card changed while opening", ErrNoCard)
	}
	serial, err := dev.Serial()
	if err != nil {
		return nil, mapErr(err)
	}
	ok = true
	return &Card{reader: reader, dev: dev, version: v, serial: serial}, nil
}

// Reader is the PC/SC reader name the Card was opened on.
func (c *Card) Reader() string { return c.reader }

// Serial is the token's serial number.
func (c *Card) Serial() uint32 { return c.serial }

// Version is the token's firmware version.
func (c *Card) Version() Version { return c.version }

// ResetFailed reports whether Close could not reset a card it had verified,
// so that a deferred Close cannot lose the fact (ErrResetFailed).
func (c *Card) ResetFailed() bool {
	c.st.Lock()
	defer c.st.Unlock()
	return c.resetFailed
}

// acquire takes the operation lock, refusing rather than waiting, and
// refuses a closed Card.
func (c *Card) acquire() (func(), error) {
	if !c.op.TryLock() {
		return nil, ErrInUse
	}
	c.st.Lock()
	closed := c.closed
	c.st.Unlock()
	if closed {
		c.op.Unlock()
		return nil, ErrClosed
	}
	return c.op.Unlock, nil
}

func (c *Card) markDirty(r dirtyReason) {
	c.st.Lock()
	c.dirty |= r
	c.st.Unlock()
}

// markVerifiedByUs records that this Card's own VERIFY succeeded, which is
// the only verified state a Token trusts.
func (c *Card) markVerifiedByUs() {
	c.st.Lock()
	c.verifiedHere = true
	c.st.Unlock()
}

func (c *Card) verifiedByUs() bool {
	c.st.Lock()
	defer c.st.Unlock()
	return c.verifiedHere
}

func (c *Card) isClosed() bool {
	c.st.Lock()
	defer c.st.Unlock()
	return c.closed
}

// Close releases the card. If a PIN was sent through this Card, a verified
// state was used, or the management key authenticated, the card is reset
// afterwards through the package's own connection; ErrResetFailed when it
// could not be and the card may still hold that state. Idempotent; a
// second call does nothing. Waits for an operation in flight — one blocked
// in a PIN prompt included — and decides on the reset only after it, so a
// PIN verified while Close was waiting is reset away too.
func (c *Card) Close() error {
	c.st.Lock()
	if c.closed {
		c.st.Unlock()
		return nil
	}
	c.closed = true // from here acquire refuses new operations
	c.st.Unlock()
	c.op.Lock() // the operation in flight, if any
	defer c.op.Unlock()
	// Only now is dirty final: the operation that just finished may have
	// verified a PIN after Close was called.
	c.st.Lock()
	dirty := c.dirty
	c.st.Unlock()
	if dirty == 0 {
		return mapErr(c.dev.Close())
	}
	// The reset connection is prepared before piv-go lets go of the card,
	// so that only a connect and a disconnect sit in the gap.
	reset := prepareReset(c.reader)
	closeErr := c.dev.Close()
	done, verified, err := reset()
	switch {
	case done:
		return mapErr(closeErr)
	case verified:
		// Still PIN-verified, or unknown: the caller warns.
	case dirty&dirtyMgmt != 0:
		// No PIN is left, but whether the management key is still
		// authenticated cannot be asked; assume it is.
		err = fmt.Errorf("%w: the management key may still be authenticated", ErrResetFailed)
	default:
		return mapErr(closeErr) // nothing of ours is left behind
	}
	c.st.Lock()
	c.resetFailed = true
	c.st.Unlock()
	if err == nil {
		err = ErrResetFailed
	}
	return err
}

// PINState asks the card whether it is verified and how many retries are
// left. No retry is consumed and no touch is needed.
func (c *Card) PINState() (PINStatus, error) {
	release, err := c.acquire()
	if err != nil {
		return PINStatus{}, err
	}
	defer release()
	return c.pinState()
}

func (c *Card) pinState() (PINStatus, error) {
	n, err := c.dev.Retries()
	if err == nil {
		return PINStatus{Retries: n, RetriesKnown: true}, nil
	}
	if strings.Contains(err.Error(), "expected error code from empty pin") {
		// The card answered 9000: verified. Whoever did that, the state is
		// now something Close must clear.
		c.markDirty(dirtyPIN)
		return PINStatus{Verified: true}, nil
	}
	return PINStatus{}, mapErr(err)
}

// Inspect describes what the slot holds. ErrEmpty when nothing does;
// ErrForbiddenSlot outside the allowlist.
func (c *Card) Inspect(slot Slot) (KeyInfo, error) {
	release, err := c.acquire()
	if err != nil {
		return KeyInfo{}, err
	}
	defer release()
	return c.inspect(slot)
}

func (c *Card) inspect(slot Slot) (KeyInfo, error) {
	ps, err := slot.pivSlot()
	if err != nil {
		return KeyInfo{}, err
	}
	info := KeyInfo{Slot: slot}
	ki, err := c.dev.KeyInfo(ps)
	hasKey := err == nil
	if err != nil && !errors.Is(err, pivgo.ErrNotFound) {
		return KeyInfo{}, mapErr(err)
	}
	if hasKey {
		info.Algorithm = algorithms[ki.Algorithm]
		info.PINPolicy = pinPolicies[ki.PINPolicy]
		info.TouchPolicy = touchPolicies[ki.TouchPolicy]
		info.Origin = origins[ki.Origin]
		if pub, ok := ki.PublicKey.(*ecdsa.PublicKey); ok && info.Algorithm == AlgorithmP256 && pub.Curve == elliptic.P256() {
			if ecdhPub, err := pub.ECDH(); err == nil {
				info.PublicKey = ecdhPub.Bytes()
				info.ecdsaPub = pub
			}
		}
	}
	// A slot can hold a certificate object without a key (another program's
	// provisioning); it is occupied then. Only "not found" means absent —
	// a certificate piv-go cannot parse is still there.
	if _, cerr := c.dev.Certificate(ps); cerr == nil {
		info.Certificate = true
	} else if !errors.Is(cerr, pivgo.ErrNotFound) {
		if m := mapErr(cerr); isTransport(m) {
			return KeyInfo{}, m
		}
		info.Certificate = true
	}
	if !hasKey && !info.Certificate {
		return KeyInfo{}, ErrEmpty
	}
	return info, nil
}

// Keys lists every allowlisted slot that holds a key or a certificate.
// Metadata only: no PIN, no touch.
func (c *Card) Keys() ([]KeyInfo, error) {
	release, err := c.acquire()
	if err != nil {
		return nil, err
	}
	defer release()
	return c.keys()
}

func (c *Card) keys() ([]KeyInfo, error) {
	var out []KeyInfo
	for _, s := range AllSlots() {
		info, err := c.inspect(s)
		if errors.Is(err, ErrEmpty) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

// Find is the slot holding this public key — the keystore's slot_pubkey.
// ErrNoKey when none does. Metadata only: no PIN, no touch.
func (c *Card) Find(pub []byte) (KeyInfo, error) {
	release, err := c.acquire()
	if err != nil {
		return KeyInfo{}, err
	}
	defer release()
	return c.find(pub)
}

func (c *Card) find(pub []byte) (KeyInfo, error) {
	if len(pub) != 65 {
		return KeyInfo{}, fmt.Errorf("%w: public key of %d bytes", ErrParams, len(pub))
	}
	keys, err := c.keys()
	if err != nil {
		return KeyInfo{}, err
	}
	for _, k := range keys {
		if bytes.Equal(k.PublicKey, pub) {
			return k, nil
		}
	}
	return KeyInfo{}, ErrNoKey
}

// FirstEmptySlot is where Generate goes when the caller has no preference:
// 9d if empty, else the lowest empty retired slot. ErrFull when every slot
// is occupied. Metadata only.
func (c *Card) FirstEmptySlot() (Slot, error) {
	release, err := c.acquire()
	if err != nil {
		return 0, err
	}
	defer release()
	for _, s := range AllSlots() {
		_, err := c.inspect(s)
		if errors.Is(err, ErrEmpty) {
			return s, nil
		}
		if err != nil {
			return 0, err
		}
	}
	return 0, ErrFull
}

// DefaultManagementKey is the factory management key, a fresh copy each
// call so that the caller may zero it. Trying it is the caller's decision;
// if it works, the user must be warned that anyone holding it can rewrite
// the token's slots.
func DefaultManagementKey() []byte {
	return bytes.Clone(pivgo.DefaultManagementKey)
}

// checkPIN refuses what the card would refuse, before anything is sent: an
// empty PIN, or more than 8 bytes.
func checkPIN(pin string) error {
	if pin == "" {
		return fmt.Errorf("%w: empty PIN", ErrParams)
	}
	if len(pin) > 8 {
		return fmt.Errorf("%w: PIN longer than 8 bytes", ErrParams)
	}
	return nil
}

// ProtectedManagementKey reads the management key ykman stores PIN-protected
// on the token (the PRINTED object). One real VERIFY: a wrong PIN costs a
// retry (*PINError), so show PINState first. ErrNoProtectedKey when the
// token holds none. The returned slice is the caller's to zero; piv-go's
// own copy is zeroed here.
func (c *Card) ProtectedManagementKey(pin string) ([]byte, error) {
	if err := checkPIN(pin); err != nil {
		return nil, err
	}
	release, err := c.acquire()
	if err != nil {
		return nil, err
	}
	defer release()
	c.markDirty(dirtyPIN)
	m, err := c.dev.Metadata(pin)
	if err != nil {
		return nil, mapErr(err)
	}
	c.markVerifiedByUs()
	if m == nil || m.ManagementKey == nil || len(*m.ManagementKey) == 0 {
		return nil, ErrNoProtectedKey
	}
	mk := bytes.Clone(*m.ManagementKey)
	kdf.Zero(*m.ManagementKey)
	return mk, nil
}

// GenerateOptions says where and how Generate makes a key.
type GenerateOptions struct {
	// Slot must be one the package may use (FirstEmptySlot picks one). No
	// default: the one call that can destroy a key names its target.
	Slot Slot
	// PINPolicy: once (the zero value) or always. Never and the fingerprint
	// policies are refused, as Usable would refuse the result.
	PINPolicy PINPolicy
	// Overwrite is required when Slot is occupied. The UI makes that an
	// explicit, second-confirmed action naming the slot and what it holds.
	Overwrite bool
}

// Generate makes a P-256 key with touch policy always. Never overwrites
// without Overwrite (*OccupiedError says what is there). ErrManagementKey
// when the key is refused — the key's algorithm is read from the token
// (3DES 24 bytes, AES 16/24/32), and a wrong length is refused before any
// APDU; ErrNoCard or ErrBusy when the card goes away during the
// management-key handshake. mgmtKey is the caller's to zero.
func (c *Card) Generate(mgmtKey []byte, o GenerateOptions) (KeyInfo, error) {
	ps, err := o.Slot.pivSlot()
	if err != nil {
		return KeyInfo{}, err
	}
	pp := o.PINPolicy
	if pp == PINPolicyUnknown {
		pp = PINPolicyOnce
	}
	pivPP, ok := pinPoliciesOut[pp]
	if !ok {
		return KeyInfo{}, fmt.Errorf("%w: PIN policy %s", ErrParams, pp)
	}
	switch len(mgmtKey) {
	case 16, 24, 32:
	default:
		return KeyInfo{}, fmt.Errorf("%w: management key of %d bytes", ErrParams, len(mgmtKey))
	}
	release, err := c.acquire()
	if err != nil {
		return KeyInfo{}, err
	}
	defer release()
	existing, err := c.inspect(o.Slot)
	switch {
	case err == nil && !o.Overwrite:
		return KeyInfo{}, &OccupiedError{Key: existing}
	case err != nil && !errors.Is(err, ErrEmpty):
		return KeyInfo{}, err
	}
	c.markDirty(dirtyMgmt)
	if _, err := c.dev.GenerateKey(mgmtKey, ps, pivgo.Key{Algorithm: pivgo.AlgorithmEC256, PINPolicy: pivPP, TouchPolicy: pivgo.TouchPolicyAlways}); err != nil {
		// piv-go wraps every failure of its management-key authentication —
		// the card going away included — under one prefix (v2.6.0
		// key.go:970 over piv.go:415/448/509), so the transport is
		// classified first, or a pulled token reads as a refused key. The
		// prefix test stays on the original text: mapErr's AuthErr arm
		// returns a *PINError that does not carry it.
		m := mapErr(err)
		if isTransport(m) {
			return KeyInfo{}, m
		}
		if strings.Contains(err.Error(), "authenticating with management key") {
			return KeyInfo{}, fmt.Errorf("%w: %v", ErrManagementKey, err)
		}
		return KeyInfo{}, m
	}
	info, err := c.inspect(o.Slot)
	if err != nil {
		return KeyInfo{}, err
	}
	if !info.Usable() {
		return info, fmt.Errorf("%w: the generated key reads back as unusable: %s", ErrUnsupported, info.WhyNotUsable())
	}
	return info, nil
}

// Attestation is a verified statement, signed through Yubico's roots, that
// a key was generated on this token with these policies.
type Attestation struct {
	Slot        Slot
	Version     Version
	Serial      uint32
	PINPolicy   PINPolicy
	TouchPolicy TouchPolicy
	FormFactor  string
}

// Attest asks the token for the slot's attestation and verifies it against
// the token's attestation certificate and the roots piv-go embeds. Reads
// the F9 attestation certificate object; no PIN, no touch, no retry. The
// result can be checked against Inspect and Serial — a card whose metadata
// lies would show here. ErrAttestation when the chain does not verify;
// ErrEmpty when the slot has no key.
func (c *Card) Attest(slot Slot) (*Attestation, error) {
	ps, err := slot.pivSlot()
	if err != nil {
		return nil, err
	}
	release, err := c.acquire()
	if err != nil {
		return nil, err
	}
	defer release()
	slotCert, err := c.dev.Attest(ps)
	if err != nil {
		if errors.Is(err, pivgo.ErrNotFound) {
			return nil, ErrEmpty
		}
		return nil, mapErr(err)
	}
	attCert, err := c.dev.AttestationCertificate()
	if err != nil {
		return nil, mapErr(err)
	}
	a, err := pivgo.Verify(attCert, slotCert)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAttestation, err)
	}
	out := &Attestation{
		Slot:        slot,
		Version:     Version{a.Version.Major, a.Version.Minor, a.Version.Patch},
		Serial:      a.Serial,
		PINPolicy:   pinPolicies[a.PINPolicy],
		TouchPolicy: touchPolicies[a.TouchPolicy],
		FormFactor:  a.Formfactor.String(),
	}
	// The attested slot comes from the certificate's common name; piv-go
	// leaves it empty when it cannot tell.
	if a.Slot.Key != 0 && a.Slot.Key != ps.Key {
		return nil, fmt.Errorf("%w: attests slot %x, asked for %s", ErrAttestation, a.Slot.Key, slot)
	}
	return out, nil
}

// Token binds this Card, the slot holding pub, and the prompter that runs
// the ceremony. It implements keystore.Token and does not outlive the
// Card. ErrNoKey when no slot holds pub; ErrNotUsable when the key's
// policies are not the ones the keystore may rely on.
func (c *Card) Token(pub []byte, p Prompter) (*Token, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: nil prompter", ErrParams)
	}
	release, err := c.acquire()
	if err != nil {
		return nil, err
	}
	defer release()
	info, err := c.find(pub)
	if err != nil {
		return nil, err
	}
	if !info.Usable() {
		return nil, fmt.Errorf("%w: %s", ErrNotUsable, info.WhyNotUsable())
	}
	return &Token{c: c, info: info, p: p}, nil
}

// Error mapping. piv-go's PC/SC and APDU error types are unexported, so
// what reaches this package is their text; the texts are pinned with the
// library version in go.mod and checked against the real card by the
// hardware tests.

var statusRe = regexp.MustCompile(`smart card error ([0-9a-f]{4})`)

// statusWord extracts the card's status word from a piv-go error.
func statusWord(err error) (uint16, bool) {
	if err == nil {
		return 0, false
	}
	m := statusRe.FindStringSubmatch(err.Error())
	if m == nil {
		return 0, false
	}
	var sw uint16
	fmt.Sscanf(m[1], "%04x", &sw)
	return sw, true
}

var sentinels = []error{
	ErrNoService, ErrNoReader, ErrNoCard, ErrBusy, ErrInUse, ErrNoPIVApplet, ErrUnsupported, ErrClosed,
	ErrForbiddenSlot, ErrEmpty, ErrOccupied, ErrFull, ErrNoKey, ErrNotUsable, ErrNoProtectedKey,
	ErrManagementKey, ErrPINBlocked, ErrPINRequired, ErrTouch, ErrCancelled, ErrTooManyOperations,
	ErrAttestation, ErrResetFailed, ErrParams,
}

// pcscTexts are piv-go's messages for the return codes that matter
// (pcsc_errors.go in v2.6.0).
var pcscTexts = []struct {
	text string
	err  error
}{
	{"other connections outstanding", ErrBusy},
	{"no Smart Card is currently in the device", ErrNoCard},
	{"the smart card has been removed", ErrNoCard},
	{"power has been removed from the smart card", ErrNoCard},
	{"the smart card has been reset", ErrNoCard},
	{"not responding to a reset", ErrNoCard},
	{"resource manager is not running", ErrNoService},
	{"resource manager has shut down", ErrNoService},
	{"cannot find a smart card reader", ErrNoReader},
	{"reader is not currently available", ErrNoReader},
}

// mapErr turns a piv-go error into one of the package's, keeping the
// original text.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	for _, s := range sentinels {
		if errors.Is(err, s) {
			return err
		}
	}
	var pe *PINError
	if errors.As(err, &pe) {
		return err
	}
	var ae pivgo.AuthErr
	if errors.As(err, &ae) {
		if ae.Retries == 0 {
			return fmt.Errorf("%w: %v", ErrPINBlocked, err)
		}
		return &PINError{Retries: ae.Retries}
	}
	msg := err.Error()
	for _, t := range pcscTexts {
		if strings.Contains(msg, t.text) {
			return fmt.Errorf("%w: %v", t.err, err)
		}
	}
	return fmt.Errorf("piv: %w", err)
}

// isTransport: the card or the reader went away, rather than the card
// answering something.
func isTransport(err error) bool {
	return errors.Is(err, ErrNoCard) || errors.Is(err, ErrBusy) || errors.Is(err, ErrNoService) || errors.Is(err, ErrNoReader)
}
