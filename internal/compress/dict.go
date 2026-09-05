package compress

import (
	"fmt"

	"github.com/klauspost/compress/zstd"
)

const (
	// DefaultDictSize is the dictionary content size BuildDict aims for when
	// the caller has no opinion: reference zstd's own default of 112640
	// bytes.
	DefaultDictSize = 112640

	// MaxDictContent bounds BuildDict's size argument: the dictionary is the
	// content plus its header and entropy tables, which must still fit in
	// MaxDictSize.
	MaxDictContent = MaxDictSize - 64<<10

	// maxTrainingSample bounds what one sample contributes to the entropy
	// tables. The library's trainer encodes each sample as a single block
	// into a history buffer whose capacity is the content plus one
	// 128 KiB block (at least 256 KiB in all), and a longer sample makes it
	// shift that history by a negative offset and panic. 128 KiB of any
	// file is ample training data; the whole sample stays eligible as
	// content.
	maxTrainingSample = 128 << 10
)

// DictID validates dict as a zstd dictionary the library can load and
// returns its ID, parsing the whole dictionary to do it. A dictionary longer
// than MaxDictSize, with ID 0, or with tables that do not parse is
// ErrParams.
func DictID(dict []byte) (uint32, error) {
	if len(dict) > MaxDictSize {
		return 0, fmt.Errorf("%w: dictionary of %d bytes exceeds %d", ErrParams, len(dict), MaxDictSize)
	}
	d, err := zstd.InspectDictionary(dict)
	if err != nil {
		return 0, fmt.Errorf("%w: dictionary: %v", ErrParams, err)
	}
	return d.ID(), nil
}

// BuildDict trains a dictionary from samples — files of the kind the archive
// will hold many of — for streams written at level. id becomes the
// dictionary's ID, must be non-zero, and is what frames reference; the
// archive layer chooses it so that a rebuilt dictionary gets a new one,
// since a reader given a different dictionary under the same ID fails only
// after decoding. size bounds the dictionary's content, 0 meaning
// DefaultDictSize and at most MaxDictContent; the result is somewhat larger,
// the entropy tables being on top.
//
// Content is chosen from every other sample — the first, third, fifth… —
// by giving each an equal share of size from its head and handing what
// short samples leave over to the longer ones; the samples left out are
// what gives the entropy tables literal statistics, since a sample that is
// in the content verbatim compresses to matches alone. A single sample
// contributes its first half as content for the same reason. All samples
// train the tables, the first 128 KiB of each. Samples shorter than eight
// bytes are ignored; at least one longer one is required. Samples too
// uniform to yield any literal — identical copies, or one sample that is
// pure repetition — are ErrParams.
func BuildDict(samples [][]byte, id uint32, level Level, size int) ([]byte, error) {
	if id == 0 {
		return nil, fmt.Errorf("%w: dictionary ID 0", ErrParams)
	}
	zl, ok := level.zstd()
	if !ok {
		return nil, fmt.Errorf("%w: level %d", ErrParams, uint8(level))
	}
	if size == 0 {
		size = DefaultDictSize
	}
	if size < 8 || size > MaxDictContent {
		return nil, fmt.Errorf("%w: dictionary content size %d not in [8, %d]", ErrParams, size, MaxDictContent)
	}
	var usable, training [][]byte
	for _, s := range samples {
		if len(s) >= 8 {
			usable = append(usable, s)
			training = append(training, s[:min(len(s), maxTrainingSample)])
		}
	}
	if len(usable) == 0 {
		return nil, fmt.Errorf("%w: no sample of at least 8 bytes", ErrParams)
	}

	var content [][]byte
	if len(usable) == 1 {
		content = [][]byte{usable[0][:len(usable[0])/2]}
	} else {
		for i := 0; i < len(usable); i += 2 {
			content = append(content, usable[i])
		}
	}
	dict, err := zstd.BuildDict(zstd.BuildDictOptions{
		ID:       id,
		Contents: training,
		History:  selectHistory(content, size),
		Level:    zl,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: building dictionary: %v", ErrParams, err)
	}
	got, err := DictID(dict)
	if err != nil {
		return nil, fmt.Errorf("%w: built dictionary rejected: %v", ErrParams, err)
	}
	if got != id {
		return nil, fmt.Errorf("%w: built dictionary has ID %d, want %d", ErrParams, got, id)
	}
	return dict, nil
}

// selectHistory picks at most size bytes of dictionary content from the
// samples: an equal share of each sample's head, then whatever the short
// samples could not use, spread over the longer ones in order.
func selectHistory(samples [][]byte, size int) []byte {
	total := 0
	for _, s := range samples {
		total += len(s)
	}
	if total <= size {
		out := make([]byte, 0, total)
		for _, s := range samples {
			out = append(out, s...)
		}
		return out
	}
	take := make([]int, len(samples))
	budget := size
	// Rounds: each gives every unsatisfied sample an equal share of what is
	// left, until the budget is gone or every sample is exhausted.
	for budget > 0 {
		open := 0
		for i, s := range samples {
			if take[i] < len(s) {
				open++
			}
		}
		if open == 0 {
			break
		}
		share := max(budget/open, 1)
		progressed := false
		for i, s := range samples {
			if budget == 0 {
				break
			}
			room := len(s) - take[i]
			if room == 0 {
				continue
			}
			k := min(share, room, budget)
			take[i] += k
			budget -= k
			progressed = progressed || k > 0
		}
		if !progressed {
			break
		}
	}
	out := make([]byte, 0, size)
	for i, s := range samples {
		out = append(out, s[:take[i]]...)
	}
	return out
}
