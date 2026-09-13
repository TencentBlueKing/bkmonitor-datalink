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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Every code in the catalogue has to be on one side or the other, decided here
// rather than by the runtime default.
//
// The default exists so an unclassified code cannot read as healthy, but a
// default that quietly absorbs new codes is a decision nobody made: the code
// would be filed against this deployment forever without anyone having asked
// whether it belongs there. This is what makes adding a reason code a moment
// where somebody answers the question.
func TestEveryReasonCodeIsAttributedToOneSideOrTheOther(t *testing.T) {
	catalogue := contract.ReasonCatalogV2()
	if len(catalogue) == 0 {
		t.Fatal("the reason catalogue is empty; the check would pass vacuously")
	}
	for _, definition := range catalogue {
		_, external := externalReasons[definition.Code]
		_, ours := ourReasons[definition.Code]
		switch {
		case external && ours:
			t.Errorf("%s is listed as both external and ours", definition.Code)
		case !external && !ours:
			t.Errorf("%s is in the reason catalogue and on neither list: it would count "+
				"against this deployment by default, which may be right, but nobody said so",
				definition.Code)
		}
	}
	// And nothing on either list that the catalogue does not have, which would be
	// a rule kept alive for a code that no longer exists.
	known := map[string]bool{}
	for _, definition := range catalogue {
		known[definition.Code] = true
	}
	for code := range externalReasons {
		if !known[code] {
			t.Errorf("externalReasons lists %q, which the reason catalogue does not contain", code)
		}
	}
	for code := range ourReasons {
		if !known[code] {
			t.Errorf("ourReasons lists %q, which the reason catalogue does not contain", code)
		}
	}
}

// The verdict follows only the objects this deployment could have prevented.
//
// A live read had 99 objects in the anomaly column, almost all of them an
// algorithm waiting for history, a strategy edited mid-round, or a backend that
// timed out. Every one of them made the verdict DEGRADED, so the verdict had
// been DEGRADED continuously for things alarmd cannot act on -- and a signal
// that is always on carries nothing.
func TestOnlyWhatThisDeploymentCouldPreventDecidesTheVerdict(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	external := func(id, reason string) Anomaly {
		return Anomaly{QueryGroup: id, Kind: KindDegradedRun, Since: at.Add(-time.Hour),
			Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: reason}
	}
	snapshots := healthySnapshots()
	snapshots[1].Anomalies = []Anomaly{
		external("qg-warming", "HISTORY_WARMING"),
		external("qg-gapped", "HISTORY_GAPPED"),
		external("qg-drift", "CONFIG_DRIFT"),
		external("qg-timeout", "QUERY_TIMEOUT"),
	}
	snapshots[1].TotalAnomalies = 4

	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, time.Minute)
	if len(view.Gaps) > 0 {
		t.Fatalf("unexpected gaps make the verdict UNKNOWN regardless: %+v", view.Gaps)
	}
	if len(view.Anomalies) != 4 {
		t.Fatalf("anomalies = %d, want the 4 external ones still listed", len(view.Anomalies))
	}
	if view.Health != HealthHealthy {
		t.Errorf("health = %q with nothing but external anomalies, want %q: the objects are real "+
			"work but not this deployment's, and a verdict that cannot come back while they exist "+
			"says nothing", view.Health, HealthHealthy)
	}

	// One object this deployment could have prevented is enough to turn it.
	snapshots[1].Anomalies = append(snapshots[1].Anomalies, Anomaly{
		QueryGroup: "qg-budget", Kind: KindDegradedRun, Since: at.Add(-time.Hour),
		Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "EXECUTION_BUDGET_EXHAUSTED"})
	snapshots[1].TotalAnomalies = 5
	view = Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, time.Minute)
	if view.Health != HealthDegraded {
		t.Errorf("health = %q with one budget exhaustion among four external anomalies, want %q",
			view.Health, HealthDegraded)
	}
	if got := OursCount(view.Anomalies); got != 1 {
		t.Errorf("ours = %d, want exactly the one budget exhaustion", got)
	}
}

// A code nobody has classified counts against the deployment. The reverse
// default would let a failure mode this build has only just started producing
// arrive as a HEALTHY verdict, which is the one direction that loses the
// signal rather than costing a look.
func TestAnUnclassifiedReasonCountsAgainstTheDeployment(t *testing.T) {
	got := attributionOf(Anomaly{Kind: KindDegradedRun, CauseReason: "SOMETHING_NOBODY_HAS_MAPPED"})
	if got != AttributionOurs {
		t.Errorf("attribution = %q for an unmapped reason, want %q", got, AttributionOurs)
	}
}

// A stalled object is ours whatever its last reason code was: its last code is
// usually the external thing that happened just before it got stuck, and
// stalling is the one condition here that never clears on its own.
func TestAStalledObjectIsOursEvenWhenItsLastReasonWasExternal(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	anomalies := []Anomaly{{
		QueryGroup: "qg-stuck", Kind: KindDegradedRun,
		Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "QUERY_TIMEOUT",
		FailingSince: at.Add(-2 * time.Hour),
	}}
	Attribute(anomalies)
	if anomalies[0].Attribution != AttributionExternal {
		t.Fatalf("before the stall is marked this object reads as %q; the test no longer "+
			"exercises the upgrade it exists for", anomalies[0].Attribution)
	}
	MarkStalled(anomalies, at, time.Hour)
	if !anomalies[0].Stalled {
		t.Fatal("the object was not marked stalled, so the upgrade is untested")
	}
	if anomalies[0].Attribution != AttributionOurs {
		t.Errorf("a stalled object reports %q, want %q: nothing outside this deployment stops "+
			"rounds from ending, and nothing outside it will start them again",
			anomalies[0].Attribution, AttributionOurs)
	}
}
