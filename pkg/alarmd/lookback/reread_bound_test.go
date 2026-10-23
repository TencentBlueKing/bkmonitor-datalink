// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"testing"
	"time"
)

// A group read here answers how long its query takes, never less than
// RecheckTimeout; a group never read here, as one just taken over, answers
// nothing rather than a guess.
func TestRereadBoundIsTheGroupsOwnReading(t *testing.T) {
	f := newFixture(t)
	if _, ok := f.engine.RereadBound("qg"); ok {
		t.Fatal("a group never read has a reading")
	}
	q := heldQuery(f)
	f.engine.mu.Lock()
	state := f.engine.groupLocked(q, f.clock.now())
	f.engine.mu.Unlock()
	if _, ok := f.engine.RereadBound("qg"); ok {
		t.Fatal("a group with no whole first read has a reading")
	}
	f.engine.mu.Lock()
	state.firstReads.add(8 * time.Second)
	f.engine.mu.Unlock()
	if got, ok := f.engine.RereadBound("qg"); !ok || got != 8*time.Second {
		t.Fatalf("slow group: %v %v, want 8s", got, ok)
	}
	f.engine.mu.Lock()
	state.firstReads = durations{}
	state.firstReads.add(2 * time.Second)
	f.engine.mu.Unlock()
	if got, ok := f.engine.RereadBound("qg"); !ok || got != RecheckTimeout {
		t.Fatalf("quick group: %v %v, want RecheckTimeout", got, ok)
	}
	var none *Engine
	if _, ok := none.RereadBound("qg"); ok {
		t.Fatal("no engine has a reading")
	}
}

// The query duration is the group's reading as measured, with no floor: a
// quick query reads as quick. A group never read, or never read whole, has
// none.
func TestQueryDurationIsTheGroupsReadingAsMeasured(t *testing.T) {
	f := newFixture(t)
	if _, ok := f.engine.QueryDuration("qg"); ok {
		t.Fatal("a group never read has a reading")
	}
	q := heldQuery(f)
	f.engine.mu.Lock()
	state := f.engine.groupLocked(q, f.clock.now())
	f.engine.mu.Unlock()
	if _, ok := f.engine.QueryDuration("qg"); ok {
		t.Fatal("a group with no whole first read has a reading")
	}
	f.engine.mu.Lock()
	state.firstReads.add(300 * time.Millisecond)
	f.engine.mu.Unlock()
	if got, ok := f.engine.QueryDuration("qg"); !ok || got != 300*time.Millisecond {
		t.Fatalf("quick group: %v %v, want 300ms", got, ok)
	}
	if got, _ := f.engine.RereadBound("qg"); got != RecheckTimeout {
		t.Fatalf("the reread bound %v, want RecheckTimeout still", got)
	}
	var none *Engine
	if _, ok := none.QueryDuration("qg"); ok {
		t.Fatal("no engine has a reading")
	}
}
