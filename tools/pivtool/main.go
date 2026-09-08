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
//	pivtool selftest [-reader NAME] [-slot 9d] [-slow SECONDS] [-default-pin]
//	pivtool resetcheck [-reader NAME]
//	pivtool idle [-seconds N] [-keepalive S] [-verify] [-verify-after] [-default-pin]
//	pivtool busy [-rounds N] [-verify]
//	pivtool touchabort [-mode cancel|reset|close] [-after N] [-default-pin]
//	pivtool hold [-touch] [-seconds N] [-release reset|handle|leave|none] [-default-pin]
//	pivtool probe [-share shared|exclusive|direct] [-spin MS]
//
// -default-pin answers a test key's factory PIN (and management key) without
// asking; it is for a key the user has handed over as a test key, never for
// one in use. idle and busy are the measurements behind DESIGN.md §11 trap
// 25: how long an idle exclusive connection survives, and how soon the card
// can be reopened after this program's own close.
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
	case "idle":
		err = cmdIdle(os.Args[2:])
	case "busy":
		err = cmdBusy(os.Args[2:])
	case "touchabort":
		err = cmdTouchAbort(os.Args[2:])
	case "hold":
		err = cmdHold(os.Args[2:])
	case "probe":
		err = cmdProbe(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: pivtool readers | info [-reader NAME] | generate [-reader NAME] [-slot 9d] [-pin-policy once|always] | selftest [-reader NAME] [-slot 9d] | resetcheck [-reader NAME] | idle [-seconds N] [-keepalive S] [-verify] [-default-pin] | busy [-rounds N] | touchabort [-mode cancel|reset|close] [-after N] [-default-pin] | hold [-touch] [-seconds N] [-release reset|handle|leave|none] [-default-pin] | probe [-share shared|exclusive|direct] [-spin MS]")
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
	defaults := fs.Bool("default-pin", false, "use the factory PIN and management key without asking (a test key only)")
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
	var mk []byte
	if *defaults {
		mk = piv.DefaultManagementKey() // a test key with factory secrets, by the caller's word
	} else if mk, err = managementKey(c); err != nil {
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
type terminalPrompter struct {
	// slow: wait this long before answering the PIN, to stand in for a user
	// typing slowly — the case the host resets the connection under
	// (DESIGN.md §11 trap 25). defaultPIN answers the factory PIN
	// without asking: a test key only.
	slow       time.Duration
	defaultPIN bool
	// noTouch: the experiment wants the touch wait to run out, or to be
	// interrupted — the prompt says so.
	noTouch bool
}

func (p terminalPrompter) PIN(st piv.PINStatus) (string, error) {
	if p.slow > 0 {
		fmt.Printf("(answering the PIN in %v)\n", p.slow)
		time.Sleep(p.slow)
	}
	if p.defaultPIN {
		return "123456", nil
	}
	switch {
	case st.Verified:
		return readSecret("PIN (card already verified; count not readable): ")
	default:
		return readSecret(fmt.Sprintf("PIN (%d retries left): ", st.Retries))
	}
}

func (p terminalPrompter) Touch(req piv.TouchRequest) {
	if p.noTouch {
		fmt.Fprintf(os.Stderr, ">>> the key is waiting for a touch: do NOT touch it (operation %d on slot %s)\n", req.N, req.Slot)
		return
	}
	fmt.Fprintf(os.Stderr, ">>> touch the YubiKey now (operation %d on slot %s)\n", req.N, req.Slot)
}

func cmdSelftest(args []string) error {
	fs := flag.NewFlagSet("selftest", flag.ExitOnError)
	reader := fs.String("reader", "", "reader name (default: the only one)")
	slotArg := fs.String("slot", "9d", "slot in hex")
	rounds := fs.Int("rounds", 2, "ECDH rounds")
	slow := fs.Int("slow", 0, "answer the PIN only after this many seconds (a slow user)")
	defaultPIN := fs.Bool("default-pin", false, "answer the factory PIN 123456 without asking (a test key only)")
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
	tok, err := c.Token(info.PublicKey, terminalPrompter{slow: time.Duration(*slow) * time.Second, defaultPIN: *defaultPIN})
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

// cmdIdle measures what an idle exclusive connection survives: open, ask
// the card something, hold the connection without sending anything for
// -seconds (or with a PIN-state probe every -keepalive seconds), then ask
// again and report whether the card was reset meanwhile. With -verify the
// PIN is verified first (the factory default with -default-pin: test keys
// only), so a lost verification shows too. This is the behaviour behind
// DECISIONS.md 2026-09-07 (the first hardware test).
func cmdIdle(args []string) error {
	fs := flag.NewFlagSet("idle", flag.ExitOnError)
	reader := fs.String("reader", "", "reader name (default: the only one)")
	seconds := fs.Int("seconds", 10, "how long to hold the connection idle")
	keepalive := fs.Int("keepalive", 0, "send a PIN-state probe every this many seconds (0: none)")
	verify := fs.Bool("verify", false, "verify the PIN first")
	verifyAfter := fs.Bool("verify-after", false, "verify the PIN after the idle: the app's own sequence, a slow user at the prompt")
	defaultPIN := fs.Bool("default-pin", false, "use the factory PIN 123456 (a test key only)")
	fs.Parse(args)
	c, err := open(*reader)
	if err != nil {
		return err
	}
	defer closeCard(c)
	if _, err := printPINState(c); err != nil {
		return err
	}
	if *verify {
		pin := "123456"
		if !*defaultPIN {
			pin, err = readSecret("PIN: ")
			if err != nil {
				return err
			}
		}
		if _, err := c.ProtectedManagementKey(pin); err != nil && !errors.Is(err, piv.ErrNoProtectedKey) {
			return fmt.Errorf("verify: %w", err)
		}
		st, err := c.PINState()
		if err != nil {
			return err
		}
		fmt.Printf("verified: %v\n", st.Verified)
	}
	t0 := time.Now()
	remaining := *seconds
	for remaining > 0 {
		step := remaining
		if *keepalive > 0 && *keepalive < step {
			step = *keepalive
		}
		time.Sleep(time.Duration(step) * time.Second)
		remaining -= step
		if *keepalive > 0 && remaining > 0 {
			st, err := c.PINState()
			fmt.Printf("t=%v keepalive: verified=%v err=%v\n", time.Since(t0).Round(time.Millisecond), st.Verified, err)
			if err != nil {
				return err
			}
		}
	}
	st, err := c.PINState()
	fmt.Printf("t=%v after idle: verified=%v retries=%d err=%v\n", time.Since(t0).Round(time.Millisecond), st.Verified, st.Retries, err)
	if err != nil {
		return err
	}
	if *verifyAfter {
		pin := "123456"
		if !*defaultPIN {
			if pin, err = readSecret("PIN: "); err != nil {
				return err
			}
		}
		if _, err := c.ProtectedManagementKey(pin); err != nil && !errors.Is(err, piv.ErrNoProtectedKey) {
			return fmt.Errorf("verify after the idle: %w", err)
		}
		st, err := c.PINState()
		fmt.Printf("t=%v verified after the idle: %v err=%v\n", time.Since(t0).Round(time.Millisecond), st.Verified, err)
		return err
	}
	return nil
}

// cmdBusy measures how long the card is unavailable after this program's
// own close (which resets the card): open, close, then reopen in a loop
// until it succeeds, reporting what each attempt answered.
func cmdBusy(args []string) error {
	fs := flag.NewFlagSet("busy", flag.ExitOnError)
	reader := fs.String("reader", "", "reader name (default: the only one)")
	rounds := fs.Int("rounds", 3, "open/close rounds")
	verify := fs.Bool("verify", false, "verify the factory PIN before each close, so the close resets the card")
	fs.Parse(args)
	for i := 1; i <= *rounds; i++ {
		c, err := open(*reader)
		if err != nil {
			return err
		}
		name := c.Reader()
		if *verify {
			if _, err := c.ProtectedManagementKey("123456"); err != nil && !errors.Is(err, piv.ErrNoProtectedKey) {
				closeCard(c)
				return fmt.Errorf("verify: %w", err)
			}
		}
		t0 := time.Now()
		closeCard(c)
		fmt.Printf("round %d: closed in %v\n", i, time.Since(t0).Round(time.Millisecond))
		t1 := time.Now()
		for attempt := 1; ; attempt++ {
			c2, err := piv.Open(name)
			if err == nil {
				fmt.Printf("round %d: reopened after %v (%d attempts)\n", i, time.Since(t1).Round(time.Millisecond), attempt)
				closeCard(c2)
				break
			}
			fmt.Printf("round %d: attempt %d at %v: %v\n", i, attempt, time.Since(t1).Round(time.Millisecond), err)
			if time.Since(t1) > 10*time.Second {
				return fmt.Errorf("still not openable after 10 s: %w", err)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	return nil
}

// readSecretOr asks at the terminal, or answers a known value when the
// caller said the key is a test key with factory secrets.
func readSecretOr(useDefault bool, value, prompt string) (string, error) {
	if useDefault {
		return value, nil
	}
	return readSecret(prompt)
}

// testKeySerial is the one key touchabort may run on: the user's test key,
// whose PIV module is free to experiment with (DECISIONS 2026-09-07).
const testKeySerial = 35678166

// cmdTouchAbort measures whether a touch wait can be cut short from another
// goroutine (DESIGN.md §11 trap 23): the ECDH on the slot is started, the
// key blinks for a touch nobody gives, and after -after seconds the
// connection under it is interrupted the -mode way. Reported: when the
// blocked ECDH returned and with what, and whether the key reopens.
func cmdTouchAbort(args []string) error {
	fs := flag.NewFlagSet("touchabort", flag.ExitOnError)
	reader := fs.String("reader", "", "reader name (default: the only one)")
	slotArg := fs.String("slot", "9d", "slot in hex")
	mode := fs.String("mode", "cancel", "cancel (SCardCancel on the context) | reset (SCardDisconnect with a reset, from another thread) | close (piv-go's Close)")
	after := fs.Int("after", 2, "seconds into the touch wait before interrupting")
	defaultPIN := fs.Bool("default-pin", false, "answer the factory PIN 123456 without asking (a test key only)")
	fs.Parse(args)
	slot, err := parseSlot(*slotArg)
	if err != nil {
		return err
	}
	c, err := open(*reader)
	if err != nil {
		return err
	}
	if c.Serial() != testKeySerial {
		closeCard(c)
		return fmt.Errorf("serial %d is not the test key (%d): refusing to experiment on it", c.Serial(), testKeySerial)
	}
	info, err := c.Inspect(slot)
	if err != nil {
		closeCard(c)
		return err
	}
	printKey(info)
	tok, err := c.Token(info.PublicKey, terminalPrompter{defaultPIN: *defaultPIN, noTouch: true})
	if err != nil {
		closeCard(c)
		return err
	}
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		closeCard(c)
		return err
	}
	type result struct {
		at  time.Duration
		err error
	}
	t0 := time.Now()
	done := make(chan result, 1)
	go func() {
		_, err := tok.ECDH(eph.PublicKey().Bytes())
		done <- result{time.Since(t0), err}
	}()
	select {
	case r := <-done:
		fmt.Printf("t=%v ECDH returned before the interrupt: err=%v\n", r.at.Round(time.Millisecond), r.err)
		closeCard(c)
		return nil
	case <-time.After(time.Duration(*after) * time.Second):
	}
	fmt.Printf("t=%v interrupting: %s\n", time.Since(t0).Round(time.Millisecond), *mode)
	ierr := c.Interrupt(piv.InterruptMode(*mode))
	fmt.Printf("t=%v interrupt returned: err=%v\n", time.Since(t0).Round(time.Millisecond), ierr)
	select {
	case r := <-done:
		fmt.Printf("t=%v ECDH returned %v after the interrupt: err=%v\n", r.at.Round(time.Millisecond), (r.at - time.Duration(*after)*time.Second).Round(time.Millisecond), r.err)
	case <-time.After(40 * time.Second):
		fmt.Println("ECDH still blocked 40 s after the interrupt")
	}
	if *mode != string(piv.InterruptClose) {
		fmt.Printf("releasing piv-go's connection: err=%v\n", c.Interrupt(piv.InterruptClose))
	}
	// Does the key come back, and in what state?
	time.Sleep(500 * time.Millisecond)
	c2, err := open(*reader)
	if err != nil {
		return fmt.Errorf("reopen after the interrupt: %w", err)
	}
	defer closeCard(c2)
	st, err := printPINState(c2)
	fmt.Printf("reopened: verified=%v retries=%d err=%v\n", st.Verified, st.Retries, err)
	return nil
}

// cmdHold is one side of the takeover experiment (DESIGN.md §11 trap 27):
// this process holds the test key PIN-verified — idle with keep-alive
// probes, or inside a touch wait — for a while, then releases it the way
// -release says, printing a timeline. Another process runs probe meanwhile.
//
//	reset:  the package's own Close — piv-go's leave-card close, then the
//	        reset connection (the gap between them is what probe races for)
//	handle: SCardDisconnect(SCARD_RESET_CARD) on the exclusive handle itself
//	leave:  piv-go's close alone, no reset — the worst case, on purpose
//	none:   never released: the process waits to be killed
func cmdHold(args []string) error {
	fs := flag.NewFlagSet("hold", flag.ExitOnError)
	reader := fs.String("reader", "", "reader name (default: the only one)")
	slotArg := fs.String("slot", "9d", "slot in hex")
	touch := fs.Bool("touch", false, "hold inside a touch wait (an ECDH nobody touches) instead of idle")
	seconds := fs.Int("seconds", 6, "how long to hold before releasing (idle mode)")
	release := fs.String("release", "reset", "reset | handle | leave | none")
	defaultPIN := fs.Bool("default-pin", false, "answer the factory PIN 123456 without asking (a test key only)")
	fs.Parse(args)
	slot, err := parseSlot(*slotArg)
	if err != nil {
		return err
	}
	c, err := open(*reader)
	if err != nil {
		return err
	}
	if c.Serial() != testKeySerial {
		closeCard(c)
		return fmt.Errorf("serial %d is not the test key (%d): refusing to experiment on it", c.Serial(), testKeySerial)
	}
	t0 := time.Now()
	stamp := func(format string, a ...any) {
		fmt.Printf("t=%v hold: %s\n", time.Since(t0).Round(time.Millisecond), fmt.Sprintf(format, a...))
	}
	pin := "123456"
	if !*defaultPIN {
		if pin, err = readSecret("PIN: "); err != nil {
			closeCard(c)
			return err
		}
	}
	if *touch {
		info, err := c.Inspect(slot)
		if err != nil {
			closeCard(c)
			return err
		}
		tok, err := c.Token(info.PublicKey, terminalPrompter{defaultPIN: *defaultPIN, noTouch: true})
		if err != nil {
			closeCard(c)
			return err
		}
		eph, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			closeCard(c)
			return err
		}
		stamp("PIN verified inside ECDH; waiting for a touch nobody gives")
		_, err = tok.ECDH(eph.PublicKey().Bytes())
		stamp("ECDH returned: err=%v", err)
	} else {
		if _, err := c.ProtectedManagementKey(pin); err != nil && !errors.Is(err, piv.ErrNoProtectedKey) {
			closeCard(c)
			return fmt.Errorf("verify: %w", err)
		}
		st, _ := c.PINState()
		stamp("PIN verified=%v; holding idle with a probe every 3 s for %d s", st.Verified, *seconds)
		deadline := time.Now().Add(time.Duration(*seconds) * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(3 * time.Second)
			st, err := c.PINState()
			stamp("probe: verified=%v err=%v", st.Verified, err)
			if err != nil {
				break
			}
		}
	}
	switch *release {
	case "reset":
		stamp("releasing: Close (piv-go leave-card close, then the reset connection)")
		err := c.Close()
		stamp("released: err=%v resetFailed=%v", err, c.ResetFailed())
	case "handle":
		stamp("releasing: SCardDisconnect(SCARD_RESET_CARD) on the exclusive handle")
		err := c.Interrupt(piv.InterruptReset)
		stamp("reset on the handle: err=%v", err)
		err = c.Interrupt(piv.InterruptClose)
		stamp("piv-go's close after it: err=%v", err)
	case "leave":
		stamp("releasing: piv-go's close alone, NO reset (the worst case, on purpose)")
		err := c.Interrupt(piv.InterruptClose)
		stamp("left: err=%v", err)
	case "none":
		stamp("holding for ever: kill this process (pid %d) from another shell", os.Getpid())
		select {}
	default:
		closeCard(c)
		return fmt.Errorf("unknown -release %q", *release)
	}
	return nil
}

// cmdProbe is the other side: what another process sees of the card —
// whether it can connect in the given share mode, and whether the card is
// PIN-verified for it. With -spin it tries in a tight loop for that many
// milliseconds and reports the first connection: the race for a window.
// It sends nothing but SELECT, GET SERIAL and the retry-free empty VERIFY.
func cmdProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	reader := fs.String("reader", "", "reader name (default: the only one)")
	share := fs.String("share", "shared", "shared | exclusive | direct")
	spin := fs.Int("spin", 0, "keep trying to connect for this many milliseconds (0: once)")
	fs.Parse(args)
	name := *reader
	if name == "" {
		readers, err := piv.Readers()
		if err != nil {
			return err
		}
		if len(readers) != 1 {
			return fmt.Errorf("%d YubiKey readers; pick one with -reader", len(readers))
		}
		name = readers[0]
	}
	t0 := time.Now()
	var res piv.ProbeResult
	n := 1
	if *spin > 0 {
		res, n = piv.ProbeSpin(name, piv.ShareMode(*share), time.Duration(*spin)*time.Millisecond)
	} else {
		res = piv.Probe(name, piv.ShareMode(*share))
	}
	fmt.Printf("t=%v probe %s (%d attempt(s), %v): %s\n", time.Since(t0).Round(time.Millisecond), *share, n, res.Took.Round(time.Millisecond), res)
	if res.Connected && res.Serial != 0 && res.Serial != testKeySerial {
		fmt.Printf("note: serial %d is not the test key; nothing was changed on it\n", res.Serial)
	}
	return nil
}
