package format

import "fmt"

// Copy names one of the two alternating copies of a superblock or slot region
// (docs/FORMAT.md §4). The write protocol is "write the copy that is not live,
// then flip"; Other gives that copy, and the offset methods give the places to
// write, so a caller never inverts the alternation by hand.
type Copy int

const (
	CopyA Copy = 0
	CopyB Copy = 1
)

// Other returns the inactive copy.
func (c Copy) Other() Copy { return c ^ 1 }

// KeystoreSuperblockOff is where this copy of the keystore superblock lives.
func (c Copy) KeystoreSuperblockOff() uint64 {
	if c == CopyA {
		return KeystoreSuperblockAOff
	}
	return KeystoreSuperblockBOff
}

// SlotRegionOff is where this copy of the slot region lives.
func (c Copy) SlotRegionOff() uint64 {
	if c == CopyA {
		return SlotRegionAOff
	}
	return SlotRegionBOff
}

// ArchiveSuperblockOff is where this copy of the archive superblock lives.
func (c Copy) ArchiveSuperblockOff() uint64 {
	if c == CopyA {
		return ArchiveSuperblockAOff
	}
	return ArchiveSuperblockBOff
}

func (c Copy) String() string {
	if c == CopyA {
		return "A"
	}
	return "B"
}

// SlotRegionCopy maps a superblock's slot_region_off back to the copy it names.
func SlotRegionCopy(off uint64) (Copy, bool) {
	switch off {
	case SlotRegionAOff:
		return CopyA, true
	case SlotRegionBOff:
		return CopyB, true
	}
	return 0, false
}

// pick chooses the live copy: the valid one with the higher seq. It returns the
// winner, which copy it was, the loser's decode error (nil when the loser was
// merely older — a non-nil value means one copy is damaged and the next write,
// which targets exactly that copy, repairs it), and the error when neither copy
// is usable. Two valid copies with equal seq is a corruption, not a tie.
func pick[T any](a, b []byte, decode func([]byte) (*T, error), seq func(*T) uint64) (live *T, which Copy, stale error, err error) {
	sa, errA := decode(a)
	sb, errB := decode(b)
	switch {
	case errA != nil && errB != nil:
		return nil, 0, nil, fmt.Errorf("no valid superblock copy (A: %w; B: %w)", errA, errB)
	case errA != nil:
		return sb, CopyB, errA, nil
	case errB != nil:
		return sa, CopyA, errB, nil
	case seq(sa) > seq(sb):
		return sa, CopyA, nil, nil
	case seq(sb) > seq(sa):
		return sb, CopyB, nil, nil
	default:
		return nil, 0, nil, invalidf("both superblock copies are valid with seq %d", seq(sa))
	}
}
