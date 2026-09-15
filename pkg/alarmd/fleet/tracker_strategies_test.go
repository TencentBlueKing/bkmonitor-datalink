// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"testing"
	"time"
)

// observedObject is the only way this table learns a strategy: from a round
// that reported one. An object that never produced a round has none, which is
// the case the second test pins.
func observedObject(t *testing.T, queryGroup, strategy string) *Tracker {
	t.Helper()
	at := &clock{at: time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)}
	tracker := newTracker(t, at)
	tracker.Observe(context.Background(), completion(queryGroup, "COMPLETED", strategy))
	return tracker
}

// An overdue object produced no round, so nothing about it reaches the list
// except what this table already learned from earlier rounds. Without that the
// row is a bare hash sitting beside rows that name a strategy, and the one
// question the reader arrived with -- which of my strategies is this -- has no
// answer on the page.
func TestTheTableNamesTheStrategiesItHasSeenBehindAnObject(t *testing.T) {
	tracker := observedObject(t, "qg-1", "77")

	strategies := tracker.StrategiesFor("qg-1")

	if len(strategies) != 1 || strategies[0].StrategyID != "77" {
		t.Fatalf("strategies = %+v, want the one this table observed", strategies)
	}
}

// Empty for an object no round has ever spoken about, which is precisely the
// object most likely to be overdue: it was parked before this replica ever
// evaluated it. Inventing a strategy for it would be worse than saying nothing.
func TestAnObjectTheTableHasNeverSeenIsNotGivenStrategies(t *testing.T) {
	tracker := observedObject(t, "qg-1", "77")

	if strategies := tracker.StrategiesFor("qg-never-seen"); len(strategies) != 0 {
		t.Fatalf("strategies = %+v, want none for an object nothing reported", strategies)
	}
}
