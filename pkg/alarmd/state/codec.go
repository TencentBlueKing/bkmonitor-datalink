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
	"encoding/hex"
	"errors"
	"fmt"
	"math"
)

var (
	ErrCorruptState     = errors.New("state: corrupt runtime state")
	ErrUnsupportedState = errors.New("state: unsupported runtime state")
	ErrStateBudget      = errors.New("state: runtime state budget exceeded")
)

const (
	stateBlobMagic            = "ALD1"
	stateBlobSchemaV1    byte = 1
	stateBlobCodecNone   byte = 0
	stateBlobHeaderBytes      = 6
)

type CodecLimits struct {
	MaxLevels       int
	MaxPoints       int
	MaxEncodedBytes int
}

type Codec struct {
	limits CodecLimits
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

func NewCodec(limits CodecLimits) (*Codec, error) {
	if limits.MaxLevels <= 0 || limits.MaxPoints <= 0 || limits.MaxEncodedBytes < stateBlobHeaderBytes {
		return nil, fmt.Errorf("state: codec limits must be positive")
	}
	return &Codec{limits: limits}, nil
}

// AdmitShape returns the conservative NONE_V1 encoded upper bound for one
// packed key after validating configured level, point, and byte limits. M3/M7
// use it before event output; Encode still validates the exact bytes.
func (codec *Codec) AdmitShape(levelCount, pointCount int) (int, error) {
	if codec == nil || levelCount < 0 || pointCount < 0 {
		return 0, fmt.Errorf("state: codec and non-negative shape are required")
	}
	if levelCount > codec.limits.MaxLevels || pointCount > codec.limits.MaxPoints {
		return 0, ErrStateBudget
	}
	upperBound, err := PackedEncodedUpperBoundV1(levelCount, pointCount)
	if err != nil {
		return 0, err
	}
	if upperBound > codec.limits.MaxEncodedBytes {
		return 0, ErrStateBudget
	}
	return upperBound, nil
}

// AdmitWindow lets M7 validate the actual post-overlay shape before emitting
// events without exposing mutable Window internals.
func (codec *Codec) AdmitWindow(window *Window) (int, error) {
	if window == nil {
		return 0, fmt.Errorf("state: window is required")
	}
	return codec.AdmitShape(len(window.levels), len(window.points))
}

// PackedEncodedUpperBoundV1 calculates a shape-only upper bound without a
// Codec instance. Multiple Levels sharing one evaluation grid pass the maximum
// retained position count, not the sum of per-Level retention counts.
func PackedEncodedUpperBoundV1(levelCount, pointCount int) (int, error) {
	if levelCount < 0 || pointCount < 0 {
		return 0, fmt.Errorf("state: encoded shape must be non-negative")
	}
	bitmapBytes := (levelCount + 7) / 8
	base := stateBlobHeaderBytes + uvarintBytes(uint64(levelCount)) + uvarintBytes(uint64(pointCount))
	perLevel := binary.MaxVarintLen32 + 32
	perPoint := binary.MaxVarintLen64 + 16 + bitmapBytes*2
	if levelCount > (math.MaxInt-base)/perLevel {
		return 0, ErrStateBudget
	}
	base += levelCount * perLevel
	if pointCount > (math.MaxInt-base)/perPoint {
		return 0, ErrStateBudget
	}
	return base + pointCount*perPoint, nil
}

func (codec *Codec) Encode(window *Window) ([]byte, error) {
	if codec == nil || window == nil {
		return nil, fmt.Errorf("%w: codec and window are required", ErrCorruptState)
	}
	upperBound, err := codec.AdmitWindow(window)
	if err != nil {
		return nil, err
	}
	bitmapBytes := (len(window.levels) + 7) / 8
	buffer := make([]byte, 0, upperBound)
	buffer = append(buffer, stateBlobMagic...)
	buffer = append(buffer, stateBlobSchemaV1, stateBlobCodecNone)
	buffer = appendUvarint(buffer, uint64(len(window.levels)))
	var previousLevel uint32
	for index, level := range window.levels {
		if level.levelID == 0 || (index > 0 && level.levelID <= previousLevel) {
			return nil, fmt.Errorf("%w: Levels must be sorted and unique", ErrCorruptState)
		}
		buffer = appendUvarint(buffer, uint64(level.levelID))
		buffer = append(buffer, level.detectFingerprint[:]...)
		previousLevel = level.levelID
	}
	buffer = appendUvarint(buffer, uint64(len(window.points)))
	var previousTime int64
	for index, point := range window.points {
		if point.sourceTime < 0 || (index > 0 && point.sourceTime <= previousTime) {
			return nil, fmt.Errorf("%w: points must be sorted by unique non-negative source time", ErrCorruptState)
		}
		if len(point.valid) != bitmapBytes || len(point.anomalous) != bitmapBytes || !anyBit(point.valid) ||
			!validPointBitmaps(point.valid, point.anomalous, len(window.levels)) {
			return nil, fmt.Errorf("%w: invalid point bitmaps", ErrCorruptState)
		}
		timestamp := uint64(point.sourceTime)
		if index > 0 {
			timestamp = uint64(point.sourceTime - previousTime)
		}
		buffer = appendUvarint(buffer, timestamp)
		buffer = append(buffer, point.recordDigest[:]...)
		buffer = append(buffer, point.valid...)
		buffer = append(buffer, point.anomalous...)
		previousTime = point.sourceTime
		if len(buffer) > codec.limits.MaxEncodedBytes {
			return nil, ErrStateBudget
		}
	}
	if len(buffer) > codec.limits.MaxEncodedBytes {
		return nil, ErrStateBudget
	}
	return buffer, nil
}

func (codec *Codec) Decode(blob []byte) (*Window, error) {
	if codec == nil {
		return nil, fmt.Errorf("%w: codec is required", ErrCorruptState)
	}
	if len(blob) > codec.limits.MaxEncodedBytes {
		return nil, ErrStateBudget
	}
	if len(blob) < stateBlobHeaderBytes || string(blob[:4]) != stateBlobMagic {
		return nil, fmt.Errorf("%w: invalid header", ErrCorruptState)
	}
	if blob[4] != stateBlobSchemaV1 {
		return nil, fmt.Errorf("%w: schema %d", ErrUnsupportedState, blob[4])
	}
	if blob[5] != stateBlobCodecNone {
		return nil, fmt.Errorf("%w: codec %d", ErrUnsupportedState, blob[5])
	}
	reader := blob[stateBlobHeaderBytes:]
	levelCount, rest, err := consumeUvarint(reader)
	if err != nil {
		return nil, err
	}
	if levelCount > uint64(codec.limits.MaxLevels) {
		return nil, ErrStateBudget
	}
	if levelCount > uint64(len(rest)/33) {
		return nil, fmt.Errorf("%w: truncated level layout", ErrCorruptState)
	}
	levels := make([]levelState, int(levelCount))
	var previousLevel uint32
	for index := range levels {
		value, next, decodeErr := consumeUvarint(rest)
		if decodeErr != nil || value == 0 || value > math.MaxUint32 || (index > 0 && uint32(value) <= previousLevel) {
			return nil, fmt.Errorf("%w: invalid level layout", ErrCorruptState)
		}
		rest = next
		if len(rest) < 32 {
			return nil, fmt.Errorf("%w: truncated level fingerprint", ErrCorruptState)
		}
		levels[index].levelID = uint32(value)
		copy(levels[index].detectFingerprint[:], rest[:32])
		rest = rest[32:]
		previousLevel = uint32(value)
	}
	pointCount, rest, err := consumeUvarint(rest)
	if err != nil {
		return nil, err
	}
	if pointCount > uint64(codec.limits.MaxPoints) {
		return nil, ErrStateBudget
	}
	bitmapBytes := (len(levels) + 7) / 8
	minimumPointBytes := 1 + 16 + bitmapBytes*2
	if pointCount > uint64(len(rest)/minimumPointBytes) {
		return nil, fmt.Errorf("%w: truncated point layout", ErrCorruptState)
	}
	points := make([]pointState, int(pointCount))
	bitmapStorage := make([]byte, int(pointCount)*bitmapBytes*2)
	var previousTime int64
	for index := range points {
		encodedTime, next, decodeErr := consumeUvarint(rest)
		if decodeErr != nil || encodedTime > math.MaxInt64 {
			return nil, fmt.Errorf("%w: invalid source time", ErrCorruptState)
		}
		rest = next
		sourceTime := int64(encodedTime)
		if index > 0 {
			if encodedTime == 0 || encodedTime > uint64(math.MaxInt64-previousTime) {
				return nil, fmt.Errorf("%w: invalid source time delta", ErrCorruptState)
			}
			sourceTime = previousTime + int64(encodedTime)
		}
		required := 16 + bitmapBytes*2
		if len(rest) < required {
			return nil, fmt.Errorf("%w: truncated point", ErrCorruptState)
		}
		points[index].sourceTime = sourceTime
		copy(points[index].recordDigest[:], rest[:16])
		bitmapOffset := index * bitmapBytes * 2
		points[index].valid = bitmapStorage[bitmapOffset : bitmapOffset+bitmapBytes]
		points[index].anomalous = bitmapStorage[bitmapOffset+bitmapBytes : bitmapOffset+bitmapBytes*2]
		copy(points[index].valid, rest[16:16+bitmapBytes])
		copy(points[index].anomalous, rest[16+bitmapBytes:required])
		if !anyBit(points[index].valid) || !validPointBitmaps(points[index].valid, points[index].anomalous, len(levels)) {
			return nil, fmt.Errorf("%w: invalid point bitmaps", ErrCorruptState)
		}
		rest = rest[required:]
		previousTime = sourceTime
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing bytes", ErrCorruptState)
	}
	return &Window{levels: levels, points: points}, nil
}

func decodeDigest32(value string) ([32]byte, error) {
	var result [32]byte
	if !isSHA256Hex(value) {
		return result, fmt.Errorf("state: digest must be 64 lowercase hexadecimal characters")
	}
	decoded, _ := hex.DecodeString(value)
	copy(result[:], decoded)
	return result, nil
}

func appendUvarint(destination []byte, value uint64) []byte {
	var encoded [binary.MaxVarintLen64]byte
	count := binary.PutUvarint(encoded[:], value)
	return append(destination, encoded[:count]...)
}

func uvarintBytes(value uint64) int {
	var encoded [binary.MaxVarintLen64]byte
	return binary.PutUvarint(encoded[:], value)
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

func validPointBitmaps(valid, anomalous []byte, levelCount int) bool {
	for index := range valid {
		if anomalous[index]&^valid[index] != 0 {
			return false
		}
	}
	if levelCount == 0 || len(valid) == 0 || levelCount%8 == 0 {
		return true
	}
	unused := byte(0xff << uint(levelCount%8))
	return valid[len(valid)-1]&unused == 0 && anomalous[len(anomalous)-1]&unused == 0
}
