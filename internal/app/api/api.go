// Package api holds the service types the shell binds into the WebView.
// Each is a struct with only unexported fields and no embedding, whose
// exported method set is the whole API the frontend can call (APP.md §1);
// every method returns the core's coded *app.Error. Nothing from
// keystore, archive, format or piv appears in a signature.
package api

import (
	"encoding/json"
	"errors"

	"github.com/dreamxwarden01/enfold/internal/app"
)

// Services builds the bound set over one core.
func Services(c *app.Core) (*Vault, *Archives, *Archive, *Keys, *Settings) {
	return &Vault{c: c}, &Archives{c: c}, &Archive{c: c}, &Keys{c: c}, &Settings{c: c}
}

// MarshalError is the error marshaller the shell registers for every
// service: the coded shape for *app.Error, "internal" for anything else,
// so no package error is ever reflected into the WebView.
func MarshalError(err error) []byte {
	var e *app.Error
	if errors.As(err, &e) {
		b, _ := json.Marshal(e)
		return b
	}
	return []byte(`{"code":"internal"}`)
}

// asErr returns a nil error interface for a nil *app.Error.
func asErr(e *app.Error) error {
	if e == nil {
		return nil
	}
	return e
}

// Vault is the session: state, ceremony, lock.
type Vault struct {
	c *app.Core
}

func (v *Vault) Status() app.VaultStatus { return v.c.Status() }

func (v *Vault) Readers() ([]app.Reader, error) {
	r, e := v.c.Readers()
	return r, asErr(e)
}

// BeginUnlock starts a ceremony: "token", "password" or "recovery". The
// secrets themselves arrive on the raw message channel, never here.
func (v *Vault) BeginUnlock(method string) error {
	return asErr(v.c.BeginUnlock(app.UnlockMethod(method)))
}

func (v *Vault) CancelUnlock() error { return asErr(v.c.CancelUnlock()) }
func (v *Vault) Lock()               { v.c.Lock() }
func (v *Vault) Reopen() error       { return asErr(v.c.Reopen()) }
func (v *Vault) Activity()           { v.c.Activity() }

func (v *Vault) OpenVaultFile(path, displayName string) error {
	return asErr(v.c.OpenVaultFile(path, displayName))
}

// CreateVault makes a new vault with a recovery key and a first slot of
// kind "token" or "password"; the recovery key is shown once through the
// ceremony's one-time URL.
func (v *Vault) CreateVault(path, displayName, kind, label string, entangle bool) error {
	return asErr(v.c.CreateVault(path, displayName, app.EnrollOptions{Kind: app.EnrollKind(kind), Label: label, Entangle: entangle}))
}

// Archives is the registry's list and the per-archive operations that do
// not need it open.
type Archives struct {
	c *app.Core
}

func (a *Archives) List(showHidden bool) ([]app.ArchiveSummary, error) {
	l, e := a.c.ListArchives(showHidden)
	return l, asErr(e)
}

func (a *Archives) Open(id string) (app.ArchiveStat, error) {
	s, e := a.c.OpenArchive(id)
	return s, asErr(e)
}

func (a *Archives) Close(id string) error     { return asErr(a.c.CloseArchive(id)) }
func (a *Archives) CloseAll() []string        { return a.c.CloseAllArchives() }
func (a *Archives) Hide(id string) error      { return asErr(a.c.HideArchive(id, true)) }
func (a *Archives) Unhide(id string) error    { return asErr(a.c.HideArchive(id, false)) }
func (a *Archives) Locate(id, p string) error { return asErr(a.c.Locate(id, p)) }

func (a *Archives) Create(path, name string, noCompression bool) (string, error) {
	id, e := a.c.CreateArchive(path, name, noCompression)
	return id, asErr(e)
}

func (a *Archives) Compact(id string) (string, error) {
	op, e := a.c.Compact(id)
	return op, asErr(e)
}

func (a *Archives) RotateKey(id string) (string, error) {
	op, e := a.c.RotateKey(id)
	return op, asErr(e)
}

func (a *Archives) Verify(id string) (string, error) {
	op, e := a.c.Verify(id)
	return op, asErr(e)
}

// Archive is an open archive.
type Archive struct {
	c *app.Core
}

func (a *Archive) Page(id, folder, sortBy string, offset, limit int) (app.Page, error) {
	p, e := a.c.Page(id, folder, sortBy, offset, limit)
	return p, asErr(e)
}

func (a *Archive) Stat(id string) (app.ArchiveStat, error) {
	s, e := a.c.Stat(id)
	return s, asErr(e)
}

func (a *Archive) AddFiles(id, folder string, paths []string, policy string) (string, error) {
	op, e := a.c.AddFiles(id, folder, paths, app.AddPolicy(policy))
	return op, asErr(e)
}

func (a *Archive) AddFolder(id, folder, dir, policy string) (string, error) {
	op, e := a.c.AddFolder(id, folder, dir, app.AddPolicy(policy))
	return op, asErr(e)
}

func (a *Archive) Replace(id, fileID, path string) (string, error) {
	op, e := a.c.ReplaceFile(id, fileID, path)
	return op, asErr(e)
}

func (a *Archive) Delete(id string, fileIDs []string) error {
	return asErr(a.c.DeleteFiles(id, fileIDs))
}

func (a *Archive) Rename(id, fileID, newLeaf string) error {
	return asErr(a.c.RenameFile(id, fileID, newLeaf))
}

func (a *Archive) Extract(id string, fileIDs []string, dir, policy string) (string, error) {
	op, e := a.c.Extract(id, fileIDs, dir, app.ExtractPolicy(policy))
	return op, asErr(e)
}

func (a *Archive) Save(id string) (string, error) {
	op, e := a.c.Save(id)
	return op, asErr(e)
}

func (a *Archive) Discard(id string) error  { return asErr(a.c.Discard(id)) }
func (a *Archive) KeepOpen(id string) error { return asErr(a.c.KeepOpen(id)) }

func (a *Archive) PreviewURL(id, fileID string) (string, error) {
	u, e := a.c.PreviewURL(id, fileID)
	return u, asErr(e)
}

// PreviewText returns up to maxBytes of a text file and whether it was cut.
func (a *Archive) PreviewText(id, fileID string, maxBytes int) (app.TextPreview, error) {
	t, trunc, e := a.c.PreviewText(id, fileID, maxBytes)
	return app.TextPreview{Text: t, Truncated: trunc}, asErr(e)
}

func (a *Archive) CheckNames(id, folder string, names []string) ([]app.Collision, error) {
	c, e := a.c.CheckNames(id, folder, names)
	return c, asErr(e)
}

func (a *Archive) CancelOp(opID string) error { return asErr(a.c.CancelOp(opID)) }

func (a *Archive) Op(opID string) (app.OpView, error) {
	o, e := a.c.Op(opID)
	return o, asErr(e)
}

// Keys is the ways in, and backups.
type Keys struct {
	c *app.Core
}

func (k *Keys) Slots() []app.SlotView { return k.c.Slots() }

func (k *Keys) BeginEnroll(kind, label string, entangle bool) error {
	return asErr(k.c.BeginEnroll(app.EnrollOptions{Kind: app.EnrollKind(kind), Label: label, Entangle: entangle}))
}

func (k *Keys) RemoveSlot(recipientID string) error { return asErr(k.c.RemoveSlot(recipientID)) }
func (k *Keys) RotateNow() error                    { return asErr(k.c.RotateNow()) }
func (k *Keys) ExportBackup(path string) error      { return asErr(k.c.ExportBackup(path)) }

func (k *Keys) BackupInfo(path string) (app.BackupInfo, error) {
	b, e := k.c.BackupInfo(path)
	return b, asErr(e)
}

// Settings is the machine-local settings plus the registry's timeouts.
type Settings struct {
	c *app.Core
}

func (s *Settings) Get() app.Settings        { return s.c.GetSettings() }
func (s *Settings) Set(v app.Settings) error { return asErr(s.c.SetSettings(v)) }
