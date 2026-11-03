// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"context"
	"testing"
	"time"
)

// A Publish reads its clock at the start of the report tick and publishes
// after the roster's refresh. An observation in between can move a group
// into the next window; the Publish carrying the earlier window must not
// clear both of that group's windows, which left it unobserved in that
// publication and the next.
func TestAPublishBehindAnObservationKeepsTheGroupsWindows(t *testing.T) {
	c, now, _ := costFixture()
	ctx := context.Background()
	*now = time.Unix(610, 0)
	c.Observe(ctx, costObservation(StageSlotCompleted))
	*now = time.Unix(661, 0)
	c.Observe(ctx, costObservation(StageSlotCompleted))

	c.Publish(time.Unix(659, 0))
	c.Publish(time.Unix(662, 0))
	s := c.Snapshot()
	if s.Coverage.ObservedGroups != 1 {
		t.Fatalf("observed groups = %d after a publication a window behind, want 1", s.Coverage.ObservedGroups)
	}
	row := costRow(t, s, "query_group", CostPlanIdentity{})
	if row.Current.RunReturns != 1 || row.Previous.RunReturns != 1 {
		t.Fatalf("run returns current %d previous %d, want one in each window", row.Current.RunReturns, row.Previous.RunReturns)
	}
}

// The census reads the clock before it takes its lock, the observer and the
// reader alike. One that waited behind a caller already in the next window
// must not clear the group's windows: the peak the heartbeat carries, and
// placement judges by, would read as the waiter's smaller one.
func TestACensusCallerBehindTheWindowKeepsTheGroupsPeak(t *testing.T) {
	now := time.Unix(610, 0)
	census := NewRetainedPeakCensus(time.Minute, func() time.Time { return now })
	ctx := context.Background()
	census.Observe(ctx, slotCompletion("qg", 100))
	now = time.Unix(661, 0)
	census.Observe(ctx, slotCompletion("qg", 200))
	now = time.Unix(659, 0)
	census.Observe(ctx, slotCompletion("qg", 50))
	now = time.Unix(662, 0)
	peaks := census.RetainedPeaks()
	if len(peaks) != 1 || peaks[0].RetainedBytesPeak != 200 {
		t.Fatalf("peaks = %+v, want the one group at 200: a caller a window behind cleared the group's windows", peaks)
	}
}
