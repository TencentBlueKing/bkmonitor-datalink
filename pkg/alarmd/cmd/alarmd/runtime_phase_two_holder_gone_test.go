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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// A Leader that places Query Groups off a holder gone from its ready set says
// so on the round: how many, and how many of those the holder held under a
// worker session. Before, such a round left no count and no line.
func TestALeaderRoundSaysWhatItPlacedOffAHolderThatIsGone(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	groups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2", "query-group-3"}
	store := newIndexStore(now, groups)
	store.holderKind = ownership.HolderUnderSession
	for _, queryGroup := range groups[:2] {
		record := store.assignments[queryGroup]
		record.DesiredWorkerID = "worker-gone"
		store.assignments[queryGroup] = record
	}
	production, observations := newIndexOwnershipHarness(t, now, store)
	if leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Minute); err != nil || !leader {
		t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
	}
	if err := production.PublishAssignments(context.Background(), groups, now); err != nil {
		t.Fatalf("PublishAssignments() error = %v", err)
	}
	var facts *observability.RebalanceFacts
	for _, observation := range *observations {
		if observation.Stage == observability.StageRebalancePlanned && observation.Result == observability.ResultSuccess {
			facts = observation.Rebalance
		}
	}
	if facts == nil {
		t.Fatal("no rebalance observation")
	}
	if facts.Placed != 2 || facts.PlacedFromSessionHolders != 2 || facts.Replaced != 0 {
		t.Fatalf("round facts placed = %d (%d from session holders), replaced %d; want 2, 2, 0",
			facts.Placed, facts.PlacedFromSessionHolders, facts.Replaced)
	}
}
