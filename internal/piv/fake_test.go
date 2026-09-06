//go:build windows

package piv

import (
	"bytes"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"
	"time"

	pivgo "github.com/go-piv/piv-go/v2/piv"
)

// fakeDevice stands in for piv-go's YubiKey: software keys per slot with
// policies, a PIN with a retry counter and a verified flag, a touch switch,
// and a record of every slot it was asked about. Its errors are shaped
// like piv-go's — the same wrapped types and, where piv-go only has text,
// the same text — because that is what the package parses.
type fakeDevice struct {
	version    pivgo.Version
	serial     uint32
	pin        string
	retries    int
	maxRetries int
	verified   bool // PIN-once state, until reset
	verifiedOp bool // a VERIFY stands for the next operation (policy always)
	mgmtKey    []byte
	protected  []byte // PIN-protected management key, nil when absent
	slots      map[uint32]*fakeSlot
	touch      bool // the user touches when asked

	// Records.
	slotsNamed   []uint32
	verifies     int
	generates    int
	closed       int
	lastAuth     pivgo.KeyAuth
	ecdhCalls    int
	failECDH     error // injected in place of the agreement
	failKeyInfo  error // injected on KeyInfo of any slot
	failCert     error // injected on Certificate of any slot
	failGenerate error // injected in place of the management-key authentication
}

type fakeSlot struct {
	key         *ecdsa.PrivateKey // nil: certificate only
	alg         pivgo.Algorithm
	pinPolicy   pivgo.PINPolicy
	touchPolicy pivgo.TouchPolicy
	origin      pivgo.Origin
	cert        bool
}

func newFake() *fakeDevice {
	return &fakeDevice{
		version: pivgo.Version{Major: 5, Minor: 7, Patch: 4}, serial: 12345678,
		pin: "123456", retries: 3, maxRetries: 3,
		mgmtKey: bytes.Repeat([]byte{7}, 32),
		slots:   map[uint32]*fakeSlot{},
		touch:   true,
	}
}

func (f *fakeDevice) addKey(slot uint32, pp pivgo.PINPolicy, tp pivgo.TouchPolicy) *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	f.slots[slot] = &fakeSlot{key: k, alg: pivgo.AlgorithmEC256, pinPolicy: pp, touchPolicy: tp, origin: pivgo.OriginGenerated}
	return k
}

func pubBytes(k *ecdsa.PrivateKey) []byte {
	p, err := k.PublicKey.ECDH()
	if err != nil {
		panic(err)
	}
	return p.Bytes()
}

// piv-go error shapes.
func notFound(sw string) error {
	return fmt.Errorf("command failed: smart card error %s: %w", sw, pivgo.ErrNotFound)
}

func status(sw string) error {
	return fmt.Errorf("command failed: smart card error %s: security status not satisfied", sw)
}

func (f *fakeDevice) Version() pivgo.Version  { return f.version }
func (f *fakeDevice) Serial() (uint32, error) { return f.serial, nil }

func (f *fakeDevice) Retries() (int, error) {
	if f.verified {
		return 0, fmt.Errorf("expected error code from empty pin")
	}
	return f.retries, nil
}

func (f *fakeDevice) VerifyPIN(pin string) error {
	f.verifies++
	if f.retries == 0 {
		return fmt.Errorf("verify pin: %w", pivgo.AuthErr{Retries: 0})
	}
	if pin != f.pin {
		f.retries--
		return fmt.Errorf("verify pin: %w", pivgo.AuthErr{Retries: f.retries})
	}
	f.retries = f.maxRetries
	f.verified, f.verifiedOp = true, true
	return nil
}

func (f *fakeDevice) KeyInfo(slot pivgo.Slot) (pivgo.KeyInfo, error) {
	f.slotsNamed = append(f.slotsNamed, slot.Key)
	if f.failKeyInfo != nil {
		return pivgo.KeyInfo{}, f.failKeyInfo
	}
	s := f.slots[slot.Key]
	if s == nil || s.key == nil {
		return pivgo.KeyInfo{}, notFound("6a88")
	}
	return pivgo.KeyInfo{Algorithm: s.alg, PINPolicy: s.pinPolicy, TouchPolicy: s.touchPolicy, Origin: s.origin, PublicKey: &s.key.PublicKey}, nil
}

func (f *fakeDevice) Certificate(slot pivgo.Slot) (*x509.Certificate, error) {
	f.slotsNamed = append(f.slotsNamed, slot.Key)
	if f.failCert != nil {
		return nil, f.failCert
	}
	s := f.slots[slot.Key]
	if s == nil || !s.cert {
		return nil, notFound("6a82")
	}
	return &x509.Certificate{}, nil
}

func selfSigned() *x509.Certificate {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		panic(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c
}

func (f *fakeDevice) Attest(slot pivgo.Slot) (*x509.Certificate, error) {
	f.slotsNamed = append(f.slotsNamed, slot.Key)
	if s := f.slots[slot.Key]; s == nil || s.key == nil {
		return nil, pivgo.ErrNotFound
	}
	return selfSigned(), nil
}

func (f *fakeDevice) AttestationCertificate() (*x509.Certificate, error) { return selfSigned(), nil }

func (f *fakeDevice) GenerateKey(key []byte, slot pivgo.Slot, opts pivgo.Key) (crypto.PublicKey, error) {
	f.slotsNamed = append(f.slotsNamed, slot.Key)
	f.generates++
	if f.failGenerate != nil {
		return nil, fmt.Errorf("authenticating with management key: %w", f.failGenerate)
	}
	if !bytes.Equal(key, f.mgmtKey) {
		return nil, fmt.Errorf("authenticating with management key: %w", status("6982"))
	}
	if opts.Algorithm != pivgo.AlgorithmEC256 {
		return nil, fmt.Errorf("unsupported algorithm")
	}
	k := f.addKey(slot.Key, opts.PINPolicy, opts.TouchPolicy)
	return &k.PublicKey, nil
}

type fakePriv struct {
	f    *fakeDevice
	slot pivgo.Slot
	auth pivgo.KeyAuth
}

func (f *fakeDevice) PrivateKey(slot pivgo.Slot, public crypto.PublicKey, auth pivgo.KeyAuth) (crypto.PrivateKey, error) {
	f.slotsNamed = append(f.slotsNamed, slot.Key)
	f.lastAuth = auth
	return &fakePriv{f: f, slot: slot, auth: auth}, nil
}

// ECDH is the card's GENERAL AUTHENTICATE: a PIN policy the current state
// does not satisfy, or a missing touch, is 6982.
func (p *fakePriv) ECDH(peer *ecdh.PublicKey) ([]byte, error) {
	f := p.f
	f.ecdhCalls++
	if f.failECDH != nil {
		return nil, f.failECDH
	}
	s := f.slots[p.slot.Key]
	if s == nil || s.key == nil {
		return nil, notFound("6a88")
	}
	switch s.pinPolicy {
	case pivgo.PINPolicyOnce:
		if !f.verified {
			return nil, status("6982")
		}
	case pivgo.PINPolicyAlways:
		if !f.verifiedOp {
			return nil, status("6982")
		}
		f.verifiedOp = false
	}
	if s.touchPolicy != pivgo.TouchPolicyNever && !f.touch {
		return nil, status("6982")
	}
	priv, err := s.key.ECDH()
	if err != nil {
		return nil, err
	}
	return priv.ECDH(peer)
}

func (f *fakeDevice) Metadata(pin string) (*pivgo.Metadata, error) {
	if err := f.VerifyPIN(pin); err != nil {
		return nil, fmt.Errorf("authenticating with pin: %w", err)
	}
	if f.protected == nil {
		return &pivgo.Metadata{}, nil
	}
	mk := bytes.Clone(f.protected)
	return &pivgo.Metadata{ManagementKey: &mk}, nil
}

func (f *fakeDevice) Close() error {
	f.closed++
	return nil
}

// reset is what the package's reset does to the card's state.
func (f *fakeDevice) reset() { f.verified, f.verifiedOp = false, false }

// namedOutsideAllowlist reports any key slot the package named that is not
// 9d or 82–95.
func (f *fakeDevice) namedOutsideAllowlist() []uint32 {
	var out []uint32
	for _, k := range f.slotsNamed {
		if !Slot(k).Allowed() {
			out = append(out, k)
		}
	}
	return out
}

// testPrompter scripts the ceremony and records what it was shown.
type testPrompter struct {
	pins     []string // answers, in order; an empty list cancels
	statuses []PINStatus
	touches  []TouchRequest
	cancel   error
}

func (p *testPrompter) PIN(st PINStatus) (string, error) {
	p.statuses = append(p.statuses, st)
	if p.cancel != nil {
		return "", p.cancel
	}
	if len(p.pins) == 0 {
		return "", errors.New("no PIN scripted")
	}
	pin := p.pins[0]
	p.pins = p.pins[1:]
	return pin, nil
}

func (p *testPrompter) Touch(req TouchRequest) { p.touches = append(p.touches, req) }

// fixture wires a fake into the package's seams and returns the Card the
// package would hand out, plus the fake and the reset recorder.
type fixture struct {
	dev *fakeDevice
	// resets counts Close's reset attempts; prepared counts the preparations.
	resets, prepared int
	// resetFails scripts a reset that cannot be done; the probe then reports
	// the fake's real verified state.
	resetFails bool
	// noProbeReset: the probe's reset disconnect is not honoured, so a Card
	// opens with whatever state the card had.
	noProbeReset bool
	preflightErr error
}

func newFixture(t interface{ Cleanup(func()) }) *fixture {
	fx := &fixture{dev: newFake()}
	oldList, oldPre, oldOpen, oldReset := listReaders, preflight, openDevice, prepareReset
	listReaders = func() ([]string, error) { return []string{"Yubico YubiKey OTP+FIDO+CCID 0"}, nil }
	preflight = func(reader string) (Version, error) {
		if fx.preflightErr != nil {
			return Version{}, fx.preflightErr
		}
		if !fx.noProbeReset {
			fx.dev.reset() // the probe's disconnect resets the card
		}
		v := fx.dev.version
		return Version{v.Major, v.Minor, v.Patch}, nil
	}
	openDevice = func(reader string) (device, error) { return fx.dev, nil }
	prepareReset = func(reader string) func() (bool, bool, error) {
		fx.prepared++
		return func() (bool, bool, error) {
			fx.resets++
			if fx.resetFails {
				if fx.dev.verified {
					return false, true, fmt.Errorf("%w: the card is still PIN-verified", ErrResetFailed)
				}
				return false, false, nil
			}
			fx.dev.reset()
			return true, false, nil
		}
	}
	t.Cleanup(func() { listReaders, preflight, openDevice, prepareReset = oldList, oldPre, oldOpen, oldReset })
	return fx
}
