// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// A first no-data write the pool has no room for ends its Slot
// RESOURCE_HARD_STOP after the round's threshold events were sent and its
// State written, with the memory unwritten and Progress where it stood before
// the round (worker: TestAFirstNoDataWriteThePoolHasNoRoomForIsRetried). The
// case builds that store from a first round that ran whole - its memory and
// its Progress taken away, as a Query Group's first round starts with none -
// rather than by filling the pool, and a new process then retries the
// round: the threshold events are not sent twice, since the State written
// before carries the round's decision, and the memory is written by the retry
// as the first write it was.
func TestAFirstNoDataWriteRetriedAfterTheThresholdEventsWentOutSendsThemOnce(t *testing.T) {
	fixture := startLifecycleFixture(t, lifecycleStrategy{revision: 7, continuous: 1,
		targets: []string{lifecycleHostA, lifecycleHostB}, dimensions: lifecycleHostPair}, nil)
	ctx := context.Background()
	fixture.serve(false, 95, lifecycleHostA, lifecycleHostB)
	if keys := fixture.memoryKeys(); len(keys) != 0 {
		t.Fatalf("no-data memory keys %v before the first round; want none", keys)
	}
	fixture.mustAttempt(1)
	at := fixture.evaluationAt(1)
	sentAt := func() int {
		count := 0
		for _, event := range fixture.thresholdEventsFor(lifecycleHostA) {
			if event.EvaluationTime == at && event.EventKind == contract.TriggerEventAbnormal {
				count++
			}
		}
		return count
	}
	if sent := sentAt(); sent != 1 {
		t.Fatalf("round 1 sent host A %d ABNORMAL threshold events, want one", sent)
	}
	keys := fixture.memoryKeys()
	if len(keys) != 1 {
		t.Fatalf("round 1 left no-data memory keys %v, want its first write", keys)
	}

	// The refusal's store: the events out and the State written, the memory
	// not, and no Progress, as before the Query Group's first round.
	progressKey := fixture.progressKey()
	fixture.closeBundle(fixture.bundle)
	if err := fixture.redis.Del(ctx, append(keys, progressKey)...).Err(); err != nil {
		t.Fatal(err)
	}
	writesBefore := len(fixture.memoryWrites())
	fixture.open("alarmd-worker-0", nil)
	fixture.mustAttempt(1)

	if next, lastFull := fixture.progressSlots(); lastFull != at || next != fixture.evaluationAt(2) {
		t.Fatalf("after the retry Progress has last full %d and next %d, want round 1's Slot %d completed", lastFull, next, at)
	}
	if sent := sentAt(); sent != 1 {
		t.Fatalf("the retry of round 1 sent host A %d ABNORMAL threshold events in all; the first attempt sent it already", sent)
	}
	if keys := fixture.memoryKeys(); len(keys) != 1 || len(fixture.memoryWrites()) == writesBefore {
		t.Fatalf("after the retry no-data memory keys are %v with %d writes since; want the first write made by the retry",
			keys, len(fixture.memoryWrites())-writesBefore)
	}
}
