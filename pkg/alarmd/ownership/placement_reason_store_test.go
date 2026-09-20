// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ownership

import (
	"context"
	"testing"
	"time"
)

// A REBALANCE Assignment round-trips through the store: published by a live
// Control Leader, it is read back by the same validation every Worker
// applies, so a Worker of this release accepts what a later Leader writes.
func TestRedisStoreReadsBackARebalanceAssignment(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	published, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: "query-group-1", DesiredWorkerID: "worker-2",
		ExpectedRecordRevision: 0, PlacementReason: PlacementRebalance, DecidedAt: now,
	})
	if err != nil {
		t.Fatalf("PublishAssignment(REBALANCE) error = %v", err)
	}
	read, err := store.ReadAssignment(ctx, "query-group-1")
	if err != nil || read != published || read.PlacementReason != PlacementRebalance {
		t.Fatalf("ReadAssignment() = (%+v, %v), want the REBALANCE record %+v", read, err, published)
	}
}
