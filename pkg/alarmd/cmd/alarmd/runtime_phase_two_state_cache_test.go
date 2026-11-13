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
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// The production wiring of the write-through State cache: the worker hands
// the store its owner fence and the Plan's activation, so a strategy that
// left a Query Group this worker kept holding and came back is not served
// the history cached before it left. Its State deleted while it was away,
// the Slot back reads the store - the key absent - and writes it anew; served
// the cache, it would meet the deleted key and write the stale history's
// result back over it (state_cache_restored_total{cause="missing"}).
func TestAStrategyBackInItsHeldGroupIsNotServedWhatWasCachedBeforeItLeft(t *testing.T) {
	fixture := startSharedGroupFixture(t)
	ctx := context.Background()
	ran := execution.EvaluationTime(fixture.base + 60)
	fixture.joinedAndRan(ctx, ran)
	keys, err := fixture.redisClient.Keys(ctx, fixture.cfg.Redis.StatePrefix+":runtime3:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	var away []string
	for _, key := range keys {
		if strings.Contains(key, ":1004:") {
			away = append(away, key)
		}
	}
	if len(away) == 0 {
		t.Fatalf("fixture: strategy 1004 wrote no State among %v", keys)
	}
	fixture.leaveAndComeBack(ctx, ran+1)
	if err := fixture.redisClient.Del(ctx, away...).Err(); err != nil {
		t.Fatal(err)
	}
	cache := func(family, label, value string) float64 {
		t.Helper()
		got, served := servedSample(t, fixture.recorder, family, map[string]string{label: value})
		if !served {
			t.Fatalf("/metrics serves no %s{%s=%q}", family, label, value)
		}
		return got
	}
	first := ran + 60
	hitsBefore := cache("alarmd_state_cache_lookups_total", "result", "hit")
	fixture.runUntilEnded(ctx, first)
	// The first Slot back is served nothing from the cache: every lookup of
	// it misses, by its activation or, retried, as a replay.
	if got := cache("alarmd_state_cache_lookups_total", "result", "hit"); got != hitsBefore {
		t.Fatalf("the first Slot back was served %v lookups from the cache, want none", got-hitsBefore)
	}
	if got := cache("alarmd_state_cache_lookups_total", "result", "miss_activation"); got < 1 {
		t.Fatalf("state_cache_lookups_total{miss_activation} = %v, want the strategy back read from the store", got)
	}
	if got := cache("alarmd_state_cache_restored_total", "cause", "missing"); got != 0 {
		t.Fatalf("state_cache_restored_total{missing} = %v, want nothing written back: the Slot read the key absent", got)
	}
	// And the Slots after, under the same fence and activation, are served
	// from what the one before wrote: a round retried under load reads the
	// store (miss_replay), so a few Slots, not one.
	for slot := first + 60; slot <= first+180; slot += 60 {
		fixture.runUntilEnded(ctx, slot)
	}
	if got := cache("alarmd_state_cache_lookups_total", "result", "hit"); got < 1 {
		lookups := map[string]float64{}
		for _, result := range state.StateCacheLookupResults {
			lookups[result] = cache("alarmd_state_cache_lookups_total", "result", result)
		}
		t.Fatalf("state_cache_lookups_total{hit} = %v, want the next Slot served from the cache; lookups %v", got, lookups)
	}
}
