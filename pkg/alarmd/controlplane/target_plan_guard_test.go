// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A new target protocol must not be ignored, decoded as the old target, or
// served by the last good Plan. A sibling using the old protocol still runs.
func TestTargetPlanIsRefusedWithoutFallingBackToLegacyTargets(t *testing.T) {
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(payload, &documents); err != nil {
		t.Fatal(err)
	}
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	originals := []controlplane.SourceStrategy{
		{SourceID: "1001", Document: documents[0], Identity: identity},
		{SourceID: "1002", Document: documents[1], Identity: identity},
	}
	previous, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: originals, Planner: &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil || !reflect.DeepEqual(catalogStrategyIDs(previous), []string{"1001", "1002"}) {
		t.Fatalf("legacy baseline: catalog=%+v err=%v", previous, err)
	}
	lastGood := &controlplane.PublishedSnapshot{
		Publication: controlplane.SnapshotPublicationRef{SnapshotRevision: previous.SnapshotRevision, PublicationEpoch: 1},
		QueryGroups: previous.QueryGroups,
	}
	for _, test := range []struct {
		name       string
		plan       string
		target     string
		secondItem bool
		incomplete bool
		cached     bool
		field      string
	}{
		{name: "valid plan with empty legacy target", plan: `{"schema_version":1,"model_id":"host","target_rule":"host_id","failure_policy":"no_match","static_targets":[{"bk_host_id":42}],"dynamic_groups":[],"dynamic_topologies":[]}`, target: `[[]]`, cached: true, field: "items[0].target_plan"},
		{name: "null is present", plan: `null`, field: "items[0].target_plan"},
		{name: "empty object is present", plan: `{}`, field: "items[0].target_plan"},
		{name: "unknown version", plan: `{"schema_version":99}`, field: "items[0].target_plan"},
		{name: "wrong type", plan: `[]`, field: "items[0].target_plan"},
		{name: "new target object cannot trigger stale fallback", plan: `{"schema_version":1}`, target: `{"schema_version":1,"model_id":"host","selectors":[]}`, field: "items[0].target_plan"},
		{name: "new field on later item", plan: `{}`, secondItem: true, field: "items[1].target_plan"},
		{name: "incomplete source cannot retain old target", plan: `{}`, incomplete: true, field: "items[0].target_plan"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var cache *controlplane.CandidateCache
			wantQueryCompilations := 1
			if test.cached {
				cache = controlplane.NewCandidateCache()
				warm, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
					Strategies: originals, Planner: &recordingPlanner{facts: queryFacts(t)}, Cache: cache,
				})
				if err != nil || !reflect.DeepEqual(warm, previous) {
					t.Fatalf("warm cache changed the legacy catalog: err=%v", err)
				}
				wantQueryCompilations = 0 // The healthy sibling is already cached.
			}
			var document map[string]json.RawMessage
			if err := json.Unmarshal(documents[1], &document); err != nil {
				t.Fatal(err)
			}
			var items []map[string]json.RawMessage
			if err := json.Unmarshal(document["items"], &items); err != nil {
				t.Fatal(err)
			}
			index := 0
			if test.secondItem {
				items = append(items, map[string]json.RawMessage{"id": json.RawMessage(`2`)})
				index = 1
			}
			items[index]["target_plan"] = json.RawMessage(test.plan)
			if test.target != "" {
				items[index]["target"] = json.RawMessage(test.target)
			}
			document["items"], err = json.Marshal(items)
			if err != nil {
				t.Fatal(err)
			}
			changed := originals[1]
			changed.Document, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if test.incomplete {
				changed.SourceDisposition = &controlplane.ObjectDisposition{SourceID: "1002", Scope: "STRATEGY", Disposition: controlplane.DispositionSourceIncomplete, Reason: "SOURCE_READ_INCOMPLETE"}
			}
			planner := &recordingPlanner{facts: queryFacts(t)}
			request := controlplane.BuildRequest{
				Strategies: []controlplane.SourceStrategy{originals[0], changed}, Planner: planner, LastGood: lastGood, Cache: cache,
			}
			catalog, err := controlplane.BuildCatalog(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if got := catalogStrategyIDs(catalog); !reflect.DeepEqual(got, []string{"1001"}) || planner.calls != wantQueryCompilations {
				t.Fatalf("new target was executed or retained: plans=%v query compilations=%d dispositions=%+v", got, planner.calls, catalog.Dispositions)
			}
			found := false
			for _, disposition := range catalog.Dispositions {
				if disposition.SourceID == "1002" && disposition.Disposition == controlplane.DispositionUnsupported && disposition.Reason == "UNSUPPORTED_TARGET_PLAN" && disposition.FieldPath == test.field {
					found = true
				}
			}
			if !found {
				t.Fatalf("new target was not refused by name and field: %+v", catalog.Dispositions)
			}
			if test.cached {
				repeated, err := controlplane.BuildCatalog(context.Background(), request)
				compiled, reused := cache.Stats()
				if err != nil || !reflect.DeepEqual(repeated, catalog) || compiled != 0 || reused != 2 || planner.calls != 0 {
					t.Fatalf("cached refusal changed: compiled=%d reused=%d query compilations=%d err=%v", compiled, reused, planner.calls, err)
				}
			}
			// Removing the new field returns to the exact original catalog,
			// including its frozen Plan bytes and revision.
			restored, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
				Strategies: originals, Planner: &recordingPlanner{facts: queryFacts(t)}, Cache: cache,
				LastGood: &controlplane.PublishedSnapshot{QueryGroups: catalog.QueryGroups},
			})
			if err != nil || !reflect.DeepEqual(restored, previous) {
				t.Fatalf("legacy catalog changed after removing the new field: err=%v", err)
			}
		})
	}
}
