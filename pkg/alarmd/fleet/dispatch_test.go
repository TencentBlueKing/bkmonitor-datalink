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
)

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
