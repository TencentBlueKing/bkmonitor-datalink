// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// roomRecorder is a State cache that keeps the room it is lent.
type roomRecorder struct{ room func() uint64 }

func (cache *roomRecorder) BorrowRetainedWithin(room func() uint64) { cache.room = room }
func (*roomRecorder) BorrowedRetainedBytes() uint64                 { return 0 }
func (*roomRecorder) YieldRetainedBytes(uint64)                     {}

// A coordinator built with a borrower lends it the pool's room at once: what
// no Slot has reserved, the whole pool before any Slot runs. The cache's
// bound then comes with the port, not from a second call somebody must
// remember to make.
func TestACoordinatorLendsItsBorrowerThePoolsRoom(t *testing.T) {
	trace := []string{}
	ports := &recordingPorts{trace: &trace, ready: true}
	cache := &roomRecorder{}
	budget := worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 30, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10}
	_, err := worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports,
		Finalization: ports, Activation: ports,
		Query: ports, Sequencer: ports, Evaluator: ports, Admission: ports, GapGuard: ports, NoData: worker.SharedNoDataStore, Hosts: worker.SharedHostBusiness,
		Events: ports, State: ports, Progress: ports,
		Observer:         observability.ObserverFunc(func(context.Context, observability.Observation) {}),
		RetainedBorrower: cache,
	}, budget)
	if err != nil {
		t.Fatal(err)
	}
	if cache.room == nil || cache.room() != budget.MaxRetainedBytes {
		t.Fatalf("the borrower was lent %v; want the pool's room, all %d bytes before any Slot", cache.room != nil, budget.MaxRetainedBytes)
	}
}
