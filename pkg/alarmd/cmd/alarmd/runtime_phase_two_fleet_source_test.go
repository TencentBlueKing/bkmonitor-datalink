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

	if got := countOf(verdict.Checks, string(fleet.CheckRoundsStalled)).Count; got != 1 {
		t.Fatalf("fleet_checks{code=ROUNDS_STALLED} = %d from the scrape, want the demoted object the page files there", got)
	}
	// With a budget the object is inside, nothing is stalled, on any column.
	patient := fleetVerdictSource(service, func() time.Time { return at }, 3*time.Hour, time.Second)()
	if got := countOf(patient.Checks, string(fleet.CheckRoundsStalled)).Count; got != 0 {
		t.Fatalf("fleet_checks{code=ROUNDS_STALLED} = %d against a three hour budget, want 0", got)
	}
}
