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
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestCodecRoundTripsDynamicLevelsDeterministically(t *testing.T) {
	codec, err := NewCodec(CodecLimits{MaxLevels: 8, MaxPoints: 16, MaxEncodedBytes: 4096})
	if err != nil {
		t.Fatalf("NewCodec() error = %v", err)
	}
	window := &Window{
		levels: []levelState{
			{levelID: 1, detectFingerprint: mustDigest32(t, strings.Repeat("1", 64))},
			{levelID: 5, detectFingerprint: mustDigest32(t, strings.Repeat("5", 64))},
		},
		points: []pointState{
			{sourceTime: 100, recordDigest: mustDigest16(t, strings.Repeat("a", 64)), valid: []byte{0b00000011}, anomalous: []byte{0b00000001}},
			{sourceTime: 160, recordDigest: mustDigest16(t, strings.Repeat("b", 64)), valid: []byte{0b00000011}, anomalous: []byte{0b00000010}},
		},
	}

	first, err := codec.Encode(window)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	second, err := codec.Encode(window)
	if err != nil {
		t.Fatalf("Encode(second) error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("Encode() is not deterministic")
	}
	if got, want := string(first[:4]), "ALD1"; got != want {
		t.Fatalf("magic = %q, want %q", got, want)
	}

	decoded, err := codec.Decode(first)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	assertWindowStateEqual(t, decoded, window)
}

func TestCodecClassifiesUnsupportedAndCorruptState(t *testing.T) {
	codec, err := NewCodec(CodecLimits{MaxLevels: 2, MaxPoints: 2, MaxEncodedBytes: 256})
	if err != nil {
		t.Fatalf("NewCodec() error = %v", err)
	}
	window := &Window{
		levels: []levelState{{levelID: 1, detectFingerprint: mustDigest32(t, strings.Repeat("1", 64))}},
		points: []pointState{{
			sourceTime: 100, recordDigest: mustDigest16(t, strings.Repeat("a", 64)), valid: []byte{1}, anomalous: []byte{1},
		}},
	}
	blob, err := codec.Encode(window)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	unsupportedSchema := append([]byte(nil), blob...)
	unsupportedSchema[4]++
	if _, err := codec.Decode(unsupportedSchema); !errors.Is(err, ErrUnsupportedState) {
		t.Fatalf("Decode(unsupported schema) error = %v", err)
	}
	unsupportedCodec := append([]byte(nil), blob...)
	unsupportedCodec[5]++
	if _, err := codec.Decode(unsupportedCodec); !errors.Is(err, ErrUnsupportedState) {
		t.Fatalf("Decode(unsupported codec) error = %v", err)
	}
	for name, candidate := range map[string][]byte{
		"truncated header":  blob[:5],
		"truncated payload": blob[:len(blob)-1],
		"trailing bytes":    append(append([]byte(nil), blob...), 0),
		"bad magic":         append([]byte("BAD!"), blob[4:]...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, decodeErr := codec.Decode(candidate); !errors.Is(decodeErr, ErrCorruptState) {
				t.Fatalf("Decode() error = %v, want corrupt state", decodeErr)
			}
		})
	}
}

func TestCodecEnforcesBoundsBeforeAllocation(t *testing.T) {
	codec, err := NewCodec(CodecLimits{MaxLevels: 1, MaxPoints: 1, MaxEncodedBytes: 128})
	if err != nil {
		t.Fatalf("NewCodec() error = %v", err)
	}
	tooManyLevels := &Window{levels: []levelState{
		{levelID: 1, detectFingerprint: mustDigest32(t, strings.Repeat("1", 64))},
		{levelID: 5, detectFingerprint: mustDigest32(t, strings.Repeat("5", 64))},
	}}
	if _, err := codec.Encode(tooManyLevels); !errors.Is(err, ErrStateBudget) {
		t.Fatalf("Encode(level budget) error = %v", err)
	}

	validCodec, err := NewCodec(CodecLimits{MaxLevels: 2, MaxPoints: 2, MaxEncodedBytes: 256})
	if err != nil {
		t.Fatalf("NewCodec(valid) error = %v", err)
	}
	blob, err := validCodec.Encode(tooManyLevels)
	if err != nil {
		t.Fatalf("Encode(valid) error = %v", err)
	}
	if _, err := codec.Decode(blob); !errors.Is(err, ErrStateBudget) {
		t.Fatalf("Decode(level budget) error = %v", err)
	}
}

func TestCodecRejectsInvalidPointBitmaps(t *testing.T) {
	codec, err := NewCodec(CodecLimits{MaxLevels: 2, MaxPoints: 2, MaxEncodedBytes: 256})
	if err != nil {
		t.Fatalf("NewCodec() error = %v", err)
	}
	window := &Window{
		levels: []levelState{{levelID: 1, detectFingerprint: mustDigest32(t, strings.Repeat("1", 64))}},
		points: []pointState{{
			sourceTime: 100, recordDigest: mustDigest16(t, strings.Repeat("a", 64)), valid: []byte{0}, anomalous: []byte{1},
		}},
	}
	if _, err := codec.Encode(window); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("Encode(invalid bitmap) error = %v", err)
	}
	window.points[0].anomalous[0] = 0
	if _, err := codec.Encode(window); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("Encode(empty bitmap) error = %v", err)
	}
}

func TestCodecShapeAdmissionProvidesBoundedUpperBound(t *testing.T) {
	codec, err := NewCodec(CodecLimits{MaxLevels: 8, MaxPoints: 120, MaxEncodedBytes: 4096})
	if err != nil {
		t.Fatalf("NewCodec() error = %v", err)
	}
	window := benchmarkWindowForTest(t, 8, 120)
	blob, err := codec.Encode(window)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	upperBound, err := codec.AdmitShape(8, 120)
	if err != nil {
		t.Fatalf("AdmitShape() error = %v", err)
	}
	if upperBound < len(blob) || upperBound > len(blob)+120*10+8*5 {
		t.Fatalf("upper bound = %d, encoded = %d, want conservative bounded estimate", upperBound, len(blob))
	}
	if actualShapeBound, err := codec.AdmitWindow(window); err != nil || actualShapeBound != upperBound {
		t.Fatalf("AdmitWindow() = (%d, %v), want (%d, nil)", actualShapeBound, err, upperBound)
	}
	if _, err := codec.AdmitShape(9, 120); !errors.Is(err, ErrStateBudget) {
		t.Fatalf("AdmitShape(levels) error = %v, want budget", err)
	}
	if _, err := codec.AdmitShape(8, 121); !errors.Is(err, ErrStateBudget) {
		t.Fatalf("AdmitShape(points) error = %v, want budget", err)
	}
	tooSmall, _ := NewCodec(CodecLimits{MaxLevels: 8, MaxPoints: 120, MaxEncodedBytes: len(blob)})
	if _, err := tooSmall.AdmitShape(8, 120); !errors.Is(err, ErrStateBudget) {
		t.Fatalf("AdmitShape(bytes) error = %v, want conservative budget rejection", err)
	}
	if _, err := tooSmall.Encode(window); !errors.Is(err, ErrStateBudget) {
		t.Fatalf("Encode(bytes) error = %v, want budget rejection before append", err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		_, _ = tooSmall.Encode(window)
	}); allocations != 0 {
		t.Fatalf("Encode(over budget) allocations = %.1f, want 0 before hard-water allocation", allocations)
	}
}

func mustDigest32(t *testing.T, value string) [32]byte {
	t.Helper()
	return digest32FromHexForTest(t, value)
}

func mustDigest16(t *testing.T, value string) [16]byte {
	t.Helper()
	full := digest32FromHexForTest(t, value)
	var result [16]byte
	copy(result[:], full[:16])
	return result
}

func digest32FromHexForTest(t *testing.T, value string) [32]byte {
	t.Helper()
	decoded, err := decodeDigest32(value)
	if err != nil {
		t.Fatalf("decodeDigest32() error = %v", err)
	}
	return decoded
}

func assertWindowStateEqual(t *testing.T, got, want *Window) {
	t.Helper()
	if len(got.levels) != len(want.levels) || len(got.points) != len(want.points) {
		t.Fatalf("window sizes = (%d,%d), want (%d,%d)", len(got.levels), len(got.points), len(want.levels), len(want.points))
	}
	for index := range want.levels {
		if got.levels[index] != want.levels[index] {
			t.Fatalf("level[%d] = %+v, want %+v", index, got.levels[index], want.levels[index])
		}
	}
	for index := range want.points {
		left, right := got.points[index], want.points[index]
		if left.sourceTime != right.sourceTime || left.recordDigest != right.recordDigest ||
			!bytes.Equal(left.valid, right.valid) || !bytes.Equal(left.anomalous, right.anomalous) {
			t.Fatalf("point[%d] = %+v, want %+v", index, left, right)
		}
	}
}

func benchmarkWindowForTest(t *testing.T, levels, points int) *Window {
	t.Helper()
	window := &Window{levels: make([]levelState, levels), points: make([]pointState, points)}
	bitmapBytes := (levels + 7) / 8
	for index := range window.levels {
		window.levels[index] = levelState{levelID: uint32(index + 1), detectFingerprint: mustDigest32(t, strings.Repeat("1", 64))}
	}
	for index := range window.points {
		window.points[index] = pointState{
			sourceTime: int64(100 + index*60), recordDigest: mustDigest16(t, strings.Repeat("a", 64)),
			valid: make([]byte, bitmapBytes), anomalous: make([]byte, bitmapBytes),
		}
		for level := range window.levels {
			setBit(window.points[index].valid, level, true)
		}
	}
	return window
}
