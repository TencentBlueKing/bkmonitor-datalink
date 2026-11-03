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
	"sync"
)

// A page that reads the fleet's snapshots holds the memory it decodes them
// into, and the view it builds of them, until its answer is written: both
// are garbage then, and counted past it they would hold observation off
// with memory nothing keeps. pageHolds collects the holds the reads under
// one piece of work take, and releases them together when the work is done.
//
// pageReadCharge is what a page read is held as, in its snapshots' stored
// length: the live peak of a page read -- the snapshots decoded and the
// view built of them -- measured at 4.22 times the stored length over a
// replica's snapshot of half a megabyte, charged at 17/4. Small snapshots
// decode with teeth up to 3.8 times their length alone, and are small.
//
// keptViewCharge is what a view kept past its page -- a diagnosis in its
// cache -- is admitted as: measured at 1.92 times the snapshots' stored
// length once the snapshots were let go and collected, over twelve
// replicas' snapshots of 2164 KB together, charged at 2. It is not the peak
// less the decode: the view's rows share their strings and pointer fields
// with the snapshots, and those stay with the view.
const (
	pageReadChargeNum, pageReadChargeDen = 17, 4
	keptViewChargeNum, keptViewChargeDen = 2, 1
)

type pageHolds struct {
	mu       sync.Mutex
	releases []func()
	stored   uint64
	done     bool
}

type pageHoldsKey struct{}

// in is ctx carrying holds: the reads under it hold what they read on it.
func (holds *pageHolds) in(ctx context.Context) context.Context {
	return context.WithValue(ctx, pageHoldsKey{}, holds)
}

// pageHoldsOf is the collector ctx carries, if any.
func pageHoldsOf(ctx context.Context) *pageHolds {
	holds, _ := ctx.Value(pageHoldsKey{}).(*pageHolds)
	return holds
}

// withPageHolds is ctx carrying a new collector, and its release: deferred
// by a page as soon as it has the context, so every way the page ends
// releases what its reads held.
func withPageHolds(ctx context.Context) (context.Context, func()) {
	holds := &pageHolds{}
	return holds.in(ctx), holds.release
}

// add keeps release for the end of the work, with the stored length of what
// it holds. Work already done releases it at once: nothing would later.
func (holds *pageHolds) add(release func(), stored uint64) {
	holds.mu.Lock()
	if holds.done {
		holds.mu.Unlock()
		release()
		return
	}
	holds.releases = append(holds.releases, release)
	holds.stored += stored
	holds.mu.Unlock()
}

// storedBytes is the stored length of everything the work held.
func (holds *pageHolds) storedBytes() uint64 {
	holds.mu.Lock()
	defer holds.mu.Unlock()
	return holds.stored
}

// release gives back everything the work held. Safe to call more than once.
func (holds *pageHolds) release() {
	holds.mu.Lock()
	releases := holds.releases
	holds.releases, holds.done = nil, true
	holds.mu.Unlock()
	for _, release := range releases {
		release()
	}
}
