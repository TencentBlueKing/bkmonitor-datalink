// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func refreshEvent(t *testing.T, facts *SourceRefreshFacts) map[string]any {
	t.Helper()
	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentControlPlane, Stage: StageSnapshotRefreshed, Result: ResultSuccess,
		SourceRefresh: facts,
	})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode source refresh log: %v; log=%s", err, output.String())
	}
	return event
}

// Whoever reads these records matches on the field name, so the split between
// the published revision and the activated one only exists if the two arrive
// under different names. Asserting on the struct alone would let a rename pass.
func TestTheActivatedRevisionTravelsUnderItsOwnFieldName(t *testing.T) {
	event := refreshEvent(t, &SourceRefreshFacts{
		Status: SourceRefreshPending, ObservationID: "observation-candidate",
		ActivatedRevision: "snapshot-activated", ActivatedEpoch: 7,
		ActiveQueryGroups: 943, ActiveQueryGroupsKnown: true,
	})

	if event["activated_snapshot_revision"] != "snapshot-activated" {
		t.Fatalf("activated_snapshot_revision = %#v", event["activated_snapshot_revision"])
	}
	if event["activated_publication_epoch"] != float64(7) {
		t.Fatalf("activated_publication_epoch = %#v", event["activated_publication_epoch"])
	}
	if event["active_query_groups"] != float64(943) {
		t.Fatalf("active_query_groups = %#v", event["active_query_groups"])
	}
	// The published names stay empty on a round that published nothing; filling
	// them is the defect this split exists to prevent.
	if event["snapshot_revision"] != nil || event["publication_epoch"] != nil {
		t.Fatalf("activated revision leaked into the published names: %#v", event)
	}
	// A size is not a change. These four say something moved, and nothing moved.
	for _, name := range []string{"old_query_groups", "new_query_groups",
		"added_query_groups", "retired_query_groups"} {
		if event[name] != nil {
			t.Fatalf("%s present on a round that measured no change: %#v", name, event[name])
		}
	}
}

// The active size is reported only when it was actually read. Defaulting it to
// zero would put a confident "0 Query Groups are active" in the record of a
// round that simply could not count them.
func TestAnUncountedActiveSetIsAbsentRatherThanZero(t *testing.T) {
	event := refreshEvent(t, &SourceRefreshFacts{
		Status: SourceRefreshPending, ObservationID: "observation-candidate",
		ActivatedRevision: "snapshot-activated", ActivatedEpoch: 7,
	})

	if _, present := event["active_query_groups"]; present {
		t.Fatalf("uncounted active set reported as a number: %#v", event["active_query_groups"])
	}
}
