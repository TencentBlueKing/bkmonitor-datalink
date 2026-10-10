// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

type fixedExpectations struct{ expectation fleet.Expectation }

func (stub fixedExpectations) Expectation(context.Context) (fleet.Expectation, error) {
	return stub.expectation, nil
}

type fixedRegistry struct{ replicas []string }

func (stub fixedRegistry) ReadyReplicas(context.Context, time.Time) ([]string, error) {
	return stub.replicas, nil
}

type fixedSnapshots struct{ snapshots []fleet.Snapshot }

func (stub fixedSnapshots) Load(context.Context, []string) ([]fleet.Snapshot, error) {
	return stub.snapshots, nil
}

// countedSnapshots counts the reads a scrape makes of the snapshots.
type countedSnapshots struct {
	snapshots []fleet.Snapshot
	loads     *int
}

func (stub countedSnapshots) Load(context.Context, []string) ([]fleet.Snapshot, error) {
	*stub.loads++
	return stub.snapshots, nil
}

// The scrape and the page decide the same screen from the same view. The
// scrape used to mark stalling on the anomaly list alone, so an object in
// the demoted pool that had stopped ending rounds was ROUNDS_STALLED on the
// page and nothing on the metric; driven through the real source so the
// call the scrape makes is the one under test.
func TestTheScrapeMarksStallingOnEveryColumnLikeThePage(t *testing.T) {
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	stuck := fleet.Anomaly{QueryGroup: "demoted-stuck", Kind: fleet.KindDegradedRun,
		Since: at.Add(-2 * time.Hour), FailingSince: at.Add(-2 * time.Hour), Replica: "pod-a"}
	service, err := fleet.NewService(
		fixedExpectations{fleet.Expectation{Known: true, QueryGroups: 2, IDs: []string{"demoted-stuck", "fine"}}},
		fixedRegistry{[]string{"pod-a"}},
		fixedSnapshots{[]fleet.Snapshot{{
			Replica: "pod-a", TakenAt: at.Add(-10 * time.Second), Owned: 2, Determined: 2,
			OwnedObjects: []string{"demoted-stuck", "fine"},
			Anomalies:    []fleet.Anomaly{}, Demoted: []fleet.Anomaly{stuck}, TotalDemoted: 1,
		}}},
		time.Minute, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}

	verdict := fleetVerdictSource(service, func() time.Time { return at }, time.Hour, time.Second)()
	// The scrape decides the verdict too, and records it.
	if history, _ := service.VerdictHistory(); len(history) != 1 || string(history[0].To) != verdict.Health {
		t.Fatalf("verdict record after a scrape = %+v, want the verdict the scrape decided (%s)", history, verdict.Health)
	}

	if got := countOf(verdict.Checks, string(fleet.CheckRoundsStalled)).Count; got != 1 {
		t.Fatalf("fleet_checks{code=ROUNDS_STALLED} = %d from the scrape, want the demoted object the page files there", got)
	}
	// With a budget the object is inside, nothing is stalled, on any column.
	patient := fleetVerdictSource(service, func() time.Time { return at }, 3*time.Hour, time.Second)()
	if got := countOf(patient.Checks, string(fleet.CheckRoundsStalled)).Count; got != 0 {
		t.Fatalf("fleet_checks{code=ROUNDS_STALLED} = %d against a three hour budget, want 0", got)
	}
}

// The scrape reports the verdict the page reports, both read from the
// replicas' summaries: a row that stalls between the replica's publish and
// the scrape is stalled on the next publish for both, and the whole view,
// decided at the scrape's own moment, would have said so first on the
// metric alone.
func TestTheScrapeReportsTheVerdictThePageReports(t *testing.T) {
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	loads := 0
	stalling := fleet.Anomaly{QueryGroup: "qg-stalling", Kind: fleet.KindDegradedRun, Replica: "pod-a",
		Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "QUERY_TIMEOUT", Since: at.Add(-time.Hour),
		FailingSince: at.Add(-10*time.Minute - 15*time.Second)}
	service, err := fleet.NewService(
		fixedExpectations{fleet.Expectation{Known: true, QueryGroups: 2, IDs: []string{"qg-stalling", "fine"}}},
		fixedRegistry{[]string{"pod-a"}},
		countedSnapshots{snapshots: []fleet.Snapshot{{
			Replica: "pod-a", TakenAt: at.Add(-30 * time.Second), Owned: 2, Determined: 2,
			OwnedObjects: []string{"qg-stalling", "fine"}, Anomalies: []fleet.Anomaly{stalling}, TotalAnomalies: 1,
		}}, loads: &loads},
		time.Minute, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	summarized, _ := service.Summarized(context.Background(), 10*time.Minute)
	whole := service.View(context.Background())
	fleet.Decide(&whole, at, 10*time.Minute)
	if summarized.Health == whole.Health {
		t.Fatalf("summaries and whole view both read %s: the fixture does not separate them", whole.Health)
	}
	loads = 0
	verdict := fleetVerdictSource(service, func() time.Time { return at }, 10*time.Minute, time.Second)()
	// One read of the snapshots for the verdict and every breakdown of it:
	// two reads could be of two publishes.
	if loads != 1 {
		t.Fatalf("a scrape read the snapshots %d times, want once", loads)
	}
	if verdict.Health != string(summarized.Health) {
		t.Fatalf("scrape verdict %s, the page's %s", verdict.Health, summarized.Health)
	}
	// And the breakdowns of the same scrape are of the rows the verdict was
	// settled on: nothing stalled, nothing on the stalled line.
	if verdict.Stalled != 0 || countOf(verdict.Checks, string(fleet.CheckRoundsStalled)).Count != 0 {
		t.Fatalf("scrape %s with stalled=%d and ROUNDS_STALLED=%d: one scrape, two readings", verdict.Health, verdict.Stalled,
			countOf(verdict.Checks, string(fleet.CheckRoundsStalled)).Count)
	}
}
