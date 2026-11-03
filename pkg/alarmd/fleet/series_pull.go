// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "sync/atomic"

// SeriesPull is how much data a replica has actually pulled.
//
// It is what makes the per-Slot ceilings readable. A page can print "series
// 524288" beside a deployment pulling two hundred series a second or two
// hundred thousand, and the number reads the same either way -- a limit with no
// measured load beside it says nothing about whether anything is close to it.
// This is the other half of that comparison, and until now nothing counted it:
// the admission counters count decisions per series per plan, which is a
// different number and larger than the series pulled whenever one series feeds
// several plans.
//
// Cumulative rather than a rate, like every other figure in the snapshot: one
// read says how much since this process started, and two reads say the rate.
type SeriesPull struct {
	// Series counts the series handed into the pipeline.
	Series uint64 `json:"series"`
	// Records counts the datapoints inside them, which is what the work
	// actually scales with -- one series of ten thousand points and ten
	// thousand series of one point cost very different amounts.
	Records uint64 `json:"records"`
}

// SeriesPullTally counts what Access pulled, for the replica's own snapshot.
//
// Like RejectionTally this is deliberately a small second tally rather than a
// read of the metric: the page answers from the replica's snapshot and must not
// need collection to have happened first, so it is right one minute after a
// restart.
type SeriesPullTally struct {
	series  atomic.Uint64
	records atomic.Uint64
}

func NewSeriesPullTally() *SeriesPullTally { return &SeriesPullTally{} }

// Add counts one pulled series and the datapoints it carried. It is called once
// per series on the streaming path, so it is two atomic adds and nothing else.
func (tally *SeriesPullTally) Add(records uint64) {
	if tally == nil {
		return
	}
	tally.series.Add(1)
	tally.records.Add(records)
}

func (tally *SeriesPullTally) Counts() *SeriesPull {
	if tally == nil {
		return nil
	}
	return &SeriesPull{Series: tally.series.Load(), Records: tally.records.Load()}
}
