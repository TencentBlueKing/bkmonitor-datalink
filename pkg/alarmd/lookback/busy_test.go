// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Busy counts what the group holds in flight or reserved, and nothing that
// was dropped or has finished: a trial done, a reread dropped with its owner.
func TestBusyCountsWhatTheGroupHoldsNow(t *testing.T) {
	engine := &Engine{groups: map[execution.QueryGroupIdentity]*group{
		"held": {
			capturing: true,
			sample:    &sample{running: true},
			probe:     &sample{},
			probations: []*sample{
				{probation: true, running: true}, {probation: true}, {probation: true, dropped: true},
			},
			directed: map[execution.EvaluationTime]*directedSlot{60: {running: true}, 120: {dropped: true}},
			prepared: &earlierSample{running: true},
		},
		"done": {prepared: &earlierSample{done: true}, sample: &sample{dropped: true}},
	}}
	busy, known := engine.Busy("held")
	want := Busy{Capturing: true, SamplePending: true, ProbePending: true, ProbationRereads: 2,
		DirectedPending: 1, ReadsInFlight: 4, TrialPrepared: true}
	if !known || busy != want {
		t.Fatalf("Busy(held) = %+v (%v), want %+v", busy, known, want)
	}
	if busy, known := engine.Busy("done"); !known || busy != (Busy{}) {
		t.Fatalf("Busy(done) = %+v (%v), want nothing held", busy, known)
	}
	if busy, known := engine.Busy("unseen"); !known || busy != (Busy{}) {
		t.Fatalf("Busy(unseen) = %+v (%v), want nothing held", busy, known)
	}
	if _, known := (*Engine)(nil).Busy("held"); known {
		t.Fatal("no engine has a reading")
	}
}
