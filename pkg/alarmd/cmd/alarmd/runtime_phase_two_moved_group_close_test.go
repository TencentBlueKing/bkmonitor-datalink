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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
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
// publishes revision 2 on the host grouping with its window shift later. It
// answers the old Query Group and the window's origin.
func moveStrategyOutOfItsGroup(t *testing.T, shift time.Duration) (*closePathsFixture, execution.QueryGroupIdentity, time.Time) {
	t.Helper()
	fixture := startClosePathsFixture(t)
	origin := fixture.start
	fixture.console.set(true, []closePathsRow{{fingerprint: closePathsA1, status: "matched", listed: true}},
		map[string]closePathsAlert{closePathsA1: {revision: 1, family: "metric_algorithm", severity: "critical", dimensions: thresholdDimensions(true)}})
	fixture.setMembers(closePathsA1)
	// The copy tracks the strategy once the inactive close has read its
	// Query Group and registered it: the read the edit then leaves behind.
	fixture.waitFor("the copy lists the strategy's alerts", func() bool {
		_, listed := fixture.listed()
		return listed
	}, func() string { return "not listed" })
	before := fixture.ownedGroups()
	if len(before) != 1 {
		t.Fatalf("revision 1 runs in %v, want one Query Group", before)
	}
	// The edit is activated at a later boundary than revision 1.
	fixture.life.clock.Store(origin.Add(3 * time.Minute).UnixMilli())
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
// Query Group under its moved lease twice more: at least one whole step of
// the loop ran at this time, past the read, through every Plan it judges.
func (fixture *closePathsFixture) heldAcrossSteps(old execution.QueryGroupIdentity) {
	fixture.t.Helper()
	from := fixture.groupOutcomes(old, closeOutcomeUnavailable)
	fixture.waitFor("the inactive close reads the old Query Group again", func() bool {
		fixture.copy.RequestCalibration()
		return fixture.groupOutcomes(old, closeOutcomeUnavailable) >= from+2
	}, func() string {
		return "unavailable=" + strconv.Itoa(fixture.groupOutcomes(old, closeOutcomeUnavailable)-from)
	})
}

func wantClosesUnderRevision(t *testing.T, closes []linkdoutput.CloseRequest, revision int64) {
	t.Helper()
	if len(closes) == 0 {
		t.Fatal("no close was sent")
	}
	for _, request := range closes {
		if request.Fingerprint != closePathsA1 || request.Reason != "" || request.StrategyRevision != revision || request.BusinessID != closePathsBusiness {
			t.Fatalf("close %+v, want A1 closed as inactive under revision %d", request, revision)
		}
	}
}

func TestAStrategyEditedOutOfItsQueryGroupIsNotClosedOnItsOldRevision(t *testing.T) {
	fixture, old, origin := moveStrategyOutOfItsGroup(t, 30*time.Minute)
	// Revision 1's window ended at origin+20m; revision 2's runs from
	// origin+20m to origin+50m.
	fixture.life.clock.Store(origin.Add(30 * time.Minute).UnixMilli())
	fixture.heldAcrossSteps(old)
	if closes := fixture.closes(); len(closes) != 0 {
		t.Fatalf("the strategy is in effect under revision 2, yet %+v was closed", closes)
	}
	// Revision 2's own window ends: the alert is closed, under revision 2.
	fixture.life.clock.Store(origin.Add(60 * time.Minute).UnixMilli())
	fixture.waitFor("the inactive close closes A1", func() bool {
		fixture.copy.RequestCalibration()
		return len(fixture.closes()) > 0
	}, func() string { return "no close" })
	wantClosesUnderRevision(t, fixture.closes(), 2)
}

// The same through the index-set fallback: the link's reconcile answers
// nothing, so the set's members stand in for the strategy's alerts. The old
// Query Group's revision 1 addresses no member while revision 2 is in effect.
func TestAStrategyEditedOutOfItsQueryGroupIsNotClosedFromTheSetOnItsOldRevision(t *testing.T) {
	fixture, old, origin := moveStrategyOutOfItsGroup(t, 90*time.Minute)
	key := closePathsKey()
	fixture.console.fail(true)
	fixture.waitFor("the copy is refused a calibration", func() bool {
		fixture.copy.RequestCalibration()
		return fixture.console.refusedToCopy() > 0
	}, func() string { return "no refused reconcile" })
	// Revision 1's window ended at origin+20m; revision 2's runs from
	// origin+80m to origin+110m. The last listing is past the age it is
	// acted on, and the set stands in.
	fixture.life.clock.Store(origin.Add(95 * time.Minute).UnixMilli())
	fixture.waitFor("the set stands in for the strategy's alerts", func() bool {
		fixture.copy.RequestCalibration()
		members, ok := fixture.copy.UnlistedMembers(key)
		return ok && len(members) == 1 && members[0] == closePathsA1
	}, func() string {
		members, ok := fixture.copy.UnlistedMembers(key)
		return strconv.FormatBool(ok) + " " + strconv.Itoa(len(members))
	})
	fixture.heldAcrossSteps(old)
	if closes := fixture.closes(); len(closes) != 0 {
		t.Fatalf("the strategy is in effect under revision 2, yet %+v was closed from the set", closes)
	}
	// Revision 2's own window ends: the member is closed from the set, under
	// revision 2.
	fixture.life.clock.Store(origin.Add(120 * time.Minute).UnixMilli())
	fixture.waitFor("the inactive close closes the set's member", func() bool {
		return fixture.maintenanceCounts()[closeOutcomeIndexSetAcked] > 0
	}, func() string { return "no index-set close" })
	wantClosesUnderRevision(t, fixture.closes(), 2)
}
