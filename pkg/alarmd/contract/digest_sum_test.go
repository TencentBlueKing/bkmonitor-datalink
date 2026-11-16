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
	"fmt"
	"math/big"
	"strings"
	"testing"
)

// sumModulo is the fold restated with big integers: the digests read as
// big-endian numbers, added, and taken modulo 2^256.
func sumModulo(digests ...[sha256.Size]byte) string {
	sum := new(big.Int)
	for _, digest := range digests {
		sum.Add(sum, new(big.Int).SetBytes(digest[:]))
	}
	sum.Mod(sum, new(big.Int).Lsh(big.NewInt(1), 256))
	return fmt.Sprintf("%064x", sum)
}

// The fold is a sum and not an XOR: an element added twice moves the value
// instead of cancelling out of it, and the order the elements come in does
// not change it. The carries run across the limbs: a digest of all ones
// folded twice is 2^257 - 2, which modulo 2^256 is all ones but the last bit.
func TestDigestSumAddsAnElementFoldedTwiceAndIsTheSameInEitherOrder(t *testing.T) {
	a, b := sha256.Sum256([]byte("group-a")), sha256.Sum256([]byte("group-b"))
	var ones [sha256.Size]byte
	for index := range ones {
		ones[index] = 0xff
	}
	zero := strings.Repeat("0", 64)

	for _, digest := range [][sha256.Size]byte{a, ones} {
		var twice DigestSum256
		twice.Add(digest)
		twice.Add(digest)
		if got := twice.Hex(); got == zero {
			t.Fatalf("%x folded twice is back at zero, as an XOR would leave it", digest)
		} else if want := sumModulo(digest, digest); got != want {
			t.Fatalf("%x folded twice = %s, want the sum modulo 2^256 %s", digest, got, want)
		}
	}

	var forward, backward DigestSum256
	forward.Add(a)
	forward.Add(b)
	backward.Add(b)
	backward.Add(a)
	if forward.Hex() != backward.Hex() {
		t.Fatalf("a then b = %s, b then a = %s, want one value", forward.Hex(), backward.Hex())
	}
	if want := sumModulo(a, b); forward.Hex() != want {
		t.Fatalf("a and b fold to %s, want the sum modulo 2^256 %s", forward.Hex(), want)
	}
}
