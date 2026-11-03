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
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A reason alarmd judged reads derived on the row, a reason the round met
// reads physical, and a stored guard's reason reads carried whatever it is.
func TestARowsCauseOriginFollowsWhoseTheReasonIs(t *testing.T) {
	carried := &observability.CarriedReasonFacts{OriginSlot: 1_700_000_040, OriginCause: "HISTORY_WARMING"}
	for _, test := range []struct {
		reason  string
		filed   bool
		carried *observability.CarriedReasonFacts
		want    string
	}{
		{"EFFECTIVE_TIME_INACTIVE", false, nil, CauseOriginDerived},
		{"HISTORY_WARMING", false, nil, CauseOriginDerived},
		{"QUERY_TIMEOUT", false, nil, observability.ReasonOriginPhysical},
		{"HISTORY_WARMING", false, carried, observability.ReasonOriginCarried},
		{"GAP_GUARD_WARMING", true, carried, CauseOriginDerived},
	} {
		if got := causeOriginOf(test.reason, test.filed, test.carried); got == nil || got.Origin != test.want {
			t.Errorf("%s filed=%t carried=%t: origin %+v, want %s", test.reason, test.filed, test.carried != nil, got, test.want)
		}
	}
}

// The overview carries the object list's cause reasons, all and by origin,
// through the path it is computed on in production: replica summaries, whose
// heads hold no rows, aggregated into the merged part the overview reads.
// Counted from hand-built rows instead, this read empty on every deployment.
func TestTheOverviewCarriesTheCauseReasonsByOriginThroughSummaries(t *testing.T) {
	snapshots := idleSnapshots()
	snapshots[0].Anomalies = []Anomaly{
		{QueryGroup: "qg-321", Replica: "pod-a", Kind: KindDegradedRun, CauseReason: "EFFECTIVE_TIME_INACTIVE",
			CauseOrigin: &CauseOrigin{Origin: CauseOriginDerived}, Since: now.Add(-time.Hour)},
		{QueryGroup: "qg-322", Replica: "pod-a", Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT",
			CauseOrigin: &CauseOrigin{Origin: observability.ReasonOriginPhysical}, Since: now.Add(-time.Hour)},
	}
	snapshots[1].Anomalies = []Anomaly{
		{QueryGroup: "qg-323", Replica: "pod-b", Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT",
			CauseOrigin: &CauseOrigin{Origin: observability.ReasonOriginCarried}, Since: now.Add(-time.Hour)},
	}
	summaries := make([]ReplicaSummary, 0, len(snapshots))
	for _, snapshot := range snapshots {
		summary := SummaryOf(snapshot, snapshot.OwnedObjects, time.Minute)
		if len(summary.Head.Anomalies) != 0 {
			t.Fatal("fixture: a summary head carries rows, so this case could not show the part carrying them")
		}
		summaries = append(summaries, summary)
	}
	_, part := aggregateSummaries(Expectation{}, SetDigest{}, summaries, replicas(), now, freshness,
		func([]string) ([][]string, bool) { return nil, false }, controlRead{})
	got := overviewCauseReasonsOf(part)
	count := func(distribution Distribution, reason string) int {
		for _, entry := range distribution.Top {
			if entry.Value == reason {
				return entry.Count
			}
		}
		return 0
	}
	if count(got.ByCauseReason, "QUERY_TIMEOUT") != 2 || count(got.ByCauseReasonPhysical, "QUERY_TIMEOUT") != 1 ||
		count(got.ByCauseReasonCarried, "QUERY_TIMEOUT") != 1 || count(got.ByCauseReasonDerived, "EFFECTIVE_TIME_INACTIVE") != 1 {
		t.Fatalf("overview cause reasons %+v, want both replicas' rows counted by origin", got)
	}
	if got.Partial {
		t.Fatal("no replica cut its rows, yet the counts read partial")
	}
	missing := part
	missing.CauseReasonsMissing = true
	if !overviewCauseReasonsOf(missing).Partial {
		t.Fatal("a replica without counts did not mark the overview's counts partial")
	}
}

// A replica on a build before the counts publishes a part without them: it
// decodes as none, the merge says so, and the overview reads partial rather
// than zero. With no replica at all the counts are partial too.
func TestAPartWithoutCauseReasonCountsLeavesTheOverviewPartial(t *testing.T) {
	var older ReplicaPart
	if err := json.Unmarshal([]byte(`{"replica":"pod-old","attribution":{},"impact":{},"empty_every_round":0,"demoted_due":0,`+
		`"demoted_due_since":"0001-01-01T00:00:00Z","cooling":{},"loss":{},"pruned_skips_total":0,"retained_share_total":0,`+
		`"read_early_total":0,"truncated":0}`), &older); err != nil {
		t.Fatal(err)
	}
	if older.CauseReasons != nil {
		t.Fatalf("a part without cause_reasons decoded counts %+v", older.CauseReasons)
	}
	current := ReplicaPart{CauseReasons: newCauseReasonCounts()}
	current.CauseReasons.add(Anomaly{CauseReason: "QUERY_TIMEOUT", CauseOrigin: &CauseOrigin{Origin: observability.ReasonOriginPhysical}})
	merged := overviewCauseReasonsOf(MergeReplicaParts(current, older))
	if !merged.Partial || len(merged.ByCauseReason.Top) != 1 {
		t.Fatalf("merged %+v, want the current replica's count marked partial", merged)
	}
	if none := overviewCauseReasonsOf(MergeReplicaParts()); !none.Partial {
		t.Fatal("no replica at all read as nothing degraded")
	}
}
