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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// seriesDigest is a series identity digest of the shape the store requires:
// 64 lowercase hexadecimal characters. The framed record derives every
// point's record id from it, so a fixture named "series" cannot be the digest
// itself - it has to be hashed into one.
func seriesDigest(name string) execution.SeriesIdentityDigest {
	sum := sha256.Sum256([]byte(name))
	return execution.SeriesIdentityDigest(hex.EncodeToString(sum[:]))
}

// derivedRecordID is the record id the framed record reconstructs for a point
// at this source time, so a fixture carries the id the store will accept.
func derivedRecordID(t *testing.T, identity execution.StateKeyIdentity, at int64) string {
	t.Helper()
	id, err := contract.DeriveRecordIDV2(string(identity.SeriesIdentityDigest), at)
	if err != nil {
		t.Fatalf("derive record id for %q at %d: %v", identity.SeriesIdentityDigest, at, err)
	}
	return id
}

// derivedPoint is one history point with a normal fact on Level 1 and the
// record id the store derives for it.
func derivedPoint(t *testing.T, identity execution.StateKeyIdentity, at int64, fingerprint string, result execution.LevelFactResult) execution.StateHistoryPoint {
	t.Helper()
	return execution.StateHistoryPoint{RecordID: derivedRecordID(t, identity, at), SourceTime: at,
		Levels: []execution.StateLevelFact{{LevelID: 1, DetectFingerprint: fingerprint, Result: result}}}
}

func derivedAnchor(t *testing.T, identity execution.StateKeyIdentity, at int64) execution.RecordAnchor {
	t.Helper()
	return execution.RecordAnchor{RecordID: derivedRecordID(t, identity, at), SourceTime: at}
}
