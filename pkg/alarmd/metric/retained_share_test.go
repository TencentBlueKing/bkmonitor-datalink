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
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The counter moves on completed Slots at or past the threshold of their
// share and on nothing else: one byte under it, a failed Slot, a completion
// that carried no share, and a row of another stage all leave it alone.
func TestTheShareApproachCounterCountsCompletedSlotsFromTheThreshold(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	counter := recorder.phaseTwo.retainedShareApproaching
	if got := testutil.ToFloat64(counter); got != 0 {
		t.Fatalf("before anything happened = %v, want a series at zero", got)
	}
	const share = 1_000_000
	at := uint64(share * observability.RetainedShareApproachPercent / 100)
	observe := func(stage observability.Stage, retained, share uint64, err error) {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: stage, Result: observability.ResultSuccess, Err: err,
			SlotBudgetUsage: &observability.SlotBudgetUsageFacts{RetainedBytes: retained, RetainedShareBytes: share},
		})
	}
	observe(observability.StageSlotCompleted, at-1, share, nil)
	if got := testutil.ToFloat64(counter); got != 0 {
		t.Fatalf("one byte under the threshold counted: %v", got)
	}
	observe(observability.StageSlotCompleted, at, share, nil)
	observe(observability.StageSlotCompleted, share, share, nil)
	observe(observability.StageSlotCompleted, share, share, errors.New("failed"))
	observe(observability.StageSlotCompleted, share, 0, nil)
	observe(observability.StageStateApplied, share, share, nil)
	if got := testutil.ToFloat64(counter); got != 2 {
		t.Fatalf("state_retained_share_approaching_slots_total = %v, want 2: the Slot at the threshold and the "+
			"one at the share, and none of the failed, share-less or other-stage rows", got)
	}
}
