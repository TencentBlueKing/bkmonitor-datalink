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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

// Asking which hold a Slot would be frozen with prepares, writes, counts and
// degrades nothing: the answer is the hold the freeze then gives, the record
// is not written, and a group that cannot answer refuses - it is not handed
// a degraded hold and counted, as a Slot being frozen is.
func TestAskingASlotsHoldWritesAndCountsNothing(t *testing.T) {
	h, c, at := runtimeTestHolds(t)
	ctx := context.Background()
	qg := execution.QueryGroupIdentity("held")
	schedule := runtimeTestHoldSchedule(t, qg)
	session, err := ownership.OpenSession(ctx, &fakePhaseTwoOwnershipStore{}, qg, "worker", *at, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	h.bind(qg, session)
	h.groups[qg].prepared = schedule.Segment
	spec := readhold.GroupSpec{QueryGroup: qg, Plans: []readhold.PlanRef{{Key: schedule.Plans[0].Key()}}, SettlingWait: 30 * time.Second, HoldLimit: 10 * time.Minute}
	if err := h.controller.Configure(spec); err != nil {
		t.Fatal(err)
	}
	h.restore(ctx, []execution.QueryGroupIdentity{qg})
	lease, _ := session.Current()
	if _, err := h.SlotReadHold(ctx, schedule, 1200, lease.Fence); err != nil {
		t.Fatal(err)
	}
	contract := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: qg, EvaluationTime: 1200}, SnapshotRevision: "snapshot",
		QueryRevision: "query", ScheduleRevision: "schedule", ScheduleSegmentStart: 60, DuePlanSetDigest: "due"}
	if err := h.controller.Observe(ctx, readhold.Evidence{Contract: contract, ArrivalAge: 180 * time.Second, Rung: "rung", Buckets: []int64{1200}}); err != nil {
		t.Fatal(err)
	}
	writes, transitions := c.writes, h.transitions.Load()
	asked, err := h.PeekSlotReadHold(schedule, 1260)
	if err != nil || asked != 150*time.Second {
		t.Fatalf("asked %v, %v; want the raised 150 s", asked, err)
	}
	if c.writes != writes || h.transitions.Load() != transitions {
		t.Fatalf("asking wrote %d and counted %d transitions", c.writes-writes, h.transitions.Load()-transitions)
	}
	if frozen, err := h.SlotReadHold(ctx, schedule, 1260, lease.Fence); err != nil || frozen != asked {
		t.Fatalf("the freeze after it gave %v, %v; want %v", frozen, err, asked)
	}

	cold := runtimeTestHoldSchedule(t, "cold")
	if _, err := h.PeekSlotReadHold(cold, 120); err == nil {
		t.Fatal("a group that was never prepared answered")
	}
	h.degradedMu.Lock()
	degraded := 0
	for _, count := range h.degraded {
		degraded += int(count)
	}
	h.degradedMu.Unlock()
	if degraded != 0 {
		t.Fatalf("a refused question was counted as a degraded hold: %v", h.degraded)
	}
}
