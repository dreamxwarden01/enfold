//go:build windows

package piv

import (
	"errors"
	"reflect"
	"testing"

	pivgo "github.com/go-piv/piv-go/v2/piv"
)

// The package's own return-code table (DESIGN.md §11 trap 26): the
// facility's codes by number, and a Win32 code by the call it came from.
func TestReturnCodeTable(t *testing.T) {
	cases := []struct {
		call string
		rc   uintptr
		want error
	}{
		{"SCardTransmit", 0x0000001f, ErrNoCard},            // ERROR_GEN_FAILURE: the key pulled under a VERIFY
		{"SCardTransmit", 0x00000016, ErrNoCard},            // ERROR_BAD_COMMAND: the preflight after the pull
		{"SCardConnectW", 0x00000048f, ErrNoCard},           // ERROR_DEVICE_NOT_CONNECTED
		{"SCardEstablishContext", 0x0000006d, ErrNoService}, // ERROR_BROKEN_PIPE: a remote session without redirection
		{"SCardListReadersW", 0x000006ba, ErrNoService},     // RPC_S_SERVER_UNAVAILABLE
		{"SCardTransmit", 0x80100010, ErrNoCard},            // NOT_READY
		{"SCardTransmit", 0x80100013, ErrNoCard},            // COMM_ERROR
		{"SCardTransmit", 0x8010001F, ErrNoCard},            // UNEXPECTED
		{"SCardTransmit", 0x8010002F, ErrNoCard},            // COMM_DATA_LOST
		{"SCardConnectW", 0x80100012, ErrNoService},         // SYSTEM_CANCELLED
		{"SCardConnectW", 0x80100018, ErrNoService},         // SHUTDOWN
		{"SCardConnectW", 0x8010001D, ErrNoService},         // NO_SERVICE
		{"SCardTransmit", 0x80100068, ErrCardReset},         // W_RESET_CARD
		{"SCardConnectW", 0x8010000B, ErrBusy},              // SHARING_VIOLATION
	}
	for _, tc := range cases {
		got := scCheck(tc.call, tc.rc)
		if !errors.Is(got, tc.want) {
			t.Errorf("%s 0x%08x: got %v, want %v", tc.call, tc.rc, got, tc.want)
		}
	}
	// This program's own mistakes stay unclassified: a bug is not a removal.
	for _, rc := range []uintptr{0x80100003, 0x80100004, 0x80100016} {
		got := scCheck("SCardTransmit", rc)
		if got == nil || errors.Is(got, ErrNoCard) || errors.Is(got, ErrNoService) {
			t.Errorf("0x%08x classified: %v", rc, got)
		}
	}
	if scCheck("SCardTransmit", 0) != nil {
		t.Error("success is an error")
	}
}

// The release resets the card on piv-go's own exclusive handle, reached by
// reflection (trap 27): this pins the layout it depends on — YubiKey.h
// *scHandle with handle uintptr, and YubiKey.ctx *scContext with ctx
// uintptr — so that a piv-go bump that renames a field fails here, in
// -short, rather than silently sending every release the long way.
func TestPivGoHandleLayout(t *testing.T) {
	typ := reflect.TypeOf(pivgo.YubiKey{})
	for _, f := range []struct{ outer, inner string }{{"h", "handle"}, {"ctx", "ctx"}} {
		outer, ok := typ.FieldByName(f.outer)
		if !ok || outer.Type.Kind() != reflect.Ptr {
			t.Fatalf("piv-go YubiKey.%s: not a pointer field (%v)", f.outer, ok)
		}
		inner, ok := outer.Type.Elem().FieldByName(f.inner)
		if !ok || inner.Type.Kind() != reflect.Uintptr {
			t.Fatalf("piv-go YubiKey.%s.%s: not a uintptr field (%v)", f.outer, f.inner, ok)
		}
	}
	// And the handle reset refuses anything but a piv-go device.
	if err := resetHandleReal(&fakeDevice{}); !errors.Is(err, ErrParams) {
		t.Fatalf("a fake device: %v", err)
	}
}
