package app

import (
	"errors"
	"os"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// Merge records (APP.md §13). Beside Import — which replaces the vault kept
// here — Import records… opens a backup or a vault over a staged copy, proves
// it, and offers its archive records one by one. Nothing of the incoming file
// is installed and no bare archive key crosses into this package: the
// keystore re-wraps every version key under this vault's KWK before the
// records are handed over (FORMAT.md §18.2, A.1).

// incomingSet is one merge handle: the converted records and the rows the
// dialog shows. It holds archive keys — wrapped under this vault's KWK, the
// same wrapping the registry itself uses — so a lock trigger drops it.
type incomingSet struct {
	path       string
	vaultID    [16]byte
	generation uint64
	modifiedAt int64
	openedAt   uint64
	recs       map[[16]byte]format.ArchiveRecord
	rows       []IncomingRecord
}

// InspectRecords opens another keystore file's registry for a merge (APP.md
// §13): a ceremony for THIS vault's VMK by any way in — the VMK is what
// derives KWK_secrets and so the retired VMKs — then a trial of the current
// VMK and every vmk_history VMK against the incoming registry. A file that
// opens that way needs no key from the user; anything else asks for that
// file's own recovery key, and a file no VMK and no key opens is reported as
// vault.not_this_vault, never as corruption. The ceremony ends at StepRecords
// with the handle in SlotLabel and the page then calls IncomingRecords.
//
// It reads a staged copy and writes nothing — to either file — so it never
// publishes an unlock and therefore never purges (§13, "The purge has one
// trigger"), and it runs on a tampered vault for the same reason the reveal
// does: R25's freeze is over the mutating calls and the export (APP.md §13,
// amendment A.5), and a merge is a way out rather than a change.
func (c *Core) InspectRecords(path string) *Error {
	if path == "" {
		return coded(CodeParams)
	}
	if _, err := os.Stat(path); err != nil {
		return c.fileError("inspect records", err)
	}
	return c.beginWithVMK("records", true, func(cer *ceremony, unl *keystore.Unlocked) error {
		staged, serr := c.stage(path, c.inspectName())
		if serr != nil {
			return c.fileError("inspect records", serr)
		}
		defer os.Remove(staged)
		other, err := cer.openVault(staged)
		if err != nil {
			return err
		}
		defer other.Close()
		// The key that unlocked this vault is not needed any more; a
		// recovery key asked for below is the incoming file's own.
		cer.releaseCard()
		if err := cer.check(); err != nil {
			return err
		}
		fr, err := unl.OpenForeign(other)
		if errors.Is(err, keystore.ErrForeign) {
			fr, err = cer.foreignByItsOwnKey(unl, other)
		}
		if err != nil {
			if errors.Is(err, keystore.ErrForeign) {
				return &parkAt{StepFailed, CodeNotThisVault}
			}
			if errors.Is(err, format.ErrInvalid) {
				// A VMK opened the file and the plaintext did not decode:
				// a damaged keystore, not a damaged archive (APP.md §13),
				// the same remapping the reveal makes.
				return &parkAt{StepFailed, CodeVaultInvalid}
			}
			return err
		}
		if fr.Tampered != nil {
			// Reported, never a refusal: nothing in that file is unlocked
			// and every field comes from its authenticated registry.
			c.log("inspect records: %s fails its own region hash: %v", path, fr.Tampered)
		}
		set := c.buildIncomingSet(path, fr)
		handle := newToken()
		c.mu.Lock()
		if c.vault.incoming == nil {
			c.vault.incoming = map[string]*incomingSet{}
		}
		c.vault.incoming[handle] = set
		c.mu.Unlock()
		cer.set(func(s *CeremonyState) { s.Step, s.SlotLabel = StepRecords, handle })
		return nil
	})
}

// foreignByItsOwnKey asks for the incoming file's own recovery key and reads
// its registry through it: another vault, or a backup from a generation this
// vault no longer keeps. A file with no recovery slot is not asked for one.
func (cer *ceremony) foreignByItsOwnKey(unl *keystore.Unlocked, other *keystore.Keystore) (*keystore.ForeignRegistry, error) {
	recovery := false
	for _, s := range other.Slots() {
		if s.Type == format.SlotRecovery {
			recovery = true
		}
	}
	if !recovery {
		return nil, keystore.ErrForeign
	}
	rk, err := cer.askRecoveryKey("")
	if err != nil {
		return nil, err
	}
	own, err := cer.unlockFile(other, keystore.RecoveryCredential{Key: rk}, nil)
	if err != nil {
		return nil, err
	}
	defer own.Close()
	return unl.OpenForeignUnlocked(own)
}

// buildIncomingSet classifies every incoming record against the registry as
// it stands now. The rows are what the dialog shows; the merge itself is
// applied again against the live registry when it runs.
//
// What the registry is asked for is taken under the state mutex and the rows
// are classified with it released: the work is the incoming file's size times
// this vault's, over data an attacker chose, and a lock trigger must still be
// answerable while it runs (APP.md §5, §13).
func (c *Core) buildIncomingSet(path string, fr *keystore.ForeignRegistry) *incomingSet {
	set := &incomingSet{
		path: path, vaultID: fr.VaultID, generation: fr.Generation,
		modifiedAt: fr.ModifiedAt, openedAt: fr.OpenedAt,
		recs: map[[16]byte]format.ArchiveRecord{}, rows: []IncomingRecord{},
	}
	c.mu.Lock()
	var locals map[[16]byte]format.ArchiveRecord
	var owners kidOwners
	if sess, e := c.sessionLocked(); e == nil {
		g := sess.Registry()
		locals = make(map[[16]byte]format.ArchiveRecord, len(g.Archives))
		for i := range g.Archives {
			locals[g.Archives[i].ArchiveID] = g.Archives[i]
		}
		owners = ownersOf(g)
	}
	c.mu.Unlock()
	for i := range fr.Registry.Archives {
		inc := fr.Registry.Archives[i]
		set.recs[inc.ArchiveID] = inc
		var local *format.ArchiveRecord
		if a, held := locals[inc.ArchiveID]; held {
			local = &a
		}
		set.rows = append(set.rows, incomingRow(local, owners, inc))
	}
	return set
}

// incomingRow is the classifier of APP.md §13: a record the vault already has
// whole is skipped and greyed, a known archive that gains something is a
// version row, a new archive_id is added, and a record held here as forgotten
// is listed unticked — ticking it is an explicit Restore. local is the record
// this vault holds under the same archive_id, nil when it holds none, and
// owners is the one kid index the whole classification shares.
func incomingRow(local *format.ArchiveRecord, owners kidOwners, inc format.ArchiveRecord) IncomingRecord {
	r := IncomingRecord{
		ArchiveID: hexID(inc.ArchiveID), Name: inc.Name, Description: inc.Description,
		CreatedAt: inc.CreatedAt, Versions: len(inc.Versions), Differs: []Difference{},
	}
	switch {
	case local == nil:
		if _, ok := takeableVersions(inc, owners); !ok {
			// Its current key is a kid this vault already holds under
			// another archive. A kid is unique across the registry
			// (FORMAT.md §7.1), so the record cannot be added here: the row
			// is shown and left unticked rather than failing the write.
			r.Action, r.Ticked = "skip", false
			return r
		}
		r.Action, r.Ticked = "add", true
		return r
	case local.Forgotten():
		r.Action, r.Ticked, r.ForgottenAt = "forgotten", false, local.ForgottenAt
		r.Differs = differences(*local, inc)
		return r
	}
	r.Differs = differences(*local, inc)
	if _, changed := mergeApply(*local, inc, owners, 0); changed {
		r.Action, r.Ticked = "version", true
	} else {
		r.Action, r.Ticked = "skip", false
	}
	return r
}

// kidOwners is every kid the registry holds and the archive holding it. A kid
// is unique across the whole registry (FORMAT.md §7.1), so an incoming version
// whose kid belongs to another archive here is not taken. One index serves the
// whole classification and the whole merge write, rather than one map per
// incoming record over the entire local registry.
type kidOwners map[[16]byte][16]byte

// ownersOf indexes the registry's kids.
func ownersOf(g *registry) kidOwners {
	o := make(kidOwners)
	if g == nil {
		return o
	}
	for i := range g.Archives {
		a := &g.Archives[i]
		for j := range a.Versions {
			o[a.Versions[j].KID] = a.ArchiveID
		}
	}
	return o
}

// heldElsewhere reports whether kid belongs to an archive other than by.
func (o kidOwners) heldElsewhere(kid, by [16]byte) bool {
	a, held := o[kid]
	return held && a != by
}

// note records the kids a merged record now holds, so a second record taken in
// the same write cannot claim one of them (FORMAT.md §7.1).
func (o kidOwners) note(a format.ArchiveRecord) {
	for i := range a.Versions {
		o[a.Versions[i].KID] = a.ArchiveID
	}
}

// takeableVersions is the version list a new record is added with: every
// incoming version whose kid this vault does not already hold elsewhere. A kid
// is unique across the whole registry (FORMAT.md §7.1), so a colliding one is
// dropped exactly as mergeApply drops it for a record already held. ok is
// false when the record cannot be added at all — its current version is one of
// the colliding ones, and a record needs exactly one current version — in
// which case that one row is left out rather than the whole merge write being
// refused for a file that names a kid of ours.
func takeableVersions(inc format.ArchiveRecord, owners kidOwners) ([]format.VersionRecord, bool) {
	out := make([]format.VersionRecord, 0, len(inc.Versions))
	for i := range inc.Versions {
		if owners.heldElsewhere(inc.Versions[i].KID, inc.ArchiveID) {
			if inc.Versions[i].KID == inc.CurrentKID {
				return nil, false
			}
			continue
		}
		out = append(out, inc.Versions[i])
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// mergeApply is the merge of one record, applied to a copy of the local one,
// and whether it changed anything (APP.md §13). Version lists union; a field
// the local record leaves empty is filled; a field the two hold differently
// keeps the local value, except always_require_full_auth, which is set if
// either side has it and cleared by neither; the fields that describe a copy
// — last_path, last_seq, hash_at_seq, last_ciphertext_hash, last_stored_size,
// last_written_at — are the other vault's observation of its own file and are
// never taken. at is the merging write's own modified_at, which retires an
// incoming version that was current there and is not current here.
func mergeApply(local, inc format.ArchiveRecord, owners kidOwners, at int64) (format.ArchiveRecord, bool) {
	out := local
	out.Versions = append([]format.VersionRecord(nil), local.Versions...)
	changed := false
	have := map[[16]byte]bool{}
	for i := range out.Versions {
		have[out.Versions[i].KID] = true
	}
	for i := range inc.Versions {
		v := inc.Versions[i]
		if have[v.KID] || owners.heldElsewhere(v.KID, local.ArchiveID) {
			continue
		}
		if v.KID != out.CurrentKID && v.State == format.VersionCurrent {
			// One current version per record: what is current there is a
			// retired key here, kept because it still opens older copies.
			v.State, v.RetiredAt = format.VersionRetired, at
			if v.RetiredAt == 0 {
				v.RetiredAt = v.CreatedAt
			}
		}
		out.Versions = append(out.Versions, v)
		have[v.KID] = true
		changed = true
	}
	if out.Name == "" && inc.Name != "" {
		out.Name, changed = inc.Name, true
	}
	if out.Description == "" && inc.Description != "" {
		out.Description, changed = inc.Description, true
	}
	if out.CreatedAt == 0 && inc.CreatedAt != 0 {
		out.CreatedAt, changed = inc.CreatedAt, true
	}
	if inc.Policy&format.PolicyAlwaysRequireFullAuth != 0 && out.Policy&format.PolicyAlwaysRequireFullAuth == 0 {
		out.Policy |= format.PolicyAlwaysRequireFullAuth
		changed = true
	}
	return out, changed
}

// differences are the fields the two hold differently, for the row's note:
// the local value is kept and the other vault's is shown.
func differences(local, inc format.ArchiveRecord) []Difference {
	d := []Difference{}
	if local.Name != "" && inc.Name != "" && local.Name != inc.Name {
		d = append(d, Difference{Field: "name", Theirs: inc.Name})
	}
	if local.Description != "" && inc.Description != "" && local.Description != inc.Description {
		d = append(d, Difference{Field: "description", Theirs: inc.Description})
	}
	// always_require_full_auth is unioned rather than kept, so it is not a
	// difference the row has to explain.
	lp := local.Policy &^ format.PolicyAlwaysRequireFullAuth
	ip := inc.Policy &^ format.PolicyAlwaysRequireFullAuth
	if lp != ip {
		d = append(d, Difference{Field: "policy", Theirs: policyText(ip)})
	}
	return d
}

// policyText renders the policy bits a row reports, display only.
func policyText(p uint32) string {
	s := ""
	add := func(name string) {
		if s != "" {
			s += ", "
		}
		s += name
	}
	if p&format.PolicyHidden != 0 {
		add("hidden")
	}
	if p&format.PolicyNoCompression != 0 {
		add("no compression")
	}
	if s == "" {
		return "none"
	}
	return s
}

// IncomingRecords is the record list behind a merge handle. A handle that was
// consumed, discarded or dropped by a lock trigger is ceremony.stale_prompt,
// as a dead reveal handle is.
func (c *Core) IncomingRecords(handle string) ([]IncomingRecord, *Error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	set := c.vault.incoming[handle]
	if set == nil {
		return nil, coded(CodeStalePrompt)
	}
	out := make([]IncomingRecord, len(set.rows))
	copy(out, set.rows)
	return out, nil
}

// DiscardRecords ends a merge handle: the dialog closed.
func (c *Core) DiscardRecords(handle string) *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vault.incoming[handle] == nil {
		return coded(CodeStalePrompt)
	}
	delete(c.vault.incoming, handle)
	return nil
}

// MergeRecords takes the ticked records into this vault's registry: an
// ordinary registry write on the session, no ceremony (APP.md §13). Refused
// with vault.archives_open while any archive is open, ceremony.in_progress
// while a ceremony runs and op.in_progress while a save, verify, compact or
// rotation runs. Every record it changes is written with
// revision = max(local, incoming) + 1 and this vault's device_id as
// last_writer, so a merge in the other direction sees a descendant rather
// than a rival (SYNC.md §5).
func (c *Core) MergeRecords(handle string, ids []string) *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	set := c.vault.incoming[handle]
	if set == nil {
		return coded(CodeStalePrompt)
	}
	if len(c.archives) > 0 {
		return coded(CodeArchivesOpen)
	}
	if c.cer != nil {
		return coded(CodeCeremonyRunning)
	}
	for _, o := range c.ops {
		if !o.finished {
			return coded(CodeOpRunning)
		}
	}
	want := make([][16]byte, 0, len(ids))
	for _, s := range ids {
		id, ok := parseID(s)
		if !ok {
			return coded(CodeParams)
		}
		if _, held := set.recs[id]; !held {
			return coded(CodeArchiveNotFound)
		}
		want = append(want, id)
	}
	// A ticked record the vault holds as forgotten is restored, which is the
	// one merge path that may touch a forgotten record (A.2).
	wrote := false
	e := c.updateRegistryAtLocked(true, func(g *registry, at int64) error {
		// One kid index for the whole write, kept current as records are
		// taken so that two rows cannot claim the same kid.
		owners := ownersOf(g)
		for _, id := range want {
			inc := set.recs[id]
			local := findRecord(g, id)
			if local == nil {
				versions, ok := takeableVersions(inc, owners)
				if !ok {
					// A crafted file cannot make the whole write fail
					// validation: this row is left out and the rest go in.
					c.log("merge: archive %s not taken: its current kid is already held here", hexID(id))
					continue
				}
				add := inc
				add.Versions = versions
				// The other vault's decision to forget is not imported: the
				// record was ticked to be brought in.
				add.ForgottenAt = 0
				add.Revision = inc.Revision + 1
				add.LastWriter = g.DeviceID
				g.Archives = append(g.Archives, add)
				owners.note(add)
				wrote = true
				continue
			}
			merged, changed := mergeApply(*local, inc, owners, at)
			restored := local.Forgotten()
			if restored {
				merged.ForgottenAt = 0
			}
			if !changed && !restored {
				continue
			}
			merged.Revision = maxRevision(local.Revision, inc.Revision) + 1
			merged.LastWriter = g.DeviceID
			*local = merged
			owners.note(merged)
			wrote = true
		}
		if !wrote {
			// Every ticked row was already here: no commit, no restamp.
			return errNoChange
		}
		return nil
	})
	if e != nil {
		return e
	}
	delete(c.vault.incoming, handle)
	go c.emitArchivesChanged()
	go c.emitState()
	return nil
}

func maxRevision(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
