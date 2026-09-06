//go:build windows

// pivtool drives the internal/piv layer from a terminal, for the steps that
// need the user's PIN and cannot be tests: generating the key a hardware
// slot will use, and proving the token's ECDH against a software one. It
// touches only what internal/piv allows — slot 9d and the retired slots —
// never overwrites a slot, and never resets, changes a PIN or a management
// key. The PIN is read from the terminal without echo, never from an
// argument or the environment.
//
//	pivtool readers
//	pivtool info     [-reader NAME]
//	pivtool generate [-reader NAME] [-slot 9d] [-pin-policy once|always]
//	pivtool selftest [-reader NAME] [-slot 9d]
//	pivtool resetcheck [-reader NAME]
package main

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"golang.org/x/term"

	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/piv"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "readers":
		err = cmdReaders()
	case "info":
		err = cmdInfo(os.Args[2:])
	case "generate":
		err = cmdGenerate(os.Args[2:])
	case "selftest":
		err = cmdSelftest(os.Args[2:])
	case "resetcheck":
		err = cmdResetCheck(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: pivtool readers | info [-reader NAME] | generate [-reader NAME] [-slot 9d] [-pin-policy once|always] | selftest [-reader NAME] [-slot 9d] | resetcheck [-reader NAME]")
	os.Exit(2)
}

func cmdReaders() error {
	readers, err := piv.Readers()
	if err != nil {
		return err
	}
	if len(readers) == 0 {
		fmt.Println("no YubiKey reader attached")
		return nil
	}
	for _, r := range readers {
		fmt.Println(r)
	}
	return nil
}

// open picks the reader: the named one, or the only one attached.
func open(name string) (*piv.Card, error) {
	if name == "" {
		readers, err := piv.Readers()
		if err != nil {
			return nil, err
		}
		switch len(readers) {
		case 0:
			return nil, errors.New("no YubiKey reader attached")
		case 1:
			name = readers[0]
		default:
			return nil, fmt.Errorf("%d YubiKey readers; pick one with -reader", len(readers))
		}
	}
	c, err := piv.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", name, err)
	}
	fmt.Printf("token: %s, firmware %s, serial %d\n", c.Reader(), c.Version(), c.Serial())
	return c, nil
}

func closeCard(c *piv.Card) {
	if err := c.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "close:", err)
		if c.ResetFailed() {
			fmt.Fprintln(os.Stderr, "WARNING: the card may stay PIN-verified for up to 10 s; unplug it to be sure")
		}
	}
}

func parseSlot(s string) (piv.Slot, error) {
	b, err := strconv.ParseUint(s, 16, 8)
	if err != nil {
		return 0, fmt.Errorf("slot %q is not a hex byte", s)
	}
	slot, ok := piv.ParseSlot(byte(b))
	if !ok {
		return 0, fmt.Errorf("slot %s is not one this tool may use (9d, 82-95)", s)
	}
	return slot, nil
}

func printPINState(c *piv.Card) (piv.PINStatus, error) {
	st, err := c.PINState()
	if err != nil {
		return st, err
	}
	switch {
	case st.Verified:
		fmt.Println("PIN: already verified (retry count not readable in this state)")
	case st.Blocked():
		fmt.Println("PIN: BLOCKED — no retries left; this tool will not ask for it")
	default:
		fmt.Printf("PIN: %d retries left\n", st.Retries)
	}
	return st, nil
}

func printKey(k piv.KeyInfo) {
	usable := "usable"
	if !k.Usable() {
		usable = "not usable: " + k.WhyNotUsable()
	}
	pub := ""
	if k.PublicKey != nil {
		pub = " pub=" + hex.EncodeToString(k.PublicKey[:9]) + "…"
	}
	fmt.Printf("  slot %s: %s pin=%s touch=%s origin=%s cert=%v%s — %s\n", k.Slot, k.Algorithm, k.PINPolicy, k.TouchPolicy, k.Origin, k.Certificate, pub, usable)
}

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	reader := fs.String("reader", "", "reader name (default: the only one)")
	fs.Parse(args)
	c, err := open(*reader)
	if err != nil {
		return err
	}
	defer closeCard(c)
	if _, err := printPINState(c); err != nil {
		return err
	}
	keys, err := c.Keys()
	if err != nil {
		return err
	}
	fmt.Println("slots this tool may use (9d, 82-95):")
	if len(keys) == 0 {
		fmt.Println("  all empty")
	}
	for _, k := range keys {
		printKey(k)
		if k.Algorithm == piv.AlgorithmP256 {
			a, err := c.Attest(k.Slot)
			if err != nil {
				fmt.Printf("    attestation: %v\n", err)
			} else {
				fmt.Printf("    attested by Yubico: generated on serial %d (firmware %s, %s), pin=%s touch=%s\n", a.Serial, a.Version, a.FormFactor, a.PINPolicy, a.TouchPolicy)
			}
		}
	}
	if s, err := c.FirstEmptySlot(); err == nil {
		fmt.Printf("first empty slot: %s\n", s)
	} else {
		fmt.Printf("no empty slot: %v\n", err)
	}
	return nil
}

// readSecretBytes prompts on stderr and reads without echo. The caller owns
// the slice and zeroes it: this is the only form that can be wiped.
func readSecretBytes(prompt string) ([]byte, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// readSecret is for the PIN only: piv-go takes it as a string, so a copy
// that cannot be zeroed is unavoidable there; the raw bytes are wiped. The
// management key is bytes end to end and goes through readSecretBytes.
func readSecret(prompt string) (string, error) {
	b, err := readSecretBytes(prompt)
	if err != nil {
		return "", err
	}
	defer kdf.Zero(b)
	return string(b), nil
}

// managementKey finds the management key: the PIN-protected one on the
// token (after showing the retries), else the user's, typed as hex. The
// factory default is never tried by this tool.
func managementKey(c *piv.Card) ([]byte, error) {
	st, err := printPINState(c)
	if err != nil {
		return nil, err
	}
	if st.Blocked() {
		return nil, piv.ErrPINBlocked
	}
	pin, err := readSecret("PIN (to read the PIN-protected management key): ")
	if err != nil {
		return nil, err
	}
	mk, err := c.ProtectedManagementKey(pin)
	switch {
	case err == nil:
		fmt.Printf("using the PIN-protected management key from the token (%d bytes)\n", len(mk))
		return mk, nil
	case errors.Is(err, piv.ErrNoProtectedKey):
		fmt.Println("the token stores no PIN-protected management key")
	default:
		return nil, err
	}
	h, err := readSecretBytes("management key as hex (from YubiKey Manager; 32, 48 or 64 hex digits): ")
	if err != nil {
		return nil, err
	}
	defer kdf.Zero(h)
	h = bytes.TrimSpace(h)
	mk = make([]byte, hex.DecodedLen(len(h)))
	n, err := hex.Decode(mk, h)
	if err != nil {
		kdf.Zero(mk)
		return nil, fmt.Errorf("management key is not hex: %v", err)
	}
	return mk[:n], nil
}

func cmdGenerate(args []string) error {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	reader := fs.String("reader", "", "reader name (default: the only one)")
	slotArg := fs.String("slot", "", "slot in hex (default: 9d if empty, else the first empty retired slot)")
	policy := fs.String("pin-policy", "once", "PIN policy: once or always")
	fs.Parse(args)
	var pp piv.PINPolicy
	switch *policy {
	case "once":
		pp = piv.PINPolicyOnce
	case "always":
		pp = piv.PINPolicyAlways
	default:
		return fmt.Errorf("pin-policy must be once or always")
	}
	c, err := open(*reader)
	if err != nil {
		return err
	}
	defer closeCard(c)
	var slot piv.Slot
	if *slotArg != "" {
		if slot, err = parseSlot(*slotArg); err != nil {
			return err
		}
		if info, err := c.Inspect(slot); err == nil {
			printKey(info)
			return fmt.Errorf("slot %s is occupied; this tool never overwrites", slot)
		} else if !errors.Is(err, piv.ErrEmpty) {
			return err
		}
	} else {
		if slot, err = c.FirstEmptySlot(); err != nil {
			return err
		}
	}
	fmt.Printf("will generate a P-256 key in slot %s with PIN policy %s and touch policy always\n", slot, pp)
	mk, err := managementKey(c)
	if err != nil {
		return err
	}
	defer kdf.Zero(mk)
	t0 := time.Now()
	info, err := c.Generate(mk, piv.GenerateOptions{Slot: slot, PINPolicy: pp})
	if err != nil {
		return err
	}
	fmt.Printf("generated in %v\n", time.Since(t0).Round(time.Millisecond))
	printKey(info)
	fmt.Printf("slot_pubkey: %s\n", hex.EncodeToString(info.PublicKey))
	if a, err := c.Attest(slot); err != nil {
		fmt.Printf("attestation: %v\n", err)
	} else {
		fmt.Printf("attested by Yubico: serial %d, pin=%s touch=%s\n", a.Serial, a.PINPolicy, a.TouchPolicy)
	}
	return nil
}

// terminalPrompter runs the ceremony on the console.
type terminalPrompter struct{}

func (terminalPrompter) PIN(st piv.PINStatus) (string, error) {
	switch {
	case st.Verified:
		return readSecret("PIN (card already verified; count not readable): ")
	default:
		return readSecret(fmt.Sprintf("PIN (%d retries left): ", st.Retries))
	}
}

func (terminalPrompter) Touch(req piv.TouchRequest) {
	fmt.Fprintf(os.Stderr, ">>> touch the YubiKey now (operation %d on slot %s)\n", req.N, req.Slot)
}

func cmdSelftest(args []string) error {
	fs := flag.NewFlagSet("selftest", flag.ExitOnError)
	reader := fs.String("reader", "", "reader name (default: the only one)")
	slotArg := fs.String("slot", "9d", "slot in hex")
	rounds := fs.Int("rounds", 2, "ECDH rounds")
	fs.Parse(args)
	slot, err := parseSlot(*slotArg)
	if err != nil {
		return err
	}
	c, err := open(*reader)
	if err != nil {
		return err
	}
	defer closeCard(c)
	info, err := c.Inspect(slot)
	if err != nil {
		return err
	}
	printKey(info)
	tok, err := c.Token(info.PublicKey, terminalPrompter{})
	if err != nil {
		return err
	}
	tokenPub, err := ecdh.P256().NewPublicKey(info.PublicKey)
	if err != nil {
		return err
	}
	for i := 1; i <= *rounds; i++ {
		eph, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		want, err := eph.ECDH(tokenPub)
		if err != nil {
			return err
		}
		t0 := time.Now()
		got, err := tok.ECDH(eph.PublicKey().Bytes())
		dt := time.Since(t0).Round(time.Millisecond)
		if err != nil {
			return fmt.Errorf("round %d after %v: %w", i, dt, err)
		}
		ok := bytes.Equal(got, want)
		kdf.Zero(got)
		kdf.Zero(want)
		fmt.Printf("round %d: token ECDH in %v (PIN/touch included); matches software: %v\n", i, dt, ok)
		if !ok {
			return errors.New("token and software ECDH disagree")
		}
	}
	st, err := c.PINState()
	if err != nil {
		return err
	}
	fmt.Printf("before close: verified=%v\n", st.Verified)
	return nil
}

// cmdResetCheck proves DESIGN.md trap 14's mechanism end to end: verify the
// PIN through a Card, close it (which resets the card), then ask the card
// from outside — over a shared connection that changes nothing — whether
// it is still verified. It must not be.
func cmdResetCheck(args []string) error {
	fs := flag.NewFlagSet("resetcheck", flag.ExitOnError)
	reader := fs.String("reader", "", "reader name (default: the only one)")
	fs.Parse(args)
	c, err := open(*reader)
	if err != nil {
		return err
	}
	name := c.Reader()
	st, err := printPINState(c)
	if err != nil {
		closeCard(c)
		return err
	}
	if st.Blocked() {
		closeCard(c)
		return piv.ErrPINBlocked
	}
	pin, err := readSecret("PIN (one VERIFY, nothing else): ")
	if err != nil {
		closeCard(c)
		return err
	}
	if mk, err := c.ProtectedManagementKey(pin); err != nil && !errors.Is(err, piv.ErrNoProtectedKey) {
		closeCard(c)
		return err
	} else if err == nil {
		kdf.Zero(mk)
	}
	st, err = c.PINState()
	if err != nil {
		closeCard(c)
		return err
	}
	fmt.Printf("after VERIFY, before close: verified=%v\n", st.Verified)
	if err := c.Close(); err != nil {
		fmt.Printf("close: %v (reset-failed=%v)\n", err, c.ResetFailed())
	} else {
		fmt.Println("closed; reset done")
	}
	for i := 1; i <= 3; i++ {
		v, err := piv.Verified(name)
		if err != nil {
			return fmt.Errorf("probe %d: %w", i, err)
		}
		fmt.Printf("probe %d from outside: verified=%v\n", i, v)
		if v {
			return errors.New("the card is still verified after Close: the reset did not take")
		}
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Println("OK: the PIN-verified state did not survive Close")
	return nil
}
