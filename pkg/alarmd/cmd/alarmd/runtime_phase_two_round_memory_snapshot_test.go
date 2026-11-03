// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The snapshot names the object keeping the most rounds, which the metric
// cannot, and reads the tracker after it forgets what this replica no longer
// owns: an object handed away is not what this replica holds.
func TestFleetPublisherNamesTheObjectKeepingTheMostRounds(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(20_000, 0)}
	tracker := fleet.NewTracker(nil, "replica-1", clock.now)
	observe := func(queryGroup, strategy string, rounds int) {
		ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: queryGroup})
		for i := 0; i < rounds; i++ {
			tracker.Observe(ctx, observability.Observation{ProgressCompletionKind: "FULL_COMPLETED",
				Trace: observability.TraceFields{StrategyID: strategy, BusinessID: "2", EvaluationTime: int64(600 + 60*i)}})
		}
	}
	observe("qg-kept", "901", 3)
	observe("qg-small", "12", 1)
	observe("qg-handed-away", "31", 9)
	owned := []execution.QueryGroupIdentity{"qg-kept", "qg-small"}
	publisher := fleetPublisher{
		tracker: tracker, replica: "replica-1", now: clock.now,
		owned: func() []execution.QueryGroupIdentity { return owned },
	}
	snapshot := publisher.snapshot(context.Background())
	memory := snapshot.RoundMemory
	if memory == nil || memory.Rounds != 4 || memory.Largest == nil || memory.Largest.QueryGroup != "qg-kept" ||
		memory.Largest.Rounds != 3 || len(memory.Largest.Strategies) != 1 || memory.Largest.Strategies[0].StrategyID != "901" {
		t.Fatalf("round memory = %+v (largest %+v), want qg-kept of strategy 901 named over the four owned rounds",
			memory, memoryLargest(memory))
	}
}

func memoryLargest(memory *fleet.RoundMemorySummary) *fleet.LargestRoundMemory {
	if memory == nil {
		return nil
	}
	return memory.Largest
}
