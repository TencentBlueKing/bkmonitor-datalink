// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"crypto/sha256"
	"encoding/binary"
)

// SetDigest is a set of object identities in sixteen bytes: how many, and
// the sum of a 64-bit hash of each, modulo 2^64.
//
// Digests add: the replicas' digests, added, are the digest of everything
// they hold between them -- an object held by two replicas counted twice.
// That is the point of a sum over XOR, under which the same object on two
// replicas cancels and "held by several" is exactly what goes unseen. Added
// up and compared with the digest of the objects expected, equal says no
// object is held by several replicas, held without being expected, or
// expected without being held (compareCoverage's three findings) -- wrong by
// chance with probability about 2^-64 -- and unequal says there is one,
// which the owned lists then name (fleet-read-scale-design §3, §6.2.1).
type SetDigest struct {
	Count uint64 `json:"count"`
	Sum   uint64 `json:"sum"`
}

// DigestOf is the digest of one set. An identity listed twice in it is one
// object, as compareCoverage reads a replica's own list.
func DigestOf(identities []string) SetDigest {
	seen := make(map[string]struct{}, len(identities))
	digest := SetDigest{}
	for _, identity := range identities {
		if _, repeat := seen[identity]; repeat {
			continue
		}
		seen[identity] = struct{}{}
		digest.Count++
		digest.Sum += identityHash(identity)
	}
	return digest
}

// Add is the digest of both sets together, an object in both counted twice.
func (digest SetDigest) Add(other SetDigest) SetDigest {
	return SetDigest{Count: digest.Count + other.Count, Sum: digest.Sum + other.Sum}
}

// identityHash is the first eight bytes of the identity's SHA-256: a hash
// no two object identities agree on by construction, at a cost that does
// not show beside publishing them.
func identityHash(identity string) uint64 {
	sum := sha256.Sum256([]byte(identity))
	return binary.BigEndian.Uint64(sum[:8])
}
