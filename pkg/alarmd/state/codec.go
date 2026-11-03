// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
)

var (
	ErrCorruptState     = errors.New("state: corrupt runtime state")
	ErrUnsupportedState = errors.New("state: unsupported runtime state")
	ErrStateBudget      = errors.New("state: runtime state budget exceeded")
)

type CodecLimits struct {
	MaxLevels       int
	MaxPoints       int
	MaxEncodedBytes int
}

type Window struct {
	levels       []levelState
	points       []pointState
	requirements map[uint32]LevelRequirement
	changed      bool
}

type levelState struct {
	levelID           uint32
	detectFingerprint [32]byte
}

type pointState struct {
	sourceTime   int64
	recordDigest [16]byte
	valid        []byte
	anomalous    []byte
}

// RuntimeEnvelopeUpperBoundV2 is the shape-only upper bound for the persisted
// Runtime State record, the JSON envelope execution v2 actually writes.
//
// It is computed on the JSON record rather than on the packed blob because
// the two representations are not within a constant factor of each other and
// only the JSON record is what the store holds. The packed blob spends 19 bytes on a point; the JSON record
// spends about 234 for one Level and 353 for two, because it repeats per point
// what the packed form hoists: a 64-character record id whose first 16 bytes
// are all anyone reads, and a 64-character detect fingerprint that is compared
// for equality against the Level's own and then discarded - a value that is
// identical for every point of a Level by construction, since a point whose
// fingerprint differs is an invariant violation.
//
// That difference has a consequence nobody wrote down. MaxValueBytes is the
// codec's byte budget, sized for the packed form, and it is applied unchanged
// to the record that costs 12 to 18 times more per point. So the point ceiling
// the configuration states - 4096, in both the compiler and the codec - cannot
// be reached in the representation in use: the real one is about 2200 points
// for one Level and about 1480 for two. A strategy configured between those
// compiles cleanly and then has every state write refused as budget exceeded,
// per series, silently, for as long as it exists.
//
// Deriving the compile-time ceiling from this function is what keeps the two
// in step. When the record becomes the packed form the arithmetic changes here
// and the stated 4096 becomes reachable again, with nothing else to revisit.
func RuntimeEnvelopeUpperBoundV2(levelCount, pointCount int) (int, error) {
	if levelCount < 0 || pointCount < 0 {
		return 0, fmt.Errorf("state: encoded shape must be non-negative")
	}
	// Per point: the object keys and punctuation, a 64-character record id, and
	// a source time at its widest. Per Level fact inside a point: the keys, a
	// Level id at its widest, a 64-character fingerprint and a result.
	const (
		// The envelope around the history: identity, apply version, digests, the
		// series guard and the keys naming them, each at its widest.
		envelopeOverhead = 4 << 10
		// One Level entry outside the points: three 64-character refs and a time.
		perLevelState    = 512
		perPointFixed    = 126
		perPointPerLevel = 123
	)
	base := envelopeOverhead
	if levelCount > (math.MaxInt-base)/perLevelState {
		return 0, ErrStateBudget
	}
	base += levelCount * perLevelState
	perPoint := perPointFixed
	if levelCount > (math.MaxInt-perPoint)/perPointPerLevel {
		return 0, ErrStateBudget
	}
	perPoint += levelCount * perPointPerLevel
	if pointCount > (math.MaxInt-base)/perPoint {
		return 0, ErrStateBudget
	}
	return base + pointCount*perPoint, nil
}

// MaxRuntimeEnvelopePoints is the most retained points a Plan with this many
// Levels can store before the record crosses valueBytes. It is the compile-time
// ceiling, derived from the representation rather than stated beside it.
func MaxRuntimeEnvelopePoints(levelCount, valueBytes int) (int, error) {
	if levelCount <= 0 || valueBytes <= 0 {
		return 0, fmt.Errorf("state: level count and value budget must be positive")
	}
	low, high := 0, valueBytes
	for low < high {
		mid := (low + high + 1) / 2
		size, err := RuntimeEnvelopeUpperBoundV2(levelCount, mid)
		if err != nil || size > valueBytes {
			high = mid - 1
			continue
		}
		low = mid
	}
	return low, nil
}

// decodeDigest32 reads a digest isSHA256Hex accepts into its 32 bytes, in
// place: the characters are known to be lowercase hexadecimal, so each pair
// is two nibbles and nothing is allocated.
func decodeDigest32(value string) ([32]byte, error) {
	var result [32]byte
	if !isSHA256Hex(value) {
		return result, fmt.Errorf("state: digest must be 64 lowercase hexadecimal characters")
	}
	for index := range result {
		result[index] = lowerHexNibble(value[2*index])<<4 | lowerHexNibble(value[2*index+1])
	}
	return result, nil
}

// lowerHexNibble is the value of one character isSHA256Hex accepted.
func lowerHexNibble(c byte) byte {
	if c <= '9' {
		return c - '0'
	}
	return c - 'a' + 10
}

func appendUvarint(destination []byte, value uint64) []byte {
	var encoded [binary.MaxVarintLen64]byte
	count := binary.PutUvarint(encoded[:], value)
	return append(destination, encoded[:count]...)
}

func consumeUvarint(source []byte) (uint64, []byte, error) {
	value, count := binary.Uvarint(source)
	if count == 0 {
		return 0, nil, fmt.Errorf("%w: truncated varint", ErrCorruptState)
	}
	if count < 0 {
		return 0, nil, fmt.Errorf("%w: overflowing varint", ErrCorruptState)
	}
	return value, source[count:], nil
}

// ALD2 is the framed record: the envelope's scalar fields stay JSON and only
// the history is packed.
//
// The history is the one part that grows with the window, and it is where the
// JSON record spends 234 bytes a point for one Level and 1068 for eight. The
// scalar fields are O(1) plus O(Levels) and are left exactly as they were,
// with their existing encoding and their existing validation: inventing a
// binary encoding for them would buy nothing measurable and would put every
// one of those checks through a second implementation.
const (
	packedFrameMagic         = "ALD2"
	packedFrameSchemaV1 byte = 1
	// packedFrameSchemaV2 marks a frame whose header carries record ids the
	// derivation cannot rebuild. Only records that need the table are written
	// at v2, so a build that knows only v1 goes on reading every other record
	// and refuses these few by name rather than the whole population.
	packedFrameSchemaV2  byte = 2
	packedFrameCodecNone byte = 0
	packedFrameHeaderLen      = 6
)

// PackedFrameUpperBoundV2 is the shape-only upper bound for the framed record.
//
// Per point: a uvarint source-time delta and three bitmaps of one bit per
// Level. Three rather than the packed window's two, because the window records
// only whether a Level had a usable value and this record has to give back the
// fact it was handed - UNAVAILABLE and ERROR are different facts, and a point
// no Level found usable is still a point the result contract reads.
//
// Every point is costed as if its id had to be stored. Most do not - within one
// key the id derives from the series digest and the source time, and a derived
// id is not stored at all - but state written before that derivation existed
// carries ids the decode cannot rebuild, and those go in the header table.
//
// Counted at the worst case rather than at the common one because this is what
// the compile time ceiling is taken from, and a ceiling that admits a Plan the
// write then refuses is the failure the ceiling exists to prevent. Measured:
// one entry costs 90 bytes plus the index digits, and a 4096 point record whose
// history is entirely such ids reaches 76.5% of a 512 KiB budget while this
// function, before counting them, reported a seventh of that.
func PackedFrameUpperBoundV2(levelCount, pointCount int) (int, error) {
	if levelCount < 0 || pointCount < 0 {
		return 0, fmt.Errorf("state: encoded shape must be non-negative")
	}
	// The JSON header, at the widths RuntimeEnvelopeUpperBoundV2 measured it
	// at, plus one 64-character detect fingerprint per Level that the envelope
	// used to repeat on every point.
	const (
		frameOverhead    = 4 + 2 + binary.MaxVarintLen64
		envelopeOverhead = 4 << 10
		perLevelState    = 512 + 80
		// {"index":N,"record_id":"<id>"}, without the index digits. The id
		// width is MaxLegacyRecordIDLength, which the encoder refuses beyond,
		// so this is a bound rather than an assumption about the data.
		perLegacyRecordID = 1 + 8 + 1 + 13 + 1 + MaxLegacyRecordIDLength + 1 + 1
		// The field name and its brackets, once.
		legacyTableOverhead = len(`"legacy_record_ids":[],`)
	)
	bitmapBytes := (levelCount + 7) / 8
	base := frameOverhead + envelopeOverhead + legacyTableOverhead
	if levelCount > (math.MaxInt-base)/perLevelState {
		return 0, ErrStateBudget
	}
	base += levelCount * perLevelState
	perPoint := binary.MaxVarintLen64
	if bitmapBytes > (math.MaxInt-perPoint)/3 {
		return 0, ErrStateBudget
	}
	perPoint += 3 * bitmapBytes
	// The index is at most as wide as the point count it indexes, which is
	// exact rather than a generous constant: a wider allowance would lower the
	// ceiling for every record to pay for digits no record can reach.
	perPoint += perLegacyRecordID + len(strconv.Itoa(max(pointCount, 1)))
	if pointCount > (math.MaxInt-base)/perPoint {
		return 0, ErrStateBudget
	}
	return base + pointCount*perPoint, nil
}

// MaxPackedFramePoints is the most retained points a Plan with this many Levels
// can store before the framed record crosses valueBytes.
//
// The counterpart of MaxRuntimeEnvelopePoints, and the function the compile
// time ceiling reads once the store writes this representation. It must not be
// read before then: it admits windows the JSON record cannot hold, and a Plan
// admitted on this bound while the store still writes the other one compiles
// cleanly and has every state write refused, per series, silently - which is
// the defect decision-019 exists to have removed.
func MaxPackedFramePoints(levelCount, valueBytes int) (int, error) {
	if levelCount <= 0 || valueBytes <= 0 {
		return 0, fmt.Errorf("state: level count and value budget must be positive")
	}
	low, high := 0, valueBytes
	for low < high {
		mid := (low + high + 1) / 2
		size, err := PackedFrameUpperBoundV2(levelCount, mid)
		if err != nil || size > valueBytes {
			high = mid - 1
			continue
		}
		low = mid
	}
	return low, nil
}
