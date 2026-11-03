// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Level whose guard reason changes is stamped anew by this Slot, and one
// whose guard clears has no origin: the earlier origin, held in the Level's
// state and in the durable guard alike, is not carried onto a reason it was
// not stamped for. That is what lets the stored origin keep only its Slot.
func TestALevelWhoseReasonChangesOrClearsDropsItsEarlierOrigin(t *testing.T) {
	const levelID, slot = uint32(1), execution.EvaluationTime(1_788_000_000)
	earlier := execution.StampReasonOrigin(1_787_999_880, contract.ReasonQueryTimeout)
	view := execution.RuntimeStateView{Levels: []execution.RuntimeLevelStateView{{LevelID: levelID,
		HistoryCompleteness: execution.HistoryGapped, GapReasonCode: contract.ReasonQueryTimeout,
		GapReasonOriginSlot: uint32(earlier.Slot)}}}
	guards := durableGuards{reasons: map[uint32]execution.ReasonCode{levelID: contract.ReasonQueryTimeout},
		origins: map[uint32]execution.ReasonOrigin{levelID: earlier}}

	if got := guards.originFor(view, slot, levelID, execution.HistoryGapped, contract.ReasonQueryTimeout); got != earlier {
		t.Fatalf("same reason: origin %+v, want the earlier %+v", got, earlier)
	}
	changed := guards.originFor(view, slot, levelID, execution.HistoryGapped, contract.ReasonGapSkipped)
	if want := execution.StampReasonOrigin(slot, contract.ReasonGapSkipped); changed != want {
		t.Fatalf("changed reason: origin %+v, want this Slot's %+v", changed, want)
	}
	level := execution.RuntimeLevelStateMutation{LevelID: levelID, HistoryCompleteness: execution.HistoryGapped,
		GapReasonCode: contract.ReasonGapSkipped}.WithReasonOrigin(changed)
	if level.GapReasonOriginSlot != uint32(slot) {
		t.Fatalf("changed reason stored origin slot %d, want %d", level.GapReasonOriginSlot, slot)
	}
	if got := guards.originFor(view, slot, levelID, execution.HistoryFull, ""); got != (execution.ReasonOrigin{}) {
		t.Fatalf("cleared guard: origin %+v, want none", got)
	}
}
