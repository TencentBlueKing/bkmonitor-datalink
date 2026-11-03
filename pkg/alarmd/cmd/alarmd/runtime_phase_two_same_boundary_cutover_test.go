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
	"testing"
	"time"
)

// Same-boundary cutover: an edit that moves the strategy
// to another Query Group, activated in the same second as the activation
// before it or on a Leader clock stepped back behind it. The cutover could
// not close the old Query Group's open Segment at or before its own start
// (a Segment ending where it starts is invalid), settled it as
// retired_unwritten and left its timeline open with revision 1, and the
// activation listed it in the current active set and in the draining
// projection at once. The cutover now moves past the open Segment: the old
// Query Group is retired at the activation's boundary, after its Segment
// starts, is draining at that same boundary, and is not current; revision
// 2's Query Group opens at that boundary.
func TestASameBoundaryCutoverLeavesTheOldQueryGroupDrainingNotCurrent(t *testing.T) {
	for _, edit := range sameBoundaryEdits {
		t.Run(edit.name, func(t *testing.T) {
			fixture := startClosePathsFixture(t)
			before := fixture.ownedGroups()
			if len(before) != 1 {
				t.Fatalf("revision 1 runs in %v, want one Query Group", before)
			}
			old := before[0]
			control := fixture.life.bundle.dependencies.Control.(*productionPhaseTwoControl)
			ctx := context.Background()
			opened, err := control.dependencies.Schedules.ReadInitialFrozenSchedule(ctx, old)
			if err != nil {
				t.Fatal(err)
			}
			fixture.life.clock.Store(fixture.start.Add(edit.editAt).UnixMilli())
			if edited := fixture.publish(2, []string{"bk_target_ip", "bk_target_cloud_id"}, false, nil); edited.Revision != 2 {
				t.Fatalf("the publication carries %+v, want revision 2", edited)
			}
			state, err := control.dependencies.Repository.LoadActivationHead(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Draining) != 1 || state.Draining[0].QueryGroup != old || state.Draining[0].RetiredBoundary <= opened.Segment.Start {
				t.Fatalf("the activation drains %+v, want %s alone, retired after its Segment's start %d", state.Draining, old, opened.Segment.Start)
			}
			boundary := state.Draining[0].RetiredBoundary
			retiredAt, retired, err := control.dependencies.Schedules.ReadScheduleRetirement(ctx, old)
			if err != nil || !retired || retiredAt != boundary {
				t.Fatalf("the old Query Group's timeline is retired (%d, %v, %v), want at the draining boundary %d", retiredAt, retired, err, boundary)
			}
			current, err := control.dependencies.Repository.LoadActiveQueryGroupSet(ctx, state.ActiveQGSetRef)
			if err != nil {
				t.Fatal(err)
			}
			if len(current) != 1 || current[0] == old {
				t.Fatalf("the current Query Groups are %v, want revision 2's alone, not %s", current, old)
			}
			if schedule, err := control.dependencies.Schedules.ReadInitialFrozenSchedule(ctx, current[0]); err != nil || schedule.Segment.Start != boundary {
				t.Fatalf("revision 2's Query Group opens at (%+v, %v), want the boundary %d", schedule.Segment, err, boundary)
			}
			if want := time.Unix(int64(boundary), 0); want.Sub(fixture.start.Add(edit.editAt)) > 5*time.Second {
				t.Fatalf("the boundary %d is %s past the edit, want within the clock skew alarmd assumes", boundary, want.Sub(fixture.start.Add(edit.editAt)))
			}
		})
	}
}
