package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/app"
)

// TestBoundSurface is the guard of APP.md §1: the exported method set of
// every service type, names and signatures, is exactly this allowlist.
// Wails binds by reflection, promoted methods included, so a new method
// or a changed signature must be a deliberate edit here.
func TestBoundSurface(t *testing.T) {
	want := map[string][]string{
		"Vault": {
			"Activity()",
			"BeginUnlock(string) error",
			"CancelUnlock() error",
			"CreateVault(string, string, string, string, bool, bool) error",
			"DiscardRecords(string) error",
			"FinishSetup(string, string, bool) error",
			"ImportFile(string, string, string, string, string, bool, bool) error",
			"IncomingRecords(string) ([]app.IncomingRecord, error)",
			"InspectFile(string) (app.FileInfo, error)",
			"InspectRecords(string) error",
			"LastExportAt() int64",
			"Lock()",
			"MergeRecords(string, []string) error",
			"OpenVaultFile(string, string) error",
			"Readers() ([]app.Reader, error)",
			"Reopen() error",
			"Status() app.VaultStatus",
		},
		"Archives": {
			"CheckFiles() error",
			"Close(string) error",
			"CloseAll() []string",
			"Compact(string) (string, error)",
			"Create(string, string, string) (string, error)",
			"Delete(string, bool) error",
			"Details(string) (app.ArchiveDetails, error)",
			"Forget(string) error",
			"Hide(string) error",
			"List(bool) ([]app.ArchiveSummary, error)",
			"Locate(string, string) error",
			"Open(string) (app.ArchiveStat, error)",
			"Rename(string, string) error",
			"Restore(string) error",
			"RotateKey(string) (string, error)",
			"SetDescription(string, string) error",
			"Unhide(string) error",
			"Verify(string) (string, error)",
		},
		"Archive": {
			"AddFiles(string, string, []string, string) (string, error)",
			"AddFolder(string, string, string, string) (string, error)",
			"CancelOp(string) error",
			"CheckNames(string, string, []string) ([]app.Collision, error)",
			"CreateFolder(string, string, string) (string, error)",
			"Delete(string, []string) error",
			"Extract(string, []string, string, string) (string, error)",
			"Move(string, []string, string) error",
			"Op(string) (app.OpView, error)",
			"Page(string, string, string, int, int) (app.Page, error)",
			"PreviewText(string, string, int) (app.TextPreview, error)",
			"PreviewURL(string, string) (string, error)",
			"Rename(string, string, string) error",
			"Replace(string, string, string) (string, error)",
			"Stat(string) (app.ArchiveStat, error)",
		},
		"Keys": {
			"BeginEnroll(string, string) error",
			"ChangeEntangledPassword() error",
			"DropRecoveryKey(string) error",
			"EntangledState() app.EntangledState",
			"ExportBackup(string) error",
			"RemoveSlot(string) error",
			"RevealRecoveryKey(string) error",
			"RotateNow() error",
			"SaveRecoveryKey(string, string) error",
			"SetEntangled(bool) error",
			"Slots() []app.SlotView",
			"VerifyBackup(string) error",
		},
		"Settings": {
			"Get() app.Settings",
			"Set(app.Settings) error",
		},
		"Shell": {
			"CloseWindow()",
			"PickFiles(string, bool) ([]string, error)",
			"PickFolder(string) (string, error)",
			"Quit()",
			"Reveal(string) error",
			"SaveFile(string, string, string) (string, error)",
			"ShowWindow()",
		},
	}
	var c *app.Core
	services := []any{&Vault{c: c}, &Archives{c: c}, &Archive{c: c}, &Keys{c: c}, &Settings{c: c}, &Shell{}}
	for _, svc := range services {
		typ := reflect.TypeOf(svc)
		name := typ.Elem().Name()
		var got []string
		for i := 0; i < typ.NumMethod(); i++ {
			m := typ.Method(i)
			switch m.Name {
			case "ServiceName", "ServiceStartup", "ServiceShutdown", "ServeHTTP":
				continue
			}
			got = append(got, signature(m))
		}
		exp := want[name]
		if strings.Join(got, "\n") != strings.Join(exp, "\n") {
			t.Errorf("%s: bound surface changed\n got: %v\nwant: %v", name, got, exp)
		}
		// No embedded fields, no exported fields: nothing is promoted.
		st := typ.Elem()
		for i := 0; i < st.NumField(); i++ {
			f := st.Field(i)
			if f.Anonymous || f.IsExported() {
				t.Errorf("%s.%s: services carry only unexported, non-embedded fields", name, f.Name)
			}
		}
	}
}

// signature renders a method as "Name(in, ...) out" without the receiver.
func signature(m reflect.Method) string {
	ft := m.Type
	var in, out []string
	for i := 1; i < ft.NumIn(); i++ {
		in = append(in, ft.In(i).String())
	}
	for i := 0; i < ft.NumOut(); i++ {
		out = append(out, ft.Out(i).String())
	}
	s := m.Name + "(" + strings.Join(in, ", ") + ")"
	switch len(out) {
	case 0:
	case 1:
		s += " " + out[0]
	default:
		s += " (" + strings.Join(out, ", ") + ")"
	}
	return s
}

// TestMarshalError: only the coded shape crosses; anything else is
// "internal".
func TestMarshalError(t *testing.T) {
	if got := string(MarshalError(&app.Error{Code: app.CodeTokenPIN})); got != `{"code":"token.pin"}` {
		t.Errorf("coded: %s", got)
	}
	if got := string(MarshalError(errString("offset 0x1234 in /Users/me/vault.eks"))); got != `{"code":"internal"}` {
		t.Errorf("foreign error leaked: %s", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
