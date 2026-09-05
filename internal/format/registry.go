package format

import "crypto/sha256"

// Registry is the plaintext of the keystore registry (docs/FORMAT.md §7): the
// device's own sync identity, one record per archive with all of its versions,
// and the pinned peers.
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
	Archives       []ArchiveRecord
	Peers          []PeerPin
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
	Versions           []VersionRecord
}

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
	registryVersion  = 1
	minArchiveRecord = 16 + 2 + 2 + 4 + 8 + 16 + 32 + 8 + 8 + 8 + 16 + 4
	minVersionRecord = 16 + WrappedKeySize + NonceSize + 8 + 8 + 1
	minPeerRecord    = 2 + X25519PubSize + 2 + 8 + 8 + 4 + 1
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
	if len(a.Versions) == 0 {
		return invalidf("archive %x has no versions", a.ArchiveID)
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

// Validate checks every record: unknown bits, version consistency, and that
// archive identities and KIDs are unique across the whole registry (§7.2 leans
// on KID uniqueness; a duplicate would make key lookup order-dependent).
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
	return nil
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
	return w.done()
}

// SlotRegionHash is the value the registry stores for an encoded slot region
// (R25): SHA-256 over the region exactly as written.
func SlotRegionHash(encodedRegion []byte) [32]byte { return sha256.Sum256(encodedRegion) }

// VerifySlotRegion compares the live slot region against the registry's
// authenticated hash. A mismatch means the region was modified by someone
// without the Metadata key — a substituted public key, an added record, or a
// spliced-in old region — and must be reported, never silently repaired, and
// never re-wrapped into.
func (g *Registry) VerifySlotRegion(encodedRegion []byte) error {
	if sha256.Sum256(encodedRegion) != g.SlotRegionHash {
		return invalidf("slot region does not match the registry's authenticated hash: the slot region was modified outside the unlocked vault")
	}
	return nil
}

// DecodeRegistry parses a registry plaintext.
func DecodeRegistry(b []byte) (*Registry, error) {
	r := newReader(b, "registry")
	if v := r.u32(); r.err == nil && v != registryVersion {
		return nil, invalidf("registry_version %d unsupported", v)
	}
	g := &Registry{}
	r.fixed(g.DeviceID[:])
	g.ModifiedAt = r.i64()
	r.fixed(g.WrappedIdentityKey[:])
	r.fixed(g.IdentityNonce[:])
	r.fixed(g.SlotRegionHash[:])
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
	if err := r.done(); err != nil {
		return nil, err
	}
	if err := g.Validate(); err != nil {
		return nil, err
	}
	return g, nil
}
