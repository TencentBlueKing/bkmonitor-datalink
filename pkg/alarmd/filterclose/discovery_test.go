// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"context"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
)

// The discovery finds each strategy with active alerts of this source and
// tenant that the snapshot does not run, one search each, and nothing of
// another source, tenant, a running strategy, or a recovered alert.
func TestAbsentDiscoveryFindsOnlyStrategiesNotRunning(t *testing.T) {
	h := newHarness(t)
	h.link.add(&fakeAlert{id: "a1", labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	h.link.add(&fakeAlert{id: "a2", labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	h.link.add(&fakeAlert{id: "a3", labels: map[string]string{"strategy_id": "77", "strategy_version": "9"}})
	h.link.add(&fakeAlert{id: "a4", labels: map[string]string{"strategy_id": "50"}})
	h.link.add(&fakeAlert{id: "a5", status: "recovered", labels: map[string]string{"strategy_id": "60"}})
	h.link.add(&fakeAlert{id: "a6", source: "another", labels: map[string]string{"strategy_id": "61"}})
	h.link.add(&fakeAlert{id: "a7", tenant: "tenant-b", labels: map[string]string{"strategy_id": "62"}})
	closer := h.closer(t)
	found, word := closer.AbsentStrategies(context.Background(), "tenant-a", "built_in_bk", []string{"50"})
	if word != "" || len(found) != 2 || found[0] != (AbsentFound{StrategyID: "41", Version: "3"}) || found[1] != (AbsentFound{StrategyID: "77", Version: "9"}) {
		t.Fatalf("found %+v, word %q", found, word)
	}
	if n := h.link.requests[linkdmanage.RouteSearch]; n != 3 {
		t.Fatalf("searches = %d, want one per strategy found and one that finds none", n)
	}
}

// A walk past its bound stops and says so.
func TestAbsentDiscoveryIsBounded(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < MaxAbsentPerRound+5; i++ {
		h.link.add(&fakeAlert{id: fmt.Sprintf("a%03d", i), labels: map[string]string{"strategy_id": fmt.Sprint(100 + i)}})
	}
	closer := h.closer(t)
	found, _ := closer.AbsentStrategies(context.Background(), "tenant-a", "built_in_bk", nil)
	if len(found) != MaxAbsentPerRound || closer.Stats()[OutcomeAbsentTruncated] != 1 {
		t.Fatalf("found %d, stats %d", len(found), closer.Stats()[OutcomeAbsentTruncated])
	}
}

// A recovered alert of an older version is no bucket: the search reads the
// link's history too, and only active alerts are the edit close's.
func TestEditEnumerationReadsOnlyActiveAlerts(t *testing.T) {
	h := newHarness(t)
	h.link.add(&fakeAlert{id: "old-recovered", status: "recovered", labels: map[string]string{"strategy_id": "41", "strategy_version": "1"}})
	closer := h.closer(t)
	buckets, _, complete, word := closer.enumerate(context.Background(), testScope, "5")
	if word != "" || len(buckets) != 0 || !complete {
		t.Fatalf("buckets %+v, word %q", buckets, word)
	}
}
