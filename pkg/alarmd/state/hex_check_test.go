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
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
)

// The check and the decode as they were before they read in place: decode,
// re-encode, compare; then decode again and copy. Kept here as the reference
// the in-place versions must agree with.
func referenceIsSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func referenceDecodeDigest32(value string) ([32]byte, bool) {
	var result [32]byte
	if !referenceIsSHA256Hex(value) {
		return result, false
	}
	decoded, _ := hex.DecodeString(value)
	copy(result[:], decoded)
	return result, true
}

// assertDigestCheckAgrees holds the in-place check and decode to the
// reference on one value: the same verdict, and where accepted the same 32
// bytes.
func assertDigestCheckAgrees(t *testing.T, value string) {
	t.Helper()
	if got, want := isSHA256Hex(value), referenceIsSHA256Hex(value); got != want {
		t.Fatalf("isSHA256Hex(%q) = %v, reference %v", value, got, want)
	}
	got, err := decodeDigest32(value)
	want, accepted := referenceDecodeDigest32(value)
	if (err == nil) != accepted {
		t.Fatalf("decodeDigest32(%q) error = %v, reference accepted = %v", value, err, accepted)
	}
	if accepted && got != want {
		t.Fatalf("decodeDigest32(%q) = %x, reference %x", value, got, want)
	}
}

// A digest is 64 lowercase hexadecimal characters and nothing else, and the
// in-place check says so exactly where decoding and re-encoding did.
func TestTheDigestCheckAcceptsExactlyWhatItDidBefore(t *testing.T) {
	valid := hex.EncodeToString(func() []byte { sum := sha256.Sum256([]byte("alarmd")); return sum[:] }())
	for _, testCase := range []struct {
		name   string
		value  string
		accept bool
	}{
		{"64 lowercase hexadecimal characters", valid, true},
		{"every digit", strings.Repeat("0123456789", 6) + "0123", true},
		{"every lowercase letter", strings.Repeat("abcdef", 10) + "abcd", true},
		{"one uppercase letter", "A" + valid[1:], false},
		{"all uppercase", strings.ToUpper(strings.Repeat("abcdef", 10) + "abcd"), false},
		{"63 characters", valid[:63], false},
		{"65 characters", valid + "0", false},
		{"a letter past f", valid[:63] + "g", false},
		{"a space", " " + valid[1:], false},
		{"a non-ASCII byte", "\xff" + valid[1:], false},
		{"the empty string", "", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isSHA256Hex(testCase.value); got != testCase.accept {
				t.Fatalf("isSHA256Hex(%q) = %v, want %v", testCase.value, got, testCase.accept)
			}
			assertDigestCheckAgrees(t, testCase.value)
		})
	}
}

// Every byte value, at the first and the last position of a valid digest,
// gets the reference's verdict; and a thousand digests decode to the
// reference's bytes.
func TestTheDigestCheckAgreesWithTheReferenceOnEveryCharacter(t *testing.T) {
	valid := strings.Repeat("5a", 32)
	for c := 0; c < 256; c++ {
		assertDigestCheckAgrees(t, string([]byte{byte(c)})+valid[1:])
		assertDigestCheckAgrees(t, valid[:63]+string([]byte{byte(c)}))
	}
	for index := 0; index < 1000; index++ {
		sum := sha256.Sum256([]byte(strconv.Itoa(index)))
		assertDigestCheckAgrees(t, hex.EncodeToString(sum[:]))
	}
}

// The point of reading in place: the window checks every point's record id
// on every record it applies, and the check allocates nothing.
func TestTheDigestCheckAllocatesNothing(t *testing.T) {
	value := strings.Repeat("0f", 32)
	if allocations := testing.AllocsPerRun(100, func() { _ = isSHA256Hex(value) }); allocations != 0 {
		t.Fatalf("isSHA256Hex allocates %.0f times per call, want 0", allocations)
	}
	if allocations := testing.AllocsPerRun(100, func() { _, _ = decodeDigest32(value) }); allocations != 0 {
		t.Fatalf("decodeDigest32 allocates %.0f times per call, want 0", allocations)
	}
}
