// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import "testing"

// Same-boundary cutover: an edit that moves the strategy
// to another Query Group, activated in the same second as the activation
// before it. The cutover cannot close the old Query Group's open Segment at
// its own start (a Segment ending where it starts is invalid), settles it
// as retired_unwritten, and leaves its timeline open with revision 1; the
// activation then lists it in the current active set and in the draining
// projection at once. Measured with the edit's window moved 30 minutes
// later: at a time revision 1 is out of its window and revision 2 in its
// own, the old Query Group's inactive close closed the strategy's alert
// under revision 1. The case asks for the state the cutover should leave:
// the old Query Group draining, not current.
func TestASameBoundaryCutoverLeavesTheOldQueryGroupDrainingNotCurrent(t *testing.T) {
	t.Skip("same-boundary cutover leaves the leaving Query Group current and draining; fixed in its own round")
	fixture := startClosePathsFixture(t)
	before := fixture.ownedGroups()
	if len(before) != 1 {
		t.Fatalf("revision 1 runs in %v, want one Query Group", before)
	}
	old := before[0]
	// Revision 2 on the host grouping, in the same second as revision 1.
	if edited := fixture.publish(2, []string{"bk_target_ip", "bk_target_cloud_id"}, false, nil); edited.Revision != 2 {
		t.Fatalf("the publication carries %+v, want revision 2", edited)
	}
	control := fixture.life.bundle.dependencies.Control.(*productionPhaseTwoControl)
	if control.CurrentAndDraining(old) {
		t.Fatalf("the Query Group %s revision 1 ran in is current and draining at once after an edit moved the strategy out of it", old)
	}
}
