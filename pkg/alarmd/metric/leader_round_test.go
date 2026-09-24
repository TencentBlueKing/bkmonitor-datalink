// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// Nothing before the process has led a round; after, every stage and the
// total, zero where no round reached, and both results.
func TestLeaderRoundCollectorEmitsEveryStageOnceLeading(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	stats := LeaderRoundStats{}
	r.SetLeaderRoundSource(func() LeaderRoundStats { return stats })
	if series := gatherFamily(t, r, "bkmonitor_alarmd_leader_round_stage_seconds_total"); len(series) != 0 {
		t.Fatalf("stage series before any round = %v, want none", series)
	}
	stats = LeaderRoundStats{Leading: true, Rounds: map[string]uint64{fleet.LeaderRoundCompleted: 3},
		Seconds: map[string]float64{fleet.LeaderRoundStageAssignmentSweep: 1.5, LeaderRoundStageTotal: 6}}
	seconds := map[string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_leader_round_stage_seconds_total") {
		seconds[m.Label[0].GetValue()] = m.GetCounter().GetValue()
	}
	if len(seconds) != len(fleet.LeaderRoundStages)+1 || seconds[fleet.LeaderRoundStageAssignmentSweep] != 1.5 ||
		seconds[LeaderRoundStageTotal] != 6 || seconds[fleet.LeaderRoundStageViewPublish] != 0 {
		t.Fatalf("stage seconds = %v, want every stage and the total", seconds)
	}
	rounds := map[string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_leader_rounds_total") {
		rounds[m.Label[0].GetValue()] = m.GetCounter().GetValue()
	}
	if len(rounds) != 2 || rounds[fleet.LeaderRoundCompleted] != 3 || rounds[fleet.LeaderRoundFailed] != 0 {
		t.Fatalf("rounds = %v, want both results", rounds)
	}
}
