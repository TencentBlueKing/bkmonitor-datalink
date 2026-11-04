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
	"testing"
	"time"
)

// A strategy edited out of its Query Group by an activation at or before the
// boundary its open Segment starts at (a same-boundary
// cutover): in the same second as revision 1, or on a Leader clock stepped
// back. Revision 2 drops the device grouping, which moves the strategy to
// another Query Group, and moves its effective window later. At a time
// revision 1 is out of its window and revision 2 is in its own, nothing is
// closed; once revision 2's window ends, the alert is closed under revision
// 2.

// sameBoundaryEdits are the two ways an edit reaches the boundary revision
// 1's Segment starts at.
var sameBoundaryEdits = []struct {
	name   string
	editAt time.Duration
}{
	{"same second", 0},
	{"clock stepped back", -3 * time.Second},
}

func TestASameBoundaryEditClosesNothingUnderTheOldRevision(t *testing.T) {
	for _, edit := range sameBoundaryEdits {
		t.Run(edit.name, func(t *testing.T) {
			fixture, old, origin := moveStrategyOutOfItsGroup(t, 30*time.Minute, edit.editAt)
			// Revision 1's window ended at origin+20m; revision 2's runs from
			// origin+20m to origin+50m.
			fixture.life.clock.Store(origin.Add(30 * time.Minute).UnixMilli())
			fixture.heldAcrossSteps(old)
			if closes := fixture.closes(); len(closes) != 0 {
				t.Fatalf("the strategy is in effect under revision 2, yet %+v was closed", closes)
			}
			// Revision 2's own window ends: the alert is closed, under
			// revision 2.
			fixture.life.clock.Store(origin.Add(60 * time.Minute).UnixMilli())
			fixture.waitFor("the inactive close closes A1", func() bool { return len(fixture.closes()) > 0 }, func() string { return "no close" })
			wantClosesUnderRevision(t, fixture.closes(), 2)
		})
	}
}
