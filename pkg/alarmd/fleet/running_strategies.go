// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "time"

// runningStrategiesOf is one replica's running strategies by state: its own
// rows folded per strategy as its strategy lines fold them, and every
// strategy evaluating on its objects with no row DETECTING. Every state is
// present, zero included. Which strategies evaluate is what the objects'
// rounds named since the replica started (Snapshot.EvaluatingStrategies),
// not the catalog: a strategy none of whose objects has run a round yet is
// not counted, and one that has left an object still held is. An object
// records at most maxStrategiesPerQueryGroup of them; past that, a strategy
// with no row on it is not counted.
func runningStrategiesOf(view View, evaluating []StrategyRef, now time.Time) map[StateWord]int {
	counts := make(map[StateWord]int, len(StateWords))
	for _, word := range StateWords {
		counts[word] = 0
	}
	folded := map[StrategyRef]bool{}
	for _, line := range StrategyLines(&view, now) {
		ref := StrategyRef{StrategyID: line.StrategyID, BusinessID: line.BusinessID}
		folded[ref] = true
		counts[line.Standing.State]++
	}
	for _, ref := range evaluating {
		if !folded[ref] {
			folded[ref] = true
			counts[StateDetecting]++
		}
	}
	return counts
}

// runningStrategiesReadGaps are the gaps that leave some replica's rows not
// read whole: its summary missing, stale, unreadable or deferred, its rows
// cut, no registry, no replica. With any of them the counts would read the
// strategies of the rows not read as not running, or as running, and a half
// view must not read as a collapse.
var runningStrategiesReadGaps = map[GapKind]bool{GapReplicaMissing: true, GapSnapshotStale: true, GapListTruncated: true,
	GapNoReplicas: true, GapRegistryUnavailable: true, GapSnapshotsUnreadable: true, GapSnapshotsDeferred: true}

// RunningStrategiesOf is the deployment's running strategies by state, the
// replicas' own counts added -- a strategy whose objects are on k replicas
// counted k times -- and whether they are known: only from a view read
// whole, every replica's part carrying them.
func RunningStrategiesOf(view *View, part ReplicaPart) (map[StateWord]int, bool) {
	if view == nil || part.RunningStrategies == nil || len(view.Replicas) == 0 {
		return nil, false
	}
	for _, gap := range view.Gaps {
		if runningStrategiesReadGaps[gap.Kind] {
			return nil, false
		}
	}
	return part.RunningStrategies, true
}
