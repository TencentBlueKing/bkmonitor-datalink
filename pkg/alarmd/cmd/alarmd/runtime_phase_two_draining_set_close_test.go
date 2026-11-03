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
	"strconv"
	"testing"
	"time"
)

// A strategy edited out of its Query Group by an activation at the boundary
// its open Segment starts at (a same-boundary cutover):
// the cutover leaves the old Query Group's timeline open with revision 1,
// and the activation lists it current and draining at once. With the link's
// reconcile answering nothing, the index set stands in for the unlisted
// alerts. The old Query Group's revision 1 is out of its window while
// revision 2 is in its own: its set is held, counted
// index_set_group_draining, and nothing is closed. Once revision 2's window
// ends, the Query Group it runs in - current, not draining - closes the
// member from the set, under revision 2.
func TestTheIndexSetDoesNotStandInForAQueryGroupCurrentAndDraining(t *testing.T) {
	fixture := startClosePathsFixture(t)
	origin := fixture.start
	key := closePathsKey()
	fixture.console.set(true, []closePathsRow{{fingerprint: closePathsA1, status: "matched", listed: true}},
		map[string]closePathsAlert{closePathsA1: {revision: 1, family: "metric_algorithm", severity: "critical", dimensions: thresholdDimensions(true)}})
	fixture.setMembers(closePathsA1)
	fixture.waitFor("the copy lists the strategy's alerts", func() bool {
		_, listed := fixture.listed()
		return listed
	}, func() string { return "not listed" })
	before := fixture.ownedGroups()
	if len(before) != 1 {
		t.Fatalf("revision 1 runs in %v, want one Query Group", before)
	}
	old := before[0]
	// Revision 2 on the host grouping, its window 90 minutes later, activated
	// in the same second as revision 1.
	fixture.start = origin.Add(90 * time.Minute)
	if edited := fixture.publish(2, []string{"bk_target_ip", "bk_target_cloud_id"}, false, nil); edited.Revision != 2 {
		t.Fatalf("the publication carries %+v, want revision 2", edited)
	}
	control := fixture.life.bundle.dependencies.Control.(*productionPhaseTwoControl)
	fixture.waitFor("the old Query Group reads current and draining", func() bool {
		return control.CurrentAndDraining(old)
	}, func() string { return "not current and draining" })
	for _, qg := range fixture.ownedGroups() {
		if qg != old && control.CurrentAndDraining(qg) {
			t.Fatalf("revision 2's Query Group %s reads current and draining", qg)
		}
	}

	fixture.console.fail(true)
	fixture.waitFor("the copy is refused a calibration", func() bool {
		fixture.copy.RequestCalibration()
		return fixture.console.refusedToCopy() > 0
	}, func() string { return "no refused reconcile" })
	// Revision 1's window ended at origin+20m; revision 2's runs from
	// origin+80m to origin+110m.
	fixture.life.clock.Store(origin.Add(95 * time.Minute).UnixMilli())
	fixture.waitFor("the set stands in for the strategy's alerts", func() bool {
		fixture.copy.RequestCalibration()
		members, ok := fixture.copy.UnlistedMembers(key)
		return ok && len(members) == 1 && members[0] == closePathsA1
	}, func() string {
		members, ok := fixture.copy.UnlistedMembers(key)
		return strconv.FormatBool(ok) + " " + strconv.Itoa(len(members))
	})
	held := fixture.groupOutcomes(old, closeOutcomeIndexSetDraining)
	// Waited for until the set is held twice more, or something closes: a
	// close sent instead is the failure this case names, not a timeout.
	fixture.waitFor("the old Query Group's set is held twice more", func() bool {
		return fixture.groupOutcomes(old, closeOutcomeIndexSetDraining) >= held+2 || len(fixture.closes()) > 0
	}, func() string {
		return "index_set_group_draining=" + strconv.Itoa(fixture.groupOutcomes(old, closeOutcomeIndexSetDraining)-held)
	})
	if closes := fixture.closes(); len(closes) != 0 {
		t.Fatalf("the strategy is in effect under revision 2, yet %+v was closed from the set", closes)
	}

	// Revision 2's own window ends: its Query Group closes the member from
	// the set, under revision 2, and the old one is still held.
	fixture.life.clock.Store(origin.Add(120 * time.Minute).UnixMilli())
	fixture.waitFor("the inactive close closes the set's member", func() bool {
		return fixture.maintenanceCounts()[closeOutcomeIndexSetAcked] > 0
	}, func() string { return "no index-set close" })
	wantClosesUnderRevision(t, fixture.closes(), 2)
}
