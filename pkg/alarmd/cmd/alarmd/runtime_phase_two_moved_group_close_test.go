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
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A strategy edited out of its Query Group, through the production bundle:
// revision 2 drops the device grouping, which moves the strategy to another
// Query Group, and moves its effective window thirty minutes later. The
// inactive close had read the old Query Group, revision 1, before the edit.
// At a time revision 1 is out of its window and revision 2 is in its own,
// nothing is closed: the old Query Group's Plans were read under a lease that
// has since moved. Once revision 2's window ends, the alert is closed, under
// revision 2.

// ownedGroups is the Query Groups the bundle runs, sorted.
func (fixture *closePathsFixture) ownedGroups() []execution.QueryGroupIdentity {
	bundle := fixture.life.bundle
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	groups := make([]execution.QueryGroupIdentity, 0, len(bundle.runners))
	for qg := range bundle.runners {
		groups = append(groups, qg)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	return groups
}

// groupOutcomes counts the inactive close's lines of one outcome about qg.
func (fixture *closePathsFixture) groupOutcomes(qg execution.QueryGroupIdentity, outcome string) int {
	life := fixture.life
	life.observationsMu.Lock()
	defer life.observationsMu.Unlock()
	n := 0
	for _, observation := range life.observations {
		if observation.Stage == observability.StageEffectiveTimeMaintenance && string(observation.ReasonCode) == outcome &&
			observation.Trace.QueryGroupKey == string(qg) {
			n++
		}
	}
	return n
}

// moveStrategyOutOfItsGroup opens the bundle with A1 open under revision 1,
// waits until the inactive close has read the strategy's Query Group, and
// publishes revision 2 on the host grouping with its window shift later,
// activated editAt after revision 1 (zero: in the same second; negative: on
// a clock stepped back). It answers the old Query Group and the window's
// origin.
func moveStrategyOutOfItsGroup(t *testing.T, shift, editAt time.Duration) (*closePathsFixture, execution.QueryGroupIdentity, time.Time) {
	t.Helper()
	fixture := startClosePathsFixture(t)
	origin := fixture.start
	fixture.open(closePathsA1)
	// The inactive close reads the Query Group's Plans on its first step,
	// at the bundle's start, and again each second while the read is not
	// current; nothing outside the loop marks the read, so the case gives
	// it two of its steps: the read the edit then leaves behind.
	time.Sleep(2 * time.Second) // fixed-wait: two steps of the maintenance loop, whose Plans read nothing outside it observes
	before := fixture.ownedGroups()
	if len(before) != 1 {
		t.Fatalf("revision 1 runs in %v, want one Query Group", before)
	}
	fixture.life.clock.Store(origin.Add(editAt).UnixMilli())
	fixture.start = origin.Add(shift)
	if edited := fixture.publish(2, []string{"bk_target_ip", "bk_target_cloud_id"}, false, nil); edited.Revision != 2 {
		t.Fatalf("the publication carries %+v, want revision 2", edited)
	}
	moved := false
	for _, qg := range fixture.ownedGroups() {
		moved = moved || qg != before[0]
	}
	if !moved {
		t.Fatalf("revision 2 runs in %v, want a Query Group other than revision 1's %s", fixture.ownedGroups(), before[0])
	}
	return fixture, before[0], origin
}

// heldAcrossSteps waits until the inactive close has failed to read the old
// Query Group under its moved lease twice more - at least one whole step of
// the loop ran at this time, past the read, through every Plan it judges -
// or until the old Query Group has drained and left the bundle, when it
// judges nothing. A close sent ends the wait too, so a case that should
// send none fails on it rather than on the deadline.
func (fixture *closePathsFixture) heldAcrossSteps(old execution.QueryGroupIdentity) {
	fixture.t.Helper()
	from := fixture.groupOutcomes(old, closeOutcomeUnavailable)
	fixture.waitFor("the inactive close reads the old Query Group again", func() bool {
		owned := false
		for _, qg := range fixture.ownedGroups() {
			owned = owned || qg == old
		}
		return fixture.groupOutcomes(old, closeOutcomeUnavailable) >= from+2 || !owned || len(fixture.closes()) > 0
	}, func() string {
		return "unavailable=" + strconv.Itoa(fixture.groupOutcomes(old, closeOutcomeUnavailable)-from)
	})
}

func TestAStrategyEditedOutOfItsQueryGroupIsNotClosedOnItsOldRevision(t *testing.T) {
	fixture, old, origin := moveStrategyOutOfItsGroup(t, 30*time.Minute, 3*time.Minute)
	// Revision 1's window ended at origin+20m; revision 2's runs from
	// origin+20m to origin+50m.
	fixture.life.clock.Store(origin.Add(30 * time.Minute).UnixMilli())
	fixture.heldAcrossSteps(old)
	if closes := fixture.closes(); len(closes) != 0 {
		t.Fatalf("the strategy is in effect under revision 2, yet %+v was closed", closes)
	}
	// Revision 2's own window ends: the alert is closed, under revision 2.
	fixture.life.clock.Store(origin.Add(60 * time.Minute).UnixMilli())
	fixture.waitFor("the inactive close closes A1", func() bool { return len(fixture.closes()) > 0 }, func() string { return "no close" })
	wantClosesUnderRevision(t, fixture.closes(), 2)
}
