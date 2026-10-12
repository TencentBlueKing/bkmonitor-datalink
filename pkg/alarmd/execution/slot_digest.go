// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"fmt"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// slotDigestMemoEntries bounds how many Slots' identity digests a process
// remembers. A replica has about as many Slots in flight as it owns Query
// Groups -- some seven hundred on a production deployment, each with a Slot
// or two between freeze and completion -- plus the Slots a backlog replays.
// Four thousand covers that several times over at about 200 bytes an entry,
// under a megabyte. Past it the memory is cleared whole and refilled: a
// cleared entry is only derived again.
const slotDigestMemoEntries = 4096

// slotDigests remembers each Slot's identity digest. The digest is a function
// of the Slot identity alone, which is the whole key, so a remembered digest
// is the one deriving it again gives: another Slot, of the same Query Group a
// minute later or of another Query Group at the same minute, is another key,
// and a retry of the same Slot is owed the same digest. It was derived again
// for every series of every Slot, a canonical JSON encoding and a SHA-256
// each time, for a value that does not change within the Slot.
var slotDigests = slotDigestMemo{digests: make(map[SlotIdentity]SlotIdentityDigest)}

type slotDigestMemo struct {
	mu      sync.RWMutex
	digests map[SlotIdentity]SlotIdentityDigest
}

// slotIdentityDigest is the Slot's identity digest, remembered.
func slotIdentityDigest(slot SlotIdentity) (SlotIdentityDigest, error) {
	slotDigests.mu.RLock()
	digest, ok := slotDigests.digests[slot]
	slotDigests.mu.RUnlock()
	if ok {
		return digest, nil
	}
	digest, err := deriveSlotIdentityDigest(slot)
	if err != nil {
		return "", err
	}
	slotDigests.mu.Lock()
	if len(slotDigests.digests) >= slotDigestMemoEntries {
		clear(slotDigests.digests)
	}
	slotDigests.digests[slot] = digest
	slotDigests.mu.Unlock()
	return digest, nil
}

// deriveSlotIdentityDigest is the digest itself, derived every time.
func deriveSlotIdentityDigest(slot SlotIdentity) (SlotIdentityDigest, error) {
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-slot-identity-v2", struct {
		QueryGroup     QueryGroupIdentity `json:"query_group"`
		EvaluationTime EvaluationTime     `json:"evaluation_time"`
	}{
		QueryGroup:     slot.QueryGroup,
		EvaluationTime: slot.EvaluationTime,
	})
	if err != nil {
		return "", fmt.Errorf("alarmd execution: derive Slot identity digest: %w", err)
	}
	return SlotIdentityDigest(digest), nil
}
