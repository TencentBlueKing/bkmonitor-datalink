// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A per-series record stores an origin as its Slot alone, so Slot zero is
// what "no origin" is: no stamp may ever produce it, and none produces a
// Slot the record cannot hold.
func TestNoStampedOriginIsSlotZero(t *testing.T) {
	for _, slot := range []execution.EvaluationTime{0, -60} {
		if origin := execution.StampReasonOrigin(slot, contract.ReasonQueryTimeout); origin != (execution.ReasonOrigin{}) {
			t.Fatalf("slot %d stamped origin %+v", slot, origin)
		}
	}
	level := execution.RuntimeLevelStateMutation{LevelID: 1, HistoryCompleteness: execution.HistoryGapped,
		GapReasonCode: contract.ReasonQueryTimeout}
	if got := level.WithReasonOrigin(execution.StampReasonOrigin(1, contract.ReasonQueryTimeout)); got.GapReasonOriginSlot != 1 {
		t.Fatalf("the smallest real Slot is stored as %d", got.GapReasonOriginSlot)
	}
	beyond := execution.ReasonOrigin{Slot: math.MaxUint32 + 60, Cause: contract.ReasonQueryTimeout}
	if got := level.WithReasonOrigin(beyond); got.GapReasonOriginSlot != 0 || got.ReasonOrigin() != (execution.ReasonOrigin{}) {
		t.Fatalf("a Slot past uint32 is stored as %d, read back as %+v; want no origin", got.GapReasonOriginSlot, got.ReasonOrigin())
	}
}

// The origin's cause is not stored: it is the reason beside it. That holds
// only if an origin never outlives its reason, so a Level and an outcome
// refuse to keep one whose cause is not their reason - a reason that changed
// or cleared takes its origin with it in the same write.
func TestAnOriginDoesNotOutliveItsReason(t *testing.T) {
	origin := execution.StampReasonOrigin(1_787_999_880, contract.ReasonQueryTimeout)
	for name, level := range map[string]execution.RuntimeLevelStateMutation{
		"reason changed": {LevelID: 1, HistoryCompleteness: execution.HistoryGapped, GapReasonCode: contract.ReasonGapSkipped},
		"reason cleared": {LevelID: 1, HistoryCompleteness: execution.HistoryFull},
	} {
		if got := level.WithReasonOrigin(origin); got.GapReasonOriginSlot != 0 {
			t.Fatalf("%s: Level kept origin slot %d for a reason it no longer holds", name, got.GapReasonOriginSlot)
		}
	}
	outcome := execution.LevelOutcome{LevelID: 1, Outcome: execution.LevelOutcomeUnknown, ReasonCode: contract.ReasonGapSkipped,
		ReasonCarried: true}
	if got := outcome.WithReasonOrigin(origin); got.ReasonOriginSlot != 0 || got.ReasonOrigin() != (execution.ReasonOrigin{}) {
		t.Fatalf("outcome kept origin %+v for reason %s", got.ReasonOrigin(), got.ReasonCode)
	}
	kept := execution.RuntimeLevelStateMutation{LevelID: 1, HistoryCompleteness: execution.HistoryGapped,
		GapReasonCode: contract.ReasonQueryTimeout}.WithReasonOrigin(origin)
	if kept.ReasonOrigin() != origin {
		t.Fatalf("Level held for the origin's own reason read back %+v, want %+v", kept.ReasonOrigin(), origin)
	}
}

// A record written by the build that stored the cause beside the Slot reads
// back with the same origin, and a record written without either reads back
// with none - not an error, and not a Slot-zero origin.
func TestStoredLevelsDecodeWithAndWithoutAnOrigin(t *testing.T) {
	withCause := []byte(`{"LevelID":5,"LevelStateCompatibility":"c","HistoryCompleteness":"GAPPED","GapReasonCode":"QUERY_TIMEOUT",` +
		`"WarmupRequirementRef":"w","LastProcessedEventTime":1788000000,"GapReasonOriginSlot":1787999940,"GapReasonOriginCause":"QUERY_TIMEOUT"}`)
	without := []byte(`{"LevelID":5,"LevelStateCompatibility":"c","HistoryCompleteness":"GAPPED","GapReasonCode":"QUERY_TIMEOUT",` +
		`"WarmupRequirementRef":"w","LastProcessedEventTime":1788000000}`)
	var level execution.RuntimeLevelStateMutation
	if err := json.Unmarshal(withCause, &level); err != nil {
		t.Fatalf("a record carrying a stored cause failed to decode: %v", err)
	}
	if want := execution.StampReasonOrigin(1_787_999_940, contract.ReasonQueryTimeout); level.ReasonOrigin() != want {
		t.Fatalf("origin %+v, want %+v", level.ReasonOrigin(), want)
	}
	level = execution.RuntimeLevelStateMutation{}
	if err := json.Unmarshal(without, &level); err != nil {
		t.Fatalf("a record without an origin failed to decode: %v", err)
	}
	if level.GapReasonOriginSlot != 0 || level.ReasonOrigin() != (execution.ReasonOrigin{}) || level.ReasonOrigin().Known() {
		t.Fatalf("a record without an origin read back origin %+v", level.ReasonOrigin())
	}
	encoded, err := json.Marshal(level)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(without) {
		t.Fatalf("a Level without an origin encodes as\n%s\nwant the bytes written before origins existed\n%s", encoded, without)
	}
}
