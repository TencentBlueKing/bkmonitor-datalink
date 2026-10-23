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
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// A slow post-query tail on a worker changes the deadlines that worker runs its queries under, never which
// strategies are served: the capability verdict stays on the configured reserve, so one slow hour - a twelve second
// state read - does not withhold a ten second strategy.
func TestALiveReserveSpikeDoesNotChangeWhichStrategiesAreServed(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	catalog := func() []string {
		admitted, err := phaseTwoCatalogRetentionAdmission(cfg)(retentionTestCatalog(
			retentionTestPlan("4701", 10), retentionTestPlan("4702", 15), retentionTestPlan("4703", 60)))
		if err != nil {
			t.Fatal(err)
		}
		return publishedStrategies(admitted)
	}
	// the admission has no reserve input but the configured one; a runtime's live reserve, however large, is not read
	live := &execution.LiveReserve{}
	before := catalog()
	live.Set(12 * time.Second)
	after := catalog()
	if len(before) != 3 || len(after) != len(before) {
		t.Fatalf("served before the spike %v, after %v: want the same three", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("served before the spike %v, after %v", before, after)
		}
	}
}

// Each derivation publishes the worker's measured post-query tail as the live reserve; before any tail it publishes
// none, so the frozen reserve applies.
func TestTheShaperPublishesThePostQueryTailAsTheLiveReserve(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	shaper, flights, _ := shaperForTest(t, &clock)
	live := &execution.LiveReserve{}
	shaper.live = live
	shaper.derive(1)
	if got := live.Reserve(); got != 0 {
		t.Fatalf("before any tail: live reserve %v, want none", got)
	}
	for i := 0; i < scheduler.PostQueryTailMinSamples; i++ {
		flights.RecordPostQueryTail(time.UnixMilli(clock.Load()), "g", 3*time.Second)
	}
	shaper.derive(1)
	if got := live.Reserve(); got < 3*time.Second {
		t.Fatalf("after a 3 s tail: live reserve %v", got)
	}
}
