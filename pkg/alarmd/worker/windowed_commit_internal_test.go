// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// 2.2 step 6 b, the total bound: the ceiling fixed at the completion is the
// deadline plus a window's bound for each of ceil(S / s_min) windows, and the
// boundaries move the deadline up to it and no further. A Slot cut into more
// windows than that - each taking its whole bound - stops being moved once
// the ceiling is behind it and is claimed at the next tick.
func TestTheWindowDeadlineStopsAtItsCeilingAndTheSlotIsThenDeclined(t *testing.T) {
	const grace = time.Minute
	fixture := newWindowedFixture(t)
	stream, coordinator := fixture.stream, fixture.stream.coordinator
	coordinator.budget.MaxStateMutations = 2
	const series = 5
	for index := range series {
		fixture.streamInput(t, fmt.Sprintf("series-%02d", index), 100)
	}
	deadline := time.UnixMilli(1_800_000_000_000)
	marker := &execution.StageMarker{}
	marker.Begin(execution.SlotStageEvaluate)
	marker.Extend(deadline, deadline)
	ctx := execution.WithStageMarker(context.Background(), marker)

	stream.fixWindowCeiling(ctx, series)
	// s_min is the State cap, two: the bytes allow far more. ceil(5 / 2) = 3.
	const windows = 3
	if want := deadline.Add(windows * windowStepBound); !stream.windows.ceiling.Equal(want) {
		t.Fatalf("ceiling = %v, want %v", stream.windows.ceiling, want)
	}

	now := deadline
	coordinator.ports.Now = func() time.Time { return now }
	for boundary := 1; boundary <= windows+1; boundary++ {
		// Each window takes its whole bound and a little more.
		now = deadline.Add(time.Duration(boundary-1)*windowStepBound + time.Second)
		if err := execution.EnterCommit(ctx, execution.SlotStageOutput); err != nil {
			t.Fatalf("window %d: %v", boundary, err)
		}
		stream.windowBoundary(ctx)
		if boundary <= windows {
			if _, _, claimed := marker.Decline(now, grace); claimed {
				t.Fatalf("window %d: claimed at its own boundary, within the ceiling", boundary)
			}
			continue
		}
		if got := marker.Deadline(); !got.Equal(stream.windows.ceiling) {
			t.Fatalf("past the ceiling the deadline is %v, want it held at the ceiling %v", got, stream.windows.ceiling)
		}
		if _, _, claimed := marker.Decline(now.Add(grace), grace); !claimed {
			t.Fatal("a Slot past its ceiling was not claimed at the next tick")
		}
	}
}
