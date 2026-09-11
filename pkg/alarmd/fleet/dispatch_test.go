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
	"strings"
	"testing"
	"time"
)

// The two tests below assert what a replica puts on the wire, not what
// Aggregate leaves in a Go pointer.
//
// The distinction was not obvious and was measured rather than reasoned: drop
// the `omitempty` from Snapshot.Dispatch and both aggregation tests stay green
// while every non-suppressing replica starts publishing `"dispatch":null`. The
// page survives that by luck -- it asks `if (!d.dispatch)`, and null is falsy --
// but the contract this whole field exists to state is "the key is absent", and
// a reader that checks for the key, or any other language reading this JSON,
// gets the opposite answer. A guard that passes on the shape the contract
// forbids is not guarding the contract.
//
// The declaration ships before the code that writes it, so the guard belongs
// here beside the declaration rather than with the writer.
func TestAReplicaThatSuppressesNothingPutsNoDispatchKeyOnTheWire(t *testing.T) {
	at := time.Date(2026, 9, 11, 17, 0, 0, 0, time.UTC)

	encoded, err := json.Marshal(Snapshot{Replica: "pod-a", TakenAt: at, Owned: 1, Determined: 1})
	if err != nil {
		t.Fatalf("encode the snapshot: %v", err)
	}
	if strings.Contains(string(encoded), `"dispatch"`) {
		t.Fatalf("a replica that suppresses nothing published a dispatch key: %s", encoded)
	}
}

// The other half: a replica that does suppress publishes the key even with
// nothing skipped yet. Without this the fix above could be "never publish it"
// and still pass, and a page would read a suppressing deployment as a shadow
// one -- the exact reassurance this field exists to withhold.
func TestAReplicaThatSuppressesPutsTheKeyOnTheWireEvenAtZero(t *testing.T) {
	at := time.Date(2026, 9, 11, 17, 0, 0, 0, time.UTC)

	encoded, err := json.Marshal(Snapshot{
		Replica: "pod-a", TakenAt: at, Owned: 1, Determined: 1,
		Dispatch: &DispatchSuppression{Skipped: map[string]uint64{}},
	})
	if err != nil {
		t.Fatalf("encode the snapshot: %v", err)
	}
	if !strings.Contains(string(encoded), `"dispatch"`) {
		t.Fatalf("a suppressing replica published no dispatch key: %s", encoded)
	}
}

// The whole point of the type. A build that suppresses nothing cannot produce
// this field, so its absence is what tells the page that the overdue count
// beside it is structurally zero rather than good news.
func TestABuildThatSuppressesNothingReportsNoSuppressionAtAll(t *testing.T) {
	at := time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		{Replica: "pod-a", TakenAt: at, Owned: 10, Determined: 10},
	}, []string{"pod-a"}, at, time.Minute)

	if view.Dispatch != nil {
		t.Fatalf("suppression = %+v, want absence from a build that parks nothing", view.Dispatch)
	}
}

// One replica reporting is enough for the deployment to be suppressing, and the
// counts add up because each replica holds back its own dispatches. A fleet
// half way through a rollout is suppressing, and reporting it as absent would
// tell the page to read the overdue count as meaningless exactly while it is
// becoming meaningful.
func TestSuppressionAddsUpAndSurvivesAHalfRolledDeployment(t *testing.T) {
	at := time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		{Replica: "pod-a", TakenAt: at, Owned: 10, Determined: 10,
			Dispatch: &DispatchSuppression{Skipped: map[string]uint64{"not_due": 900}, Parked: 4}},
		{Replica: "pod-b", TakenAt: at, Owned: 10, Determined: 10},
	}, []string{"pod-a", "pod-b"}, at, time.Minute)

	if view.Dispatch == nil {
		t.Fatal("a deployment with a suppressing replica reported no suppression")
	}
	if view.Dispatch.Skipped["not_due"] != 900 || view.Dispatch.Parked != 4 {
		t.Fatalf("suppression = %+v, want the reporting replica's figures kept", view.Dispatch)
	}
}
