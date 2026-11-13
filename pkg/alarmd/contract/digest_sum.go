// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math/bits"
)

// DigestSum256 folds SHA-256 digests into their sum modulo 2^256, so a set
// read in any order and in any number of segments names one value.
//
// A sum and not an XOR: XOR is order-free too, but an element folded twice
// cancels out of it, so a scan that returned one element twice would digest
// as a set without that element. A sum moves instead - which still makes the
// value wrong, so each element must be added exactly once and deduplicating is
// the caller's. Two different sets collide only if their sums of SHA-256
// values agree modulo 2^256. The elements are the platform's own records and
// the value is an idempotency identity rather than a security boundary, so the
// generalised-birthday weakening of additive set hashes against a chosen-input
// adversary does not apply to its callers.
//
// What one element's digest covers, and its domain, is the caller's: this is
// the fold alone, shared so that every order-free digest of the no-data memory
// adds alike.
type DigestSum256 struct {
	// limbs is the sum as four 64-bit limbs, most significant first.
	limbs [4]uint64
}

// Add folds one digest in.
func (sum *DigestSum256) Add(digest [sha256.Size]byte) {
	var carry uint64
	for limb := len(sum.limbs) - 1; limb >= 0; limb-- {
		sum.limbs[limb], carry = bits.Add64(sum.limbs[limb], binary.BigEndian.Uint64(digest[limb*8:limb*8+8]), carry)
	}
}

// Hex is the sum as 64 lowercase hexadecimal digits, most significant first.
func (sum *DigestSum256) Hex() string {
	folded := make([]byte, 0, sha256.Size)
	for _, limb := range sum.limbs {
		folded = binary.BigEndian.AppendUint64(folded, limb)
	}
	return hex.EncodeToString(folded)
}
