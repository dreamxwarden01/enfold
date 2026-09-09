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

// InspectFile says what a keystore file is — a vault or a backup — and
// whether it is this vault and newer, without a credential.
func (v *Vault) InspectFile(path string) (app.FileInfo, error) {
	i, e := v.c.InspectFile(path)
	return i, asErr(e)
}

// CreateVault makes a new vault with a recovery key and a first slot of
// kind "token" or "password"; the recovery key is shown once through the
// ceremony's one-time URL. path empty is the one place a vault lives;
// over a vault kept there, replace must be true (a confirmed replacement).
func (v *Vault) CreateVault(path, displayName, kind, label string, entangle, replace bool) error {
	return asErr(v.c.CreateVault(path, displayName, app.EnrollOptions{Kind: app.EnrollKind(kind), Label: label, Entangle: entangle}, replace))
}

// ImportFile makes a vault or backup file the vault kept here, once it
// has proved itself: a vault by unlocking with method ("token",
// "password", "recovery"), a backup by its recovery key and then the
// first way in (kind, label, entangle). replace confirms replacing the
// vault kept here.
func (v *Vault) ImportFile(path, displayName, method, kind, label string, entangle, replace bool) error {
	return asErr(v.c.ImportFile(path, displayName, app.UnlockMethod(method), app.EnrollOptions{Kind: app.EnrollKind(kind), Label: label, Entangle: entangle}, replace))
}

// FinishSetup gives a vault that has only its recovery key its first way
// in.
func (v *Vault) FinishSetup(kind, label string, entangle bool) error {
	return asErr(v.c.FinishSetup(app.EnrollOptions{Kind: app.EnrollKind(kind), Label: label, Entangle: entangle}))
}

// InspectRecords opens a backup or a vault over a staged copy for a merge:
// a ceremony for this vault's VMK, ending with a handle in the ceremony
// state's slotLabel. Nothing of that file is installed.
func (v *Vault) InspectRecords(path string) error {
	return asErr(v.c.InspectRecords(path))
}

// IncomingRecords is the record list behind a merge handle.
func (v *Vault) IncomingRecords(handle string) ([]app.IncomingRecord, error) {
	r, e := v.c.IncomingRecords(handle)
	return r, asErr(e)
}

// MergeRecords takes the ticked records into this vault's registry: a
// registry write, no ceremony.
func (v *Vault) MergeRecords(handle string, ids []string) error {
	return asErr(v.c.MergeRecords(handle, ids))
}

// DiscardRecords ends a merge handle: the dialog closed.
func (v *Vault) DiscardRecords(handle string) error {
	return asErr(v.c.DiscardRecords(handle))
}

// LastExportAt is when a backup of the vault kept here was last written,
// in Unix seconds; 0 means never. It words the confirmations of Forget and
// Delete and pre-selects the rotate dialog's backup checkbox, and gates
// nothing (APP.md §13).
func (v *Vault) LastExportAt() int64 { return v.c.LastExportAt() }

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

// Create makes an archive at path with the compression method chosen in
// the dialog: store · fastest · normal · better · best (APP.md §3, §6).
// A path where a file already exists is archive.exists.
func (a *Archives) Create(path, name, method string) (string, error) {
	id, e := a.c.CreateArchive(path, name, method)
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

// Rename writes the registry's trusted name (FORMAT.md §7.4); no ceremony.
func (a *Archives) Rename(id, name string) error { return asErr(a.c.RenameArchive(id, name)) }

// SetDescription writes the record's description: at most 1 024 bytes of
// UTF-8, empty clears it (FORMAT.md §7.1).
func (a *Archives) SetDescription(id, text string) error {
	return asErr(a.c.SetArchiveDescription(id, text))
}

// Details is one registry record read whole, for the details pane and its
// modal: no key material, and refused while the vault is locked.
func (a *Archives) Details(id string) (app.ArchiveDetails, error) {
	d, e := a.c.ArchiveDetails(id)
	return d, asErr(e)
}

// Forget drops the record softly: its keys stay until the purge at an unlock
// more than thirty days later, and Restore brings it back.
func (a *Archives) Forget(id string) error  { return asErr(a.c.ForgetArchive(id)) }
func (a *Archives) Restore(id string) error { return asErr(a.c.RestoreArchive(id)) }

// Delete is Forget plus the file when alsoFile is set: the file is removed
// first and only then is the record forgotten.
func (a *Archives) Delete(id string, alsoFile bool) error {
	return asErr(a.c.DeleteArchive(id, alsoFile))
}

// CheckFiles refreshes the presence of every record's last_path; the list's
// Status column follows on archives.changed.
func (a *Archives) CheckFiles() error { return asErr(a.c.CheckFiles()) }

// Archive is an open archive.
type Archive struct {
	c *app.Core
}

// Page lists the live children of one directory in the merged view. dirID
// is a record id — the all-zero id is the archive's root — never a path, and
// one that no longer names a live directory is file.not_found.
func (a *Archive) Page(id, dirID, sortBy string, offset, limit int) (app.Page, error) {
	p, e := a.c.Page(id, dirID, sortBy, offset, limit)
	return p, asErr(e)
}

func (a *Archive) Stat(id string) (app.ArchiveStat, error) {
	s, e := a.c.Stat(id)
	return s, asErr(e)
}

// CreateFolder stages a directory record and returns its id: a folder is a
// record, so an empty one survives the save (FORMAT.md R39).
func (a *Archive) CreateFolder(id, parentID, name string) (string, error) {
	rid, e := a.c.CreateFolder(id, parentID, name)
	return rid, asErr(e)
}

func (a *Archive) AddFiles(id, parentID string, paths []string, policy string) (string, error) {
	op, e := a.c.AddFiles(id, parentID, paths, app.AddPolicy(policy))
	return op, asErr(e)
}

func (a *Archive) AddFolder(id, parentID, dir, policy string) (string, error) {
	op, e := a.c.AddFolder(id, parentID, dir, app.AddPolicy(policy))
	return op, asErr(e)
}

func (a *Archive) Replace(id, fileID, path string) (string, error) {
	op, e := a.c.ReplaceFile(id, fileID, path)
	return op, asErr(e)
}

// Delete stages a deletion of each record; a directory takes its subtree,
// tombstoned in the same write and counted as one change.
func (a *Archive) Delete(id string, recordIDs []string) error {
	return asErr(a.c.DeleteRecords(id, recordIDs))
}

func (a *Archive) Rename(id, recordID, newName string) error {
	return asErr(a.c.RenameRecord(id, recordID, newName))
}

// Move re-parents each record onto parentID, pre-flighted whole against
// FORMAT.md R39 and refused whole and in place.
func (a *Archive) Move(id string, recordIDs []string, parentID string) error {
	return asErr(a.c.MoveRecords(id, recordIDs, parentID))
}

func (a *Archive) Extract(id string, recordIDs []string, dir, policy string) (string, error) {
	op, e := a.c.Extract(id, recordIDs, dir, app.ExtractPolicy(policy))
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

// CheckNames lets the UI ask once before an add: which of the offered names
// a live child of parentID already holds. A name given with a trailing "/"
// is offered as a directory, and the collision carries the kind on both
// sides.
func (a *Archive) CheckNames(id, parentID string, names []string) ([]app.Collision, error) {
	c, e := a.c.CheckNames(id, parentID, names)
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

// BeginEnroll adds a way in: "token", "password" or "recovery". An
// enrolled key inherits the vault's entangled password and is wrapped from
// the kept K_P, so no password is chosen here (APP.md §13).
func (k *Keys) BeginEnroll(kind, label string) error {
	return asErr(k.c.BeginEnroll(app.EnrollOptions{Kind: app.EnrollKind(kind), Label: label}))
}

// EntangledState is the Keys page's row for the vault's password: whether
// it is on, and whether the invariant would let it be turned on.
func (k *Keys) EntangledState() app.EntangledState { return k.c.EntangledState() }

// SetEntangled turns the vault's password on or off; turning it on asks
// for the new password, turning it off asks for nothing. Both are
// ceremonies, and neither ever asks the old password.
func (k *Keys) SetEntangled(on bool) error { return asErr(k.c.SetEntangled(on)) }

// ChangeEntangledPassword replaces the vault's password; a ceremony, and
// the old password is never a field.
func (k *Keys) ChangeEntangledPassword() error { return asErr(k.c.ChangeEntangledPassword()) }

func (k *Keys) RemoveSlot(recipientID string) error { return asErr(k.c.RemoveSlot(recipientID)) }
func (k *Keys) RotateNow() error                    { return asErr(k.c.RotateNow()) }
func (k *Keys) ExportBackup(path string) error      { return asErr(k.c.ExportBackup(path)) }

// RevealRecoveryKey shows a recovery slot's key again, after a protector
// unlock; the digits arrive over the one-time URL, never here.
func (k *Keys) RevealRecoveryKey(recipientID string) error {
	return asErr(k.c.RevealRecoveryKey(recipientID))
}

// SaveRecoveryKey writes the key behind a reveal's handle to a path the
// user chose; the handle is the URL's token, not the digits.
func (k *Keys) SaveRecoveryKey(handle, path string) error {
	return asErr(k.c.SaveRecoveryKey(handle, path))
}

// DropRecoveryKey ends a reveal's handle when its dialog closes.
func (k *Keys) DropRecoveryKey(handle string) error { return asErr(k.c.DropRecoveryKey(handle)) }

// VerifyBackup proves a backup opens with its recovery key, on a copy;
// nothing changes.
func (k *Keys) VerifyBackup(path string) error { return asErr(k.c.VerifyBackup(path)) }

// Settings is the machine-local settings plus the registry's timeouts.
type Settings struct {
	c *app.Core
}

func (s *Settings) Get() app.Settings        { return s.c.GetSettings() }
func (s *Settings) Set(v app.Settings) error { return asErr(s.c.SetSettings(v)) }
