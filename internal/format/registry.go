package format

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
)

// Registry is the plaintext of the keystore registry (docs/FORMAT.md §7): the
// device's own sync identity, one record per archive with all of its versions,
// the pinned peers, and the secrets section.
type Registry struct {
	DeviceID           [16]byte
	ModifiedAt         int64
	WrappedIdentityKey [WrappedKeySize]byte // device identity X25519 private key under KWK_identity
	IdentityNonce      [NonceSize]byte
	// SlotRegionHash is the SHA-256 of the live slot region exactly as written
	// (R25). The slot region is only checksummed; this is the authenticated copy
	// of it, verified after the registry is decrypted and before any rotation
	// re-wraps the VMK into a slot's stored public key.
	SlotRegionHash [32]byte
	// IdleMinutes and AbsoluteMinutes are the session timeouts of DESIGN.md
	// §10 (R37): authenticated here rather than trusted from a settings
	// file; zero means the reader's default, and the reader clamps them.
	IdleMinutes     uint16
	AbsoluteMinutes uint16
	Archives        []ArchiveRecord
	Peers           []PeerPin
	// Secrets is the secrets section (§7.6, R38): every escrowed recovery
	// key, K_P, and every retired VMK, all under one KWK_secrets. Sorted
	// strictly ascending by (Kind, ID), which is what keeps decode and
	// re-encode byte-exact (R21).
	Secrets []SecretRecord
}

// SecretRecord is one record of the secrets section (§7.6, R38): a 32-byte
// secret under KWK_secrets, sealed with the AAD of SecretAAD. What the
// plaintext means is the kind's: K_P, a retired VMK, or a 16-byte recovery key
// followed by sixteen zero bytes.
type SecretRecord struct {
	Kind       SecretKind
	ID         [16]byte
	Nonce      [NonceSize]byte
	Ciphertext [WrappedSecretSize]byte
}

// VMKHistoryID is the id of a vmk_history record (§7.6): the retired
// generation as a little-endian u64 in bytes 0–7, bytes 8–15 zero.
func VMKHistoryID(generation uint64) [16]byte {
	var id [16]byte
	binary.LittleEndian.PutUint64(id[:8], generation)
	return id
}

// HistoryGeneration reports the generation a vmk_history record's id encodes.
// ok is false for any other kind, so a caller cannot read a recovery slot's
// recipient_id as a number by accident.
func (s *SecretRecord) HistoryGeneration() (generation uint64, ok bool) {
	if s.Kind != SecretVMKHistory {
		return 0, false
	}
	return binary.LittleEndian.Uint64(s.ID[:8]), true
}

// validate applies §7.6's shape rules per kind. A kind outside 1–3 fails
// closed (§1); an entangled_key record's id is all zero; a vmk_history
// record's tail is zero and the generation it carries is non-zero, since
// generation 0 never exists (§5). A recovery_escrow record's id is its slot's
// recipient_id and has no shape.
func (s *SecretRecord) validate() error {
	switch s.Kind {
	case SecretRecoveryEscrow:
		return nil
	case SecretEntangledKey:
		if s.ID != zero16 {
			return invalidf("entangled_key secret carries a non-zero id %x", s.ID)
		}
		return nil
	case SecretVMKHistory:
		if !bytes.Equal(s.ID[8:], zero16[8:]) {
			return invalidf("vmk_history secret %x has a non-zero id tail", s.ID)
		}
		if binary.LittleEndian.Uint64(s.ID[:8]) == 0 {
			return invalidf("vmk_history secret encodes generation 0, which never exists")
		}
		return nil
	default:
		return invalidf("secret kind %d unknown", s.Kind)
	}
}

// secretOrder compares two records the way §7.6 orders the section: kind
// first, then id as unsigned bytes.
func secretOrder(a, b *SecretRecord) int {
	switch {
	case a.Kind < b.Kind:
		return -1
	case a.Kind > b.Kind:
		return 1
	}
	return bytes.Compare(a.ID[:], b.ID[:])
}

// ArchiveRecord is one archive with all of its versions (§7.1).
type ArchiveRecord struct {
	ArchiveID          [16]byte
	Name               string // the trusted name (§7.4)
	LastPath           string // a hint, never an identity
	Policy             uint32
	CreatedAt          int64
	CurrentKID         [16]byte
	LastCiphertextHash [32]byte
	LastStoredSize     uint64
	LastWrittenAt      int64
	Revision           uint64
	LastWriter         [16]byte
	// LastSeq is the archive superblock's seq at the last commit this
	// registry recorded — the keyless identity of a copy (R36). HashAtSeq
	// is the LastSeq at which LastCiphertextHash was computed; equal means
	// the hash is current, lower means it is behind by that many commits.
	LastSeq   uint64
	HashAtSeq uint64
	// Description is optional and empty when none: at most MaxDescriptionLen
	// bytes of UTF-8, longer is invalid (§7.1).
	Description string
	// ForgottenAt is zero unless the record was forgotten, in which case it is
	// the modified_at of the write that forgot it — never the raw clock
	// (§7.1, §18.2). A restore sets it back to zero.
	ForgottenAt int64
	Versions    []VersionRecord
}

// Forgotten reports whether this record has been forgotten (§18.2). A
// forgotten record keeps its keys and is listed only on request.
func (a *ArchiveRecord) Forgotten() bool { return a.ForgottenAt != 0 }

// VersionRecord is a key, not a snapshot (§7.2).
type VersionRecord struct {
	KID               [16]byte
	WrappedArchiveKey [WrappedKeySize]byte // under KWK
	WrapNonce         [NonceSize]byte
	CreatedAt         int64
	RetiredAt         int64 // zero while current, non-zero once retired
	State             VersionState
}

// PeerPin is a pinned peer (§7.5). IdentityPubkey is the only thing matched on.
type PeerPin struct {
	IdentityPubkey [X25519PubSize]byte
	DeviceName     string // display only, attacker-chosen
	PairedAt       int64
	LastSeenAt     int64
	Capabilities   uint32
	DeviceClass    DeviceClass
}

const (
	// registryVersion is the only version read or written since Revision 2
	// (§7, §18.2): the version-1 and version-2 readers are gone, so every
	// accepted encoding is canonical (R21).
	registryVersion  = 3
	minArchiveRecord = 16 + 2 + 2 + 4 + 8 + 16 + 32 + 8 + 8 + 8 + 16 + 8 + 8 + 2 + 8 + 4
	minVersionRecord = 16 + WrappedKeySize + NonceSize + 8 + 8 + 1
	minPeerRecord    = 2 + X25519PubSize + 2 + 8 + 8 + 4 + 1
	// minSecretRecord is kind, id, nonce and ciphertext. secret_count has no
	// ceiling of its own: R19's 64 MiB registry and the reader's count() guard
	// already bound what a hostile section can allocate, and a fixed ceiling
	// would eventually make a long-lived vault — one vmk_history record per
	// rotation — unreadable by its own writer.
	minSecretRecord = 1 + 16 + NonceSize + WrappedSecretSize
)

func (v *VersionRecord) validate() error {
	switch v.State {
	case VersionCurrent:
		if v.RetiredAt != 0 {
			return invalidf("current version %x has retired_at set", v.KID)
		}
	case VersionRetired:
		if v.RetiredAt == 0 {
			return invalidf("retired version %x has no retired_at", v.KID)
		}
	default:
		return invalidf("version state %d unknown", v.State)
	}
	return nil
}

func (a *ArchiveRecord) validate(kids map[[16]byte]struct{}) error {
	if a.Policy&^knownPolicyBits != 0 {
		return invalidf("archive %x policy 0x%x carries unknown bits", a.ArchiveID, a.Policy)
	}
	if lvl := PolicyLevel(a.Policy); lvl > PolicyLevelBest {
		// 5–7 are not defined: fail closed (§1) rather than compress at a
		// level this program cannot name.
		return invalidf("archive %x compression level %d is not one of 0–4", a.ArchiveID, lvl)
	}
	if len(a.Versions) == 0 {
		return invalidf("archive %x has no versions", a.ArchiveID)
	}
	if a.HashAtSeq > a.LastSeq {
		return invalidf("archive %x hash_at_seq %d is ahead of last_seq %d", a.ArchiveID, a.HashAtSeq, a.LastSeq)
	}
	if len(a.Description) > MaxDescriptionLen {
		return invalidf("archive %x description is %d bytes, the limit is %d", a.ArchiveID, len(a.Description), MaxDescriptionLen)
	}
	// §18.2 gives the app the retention arithmetic; the format layer only
	// keeps the subtraction from wrapping, as modified_at ≥ 0 does in §5.
	if a.ForgottenAt < 0 {
		return invalidf("archive %x forgotten_at %d is negative", a.ArchiveID, a.ForgottenAt)
	}
	current := 0
	found := false
	for i := range a.Versions {
		v := &a.Versions[i]
		if err := v.validate(); err != nil {
			return err
		}
		if _, dup := kids[v.KID]; dup {
			return invalidf("kid %x appears twice", v.KID)
		}
		kids[v.KID] = struct{}{}
		if v.State == VersionCurrent {
			current++
		}
		if v.KID == a.CurrentKID {
			found = true
			if v.State != VersionCurrent {
				return invalidf("archive %x current_kid points at a retired version", a.ArchiveID)
			}
		}
	}
	if !found {
		return invalidf("archive %x current_kid matches no version", a.ArchiveID)
	}
	if current != 1 {
		return invalidf("archive %x has %d current versions", a.ArchiveID, current)
	}
	return nil
}

func (p *PeerPin) validate() error {
	if p.Capabilities&^knownCapabilities != 0 {
		return invalidf("peer capabilities 0x%x carry unknown bits", p.Capabilities)
	}
	switch p.DeviceClass {
	case DeviceEphemeralSession, DeviceEnrolledPersonal:
	default:
		return invalidf("device_class %d unknown", p.DeviceClass)
	}
	return nil
}

// Validate checks every record: unknown bits, version consistency, that
// archive identities and KIDs are unique across the whole registry (§7.2 leans
// on KID uniqueness; a duplicate would make key lookup order-dependent), and
// that the secrets section is strictly ascending by (kind, id).
func (g *Registry) Validate() error {
	archives := make(map[[16]byte]struct{}, len(g.Archives))
	kids := make(map[[16]byte]struct{}, len(g.Archives))
	for i := range g.Archives {
		if _, dup := archives[g.Archives[i].ArchiveID]; dup {
			return invalidf("archive %x appears twice", g.Archives[i].ArchiveID)
		}
		archives[g.Archives[i].ArchiveID] = struct{}{}
		if err := g.Archives[i].validate(kids); err != nil {
			return err
		}
	}
	for i := range g.Peers {
		if err := g.Peers[i].validate(); err != nil {
			return err
		}
	}
	// §7.6: sorted ascending by kind then by id as unsigned bytes, and no two
	// records sharing a (kind, id). Strictness delivers both in one pass and
	// keeps decode and re-encode byte-exact (R21); with the zero id required
	// of kind 2 it also gives "at most one entangled_key" for free.
	for i := range g.Secrets {
		if err := g.Secrets[i].validate(); err != nil {
			return err
		}
		if i > 0 && secretOrder(&g.Secrets[i-1], &g.Secrets[i]) >= 0 {
			return invalidf("secrets section is not strictly ascending by (kind, id) at record %d", i)
		}
	}
	return nil
}

// Secret returns the record with this kind and id, or nil.
func (g *Registry) Secret(kind SecretKind, id [16]byte) *SecretRecord {
	for i := range g.Secrets {
		if g.Secrets[i].Kind == kind && g.Secrets[i].ID == id {
			return &g.Secrets[i]
		}
	}
	return nil
}

// SecretsOfKind returns every record of one kind, in section order.
func (g *Registry) SecretsOfKind(kind SecretKind) []SecretRecord {
	var out []SecretRecord
	for i := range g.Secrets {
		if g.Secrets[i].Kind == kind {
			out = append(out, g.Secrets[i])
		}
	}
	return out
}

// SetSecret inserts or replaces a record, keeping the section in (kind, id)
// order so that Encode never has to sort and the writer cannot produce a
// section a reader would refuse.
func (g *Registry) SetSecret(rec SecretRecord) {
	for i := range g.Secrets {
		switch c := secretOrder(&g.Secrets[i], &rec); {
		case c == 0:
			g.Secrets[i] = rec
			return
		case c > 0:
			g.Secrets = append(g.Secrets, SecretRecord{})
			copy(g.Secrets[i+1:], g.Secrets[i:])
			g.Secrets[i] = rec
			return
		}
	}
	g.Secrets = append(g.Secrets, rec)
}

// DeleteSecret removes a record and reports whether one was there.
func (g *Registry) DeleteSecret(kind SecretKind, id [16]byte) bool {
	for i := range g.Secrets {
		if g.Secrets[i].Kind == kind && g.Secrets[i].ID == id {
			g.Secrets = append(g.Secrets[:i], g.Secrets[i+1:]...)
			return true
		}
	}
	return false
}

// CheckEntangleAgreement applies §7.6's cross-check against the slot region
// header: exactly one entangled_key record when the header's entangle is 1,
// none when it is 0. DecodeRegistry does not call it — the format layer never
// sees the slot region — so internal/keystore calls it beside VerifySlotRegion
// (R25, R38), at unlock and on every write that lands a header.
func (g *Registry) CheckEntangleAgreement(entangle bool) error {
	n := 0
	for i := range g.Secrets {
		if g.Secrets[i].Kind == SecretEntangledKey {
			n++
		}
	}
	switch {
	case entangle && n != 1:
		return invalidf("slot region header says entangle 1 yet the registry keeps %d entangled_key records", n)
	case !entangle && n != 0:
		return invalidf("slot region header says entangle 0 yet the registry keeps %d entangled_key records", n)
	}
	return nil
}

// PurgeForgotten drops every forgotten archive record whose retention has run
// out and returns what went. modifiedAt is the modified_at of the write doing
// the dropping, never the raw clock (§18.2): a record forgotten at T is
// dropped only by a write whose own modified_at exceeds T by more than
// ForgottenRetentionSeconds, and a forgotten_at ahead of modifiedAt — a stamp
// from a faster clock — is treated as freshly forgotten rather than as
// overdue. Pure: the trigger and the confirmation before it are the app's
// (APP.md §13).
func (g *Registry) PurgeForgotten(modifiedAt int64) []ArchiveRecord {
	var dropped []ArchiveRecord
	kept := g.Archives[:0]
	for _, a := range g.Archives {
		if a.Forgotten() && modifiedAt > a.ForgottenAt && modifiedAt-a.ForgottenAt > ForgottenRetentionSeconds {
			dropped = append(dropped, a)
			continue
		}
		kept = append(kept, a)
	}
	if len(dropped) > 0 {
		g.Archives = kept
	}
	return dropped
}

// Encode returns the registry plaintext.
func (g *Registry) Encode() ([]byte, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	w := &writer{b: make([]byte, 0, 256+len(g.Archives)*256)}
	w.u32(registryVersion)
	w.fixed(g.DeviceID[:])
	w.i64(g.ModifiedAt)
	w.fixed(g.WrappedIdentityKey[:])
	w.fixed(g.IdentityNonce[:])
	w.fixed(g.SlotRegionHash[:])
	w.u16(g.IdleMinutes)
	w.u16(g.AbsoluteMinutes)
	w.u32(uint32(len(g.Archives)))
	for i := range g.Archives {
		a := &g.Archives[i]
		w.fixed(a.ArchiveID[:])
		w.str(a.Name)
		w.str(a.LastPath)
		w.u32(a.Policy)
		w.i64(a.CreatedAt)
		w.fixed(a.CurrentKID[:])
		w.fixed(a.LastCiphertextHash[:])
		w.u64(a.LastStoredSize)
		w.i64(a.LastWrittenAt)
		w.u64(a.Revision)
		w.fixed(a.LastWriter[:])
		w.u64(a.LastSeq)
		w.u64(a.HashAtSeq)
		w.str(a.Description)
		w.i64(a.ForgottenAt)
		w.u32(uint32(len(a.Versions)))
		for j := range a.Versions {
			v := &a.Versions[j]
			w.fixed(v.KID[:])
			w.fixed(v.WrappedArchiveKey[:])
			w.fixed(v.WrapNonce[:])
			w.i64(v.CreatedAt)
			w.i64(v.RetiredAt)
			w.u8(uint8(v.State))
		}
	}
	w.u32(uint32(len(g.Peers)))
	for i := range g.Peers {
		p := &g.Peers[i]
		w.bytes16(p.IdentityPubkey[:])
		w.str(p.DeviceName)
		w.i64(p.PairedAt)
		w.i64(p.LastSeenAt)
		w.u32(p.Capabilities)
		w.u8(uint8(p.DeviceClass))
	}
	w.u32(uint32(len(g.Secrets)))
	for i := range g.Secrets {
		s := &g.Secrets[i]
		w.u8(uint8(s.Kind))
		w.fixed(s.ID[:])
		w.fixed(s.Nonce[:])
		w.fixed(s.Ciphertext[:])
	}
	return w.done()
}

// SlotRegionHash is the value the registry stores for an encoded slot region
// (R25): SHA-256 over the region exactly as written.
func SlotRegionHash(encodedRegion []byte) [32]byte { return sha256.Sum256(encodedRegion) }

// VerifySlotRegion compares the live slot region against the registry's
// authenticated hash. A mismatch means the region was modified by someone
// without the Metadata key — a substituted public key, an added record, an
// edited header, or a spliced-in old region — and must be reported, never
// silently repaired, and never re-wrapped into.
func (g *Registry) VerifySlotRegion(encodedRegion []byte) error {
	if sha256.Sum256(encodedRegion) != g.SlotRegionHash {
		return invalidf("slot region does not match the registry's authenticated hash: the slot region was modified outside the unlocked vault")
	}
	return nil
}

// DecodeRegistry parses a registry plaintext.
func DecodeRegistry(b []byte) (*Registry, error) {
	r := newReader(b, "registry")
	version := r.u32()
	if r.err == nil && version != registryVersion {
		return nil, invalidf("registry_version %d unsupported", version)
	}
	g := &Registry{}
	r.fixed(g.DeviceID[:])
	g.ModifiedAt = r.i64()
	r.fixed(g.WrappedIdentityKey[:])
	r.fixed(g.IdentityNonce[:])
	r.fixed(g.SlotRegionHash[:])
	g.IdleMinutes = r.u16()
	g.AbsoluteMinutes = r.u16()
	na := r.count(r.u32(), minArchiveRecord)
	g.Archives = make([]ArchiveRecord, 0, na)
	for i := 0; i < na && r.err == nil; i++ {
		var a ArchiveRecord
		r.fixed(a.ArchiveID[:])
		a.Name = r.str()
		a.LastPath = r.str()
		a.Policy = r.u32()
		a.CreatedAt = r.i64()
		r.fixed(a.CurrentKID[:])
		r.fixed(a.LastCiphertextHash[:])
		a.LastStoredSize = r.u64()
		a.LastWrittenAt = r.i64()
		a.Revision = r.u64()
		r.fixed(a.LastWriter[:])
		a.LastSeq = r.u64()
		a.HashAtSeq = r.u64()
		a.Description = r.str()
		a.ForgottenAt = r.i64()
		nv := r.count(r.u32(), minVersionRecord)
		a.Versions = make([]VersionRecord, 0, nv)
		for j := 0; j < nv && r.err == nil; j++ {
			var v VersionRecord
			r.fixed(v.KID[:])
			r.fixed(v.WrappedArchiveKey[:])
			r.fixed(v.WrapNonce[:])
			v.CreatedAt = r.i64()
			v.RetiredAt = r.i64()
			v.State = VersionState(r.u8())
			a.Versions = append(a.Versions, v)
		}
		g.Archives = append(g.Archives, a)
	}
	np := r.count(r.u32(), minPeerRecord)
	g.Peers = make([]PeerPin, 0, np)
	for i := 0; i < np && r.err == nil; i++ {
		var p PeerPin
		if key := r.bytes16(); len(key) == X25519PubSize {
			copy(p.IdentityPubkey[:], key)
		} else if r.err == nil {
			r.invalidf("peer identity pubkey is %d bytes, want %d", len(key), X25519PubSize)
		}
		p.DeviceName = r.str()
		p.PairedAt = r.i64()
		p.LastSeenAt = r.i64()
		p.Capabilities = r.u32()
		p.DeviceClass = DeviceClass(r.u8())
		g.Peers = append(g.Peers, p)
	}
	ns := r.count(r.u32(), minSecretRecord)
	if ns > 0 {
		g.Secrets = make([]SecretRecord, 0, ns) // nil when the section is empty
	}
	for i := 0; i < ns && r.err == nil; i++ {
		var s SecretRecord
		s.Kind = SecretKind(r.u8())
		r.fixed(s.ID[:])
		r.fixed(s.Nonce[:])
		r.fixed(s.Ciphertext[:])
		g.Secrets = append(g.Secrets, s)
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	if err := g.Validate(); err != nil {
		return nil, err
	}
	return g, nil
}
