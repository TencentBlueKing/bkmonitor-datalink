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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// RejectionTally counts how often each capacity budget refused something.
//
// The same counts exist as a metric, and this is deliberately a second small
// tally rather than a read of it: the page answers from the replica's own
// snapshot, so it must not need collection to have happened first. It is what
// makes "no budget is holding this deployment back" a statement the page can
// make on a deployment that started a minute ago.
type RejectionTally struct {
	mu     sync.Mutex
	counts map[string]uint64
}

func NewRejectionTally() *RejectionTally {
	return &RejectionTally{counts: map[string]uint64{}}
}

// Observe counts a refusal. Anything that is not a refusal is ignored: an
// admitted request is the normal case and counting it here would turn a small
// tally into a second copy of the throughput counters.
func (tally *RejectionTally) Observe(_ context.Context, observation observability.Observation) {
	if tally == nil || observation.Component != observability.ComponentResource {
		return
	}
	if observation.CapacityBudget == "" {
		return
	}
	if observation.Result != observability.ResultPaused && observation.Result != observability.ResultFailed {
		return
	}
	tally.mu.Lock()
	defer tally.mu.Unlock()
	tally.counts[string(observation.CapacityBudget)]++
}

// Counts returns a copy. Budgets that never refused anything are absent rather
// than zero, so the map stays the size of what actually happened.
func (tally *RejectionTally) Counts() map[string]uint64 {
	if tally == nil {
		return nil
	}
	tally.mu.Lock()
	defer tally.mu.Unlock()
	if len(tally.counts) == 0 {
		return nil
	}
	counts := make(map[string]uint64, len(tally.counts))
	for budget, count := range tally.counts {
		counts[budget] = count
	}
	return counts
}
