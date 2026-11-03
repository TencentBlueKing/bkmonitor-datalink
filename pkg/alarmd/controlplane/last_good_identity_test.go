// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
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

// A strategy whose document does not compile this round keeps running on
// its last good definition -- the definition of that strategy. When the
// document now states another tenant, business, space or global switch, the
// number names another strategy (a writer whose numbering started over), and
// the old Plan is not run under the new one's name: it is named
// LAST_GOOD_IDENTITY_CHANGED and counted. Where there is no document to read
// an identity from -- unreadable, or no longer listed -- nothing says the
// strategy changed and the last good definition is kept as before.
func TestBuildCatalogKeepsALastGoodPlanOnlyForTheStrategyItWasBuiltFor(t *testing.T) {
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(payload, &documents); err != nil {
		t.Fatal(err)
	}
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	previous, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{
			{SourceID: "1001", Document: documents[0], Identity: identity},
			{SourceID: "1002", Document: documents[1], Identity: identity},
		},
		Planner: &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil || len(previous.QueryGroups) != 1 || len(previous.QueryGroups[0].Plans) != 2 {
		t.Fatalf("previous catalog = %+v, %v; want both strategies in one group", previous, err)
	}
	lastGood := &controlplane.PublishedSnapshot{
		Publication: controlplane.SnapshotPublicationRef{SnapshotRevision: previous.SnapshotRevision, PublicationEpoch: 1},
		QueryGroups: previous.QueryGroups,
	}
	incomplete := controlplane.ObjectDisposition{SourceID: "1002", Scope: "STRATEGY",
		Disposition: controlplane.DispositionSourceIncomplete, Reason: "SOURCE_READ_INCOMPLETE"}
	rejected := controlplane.ObjectDisposition{SourceID: "1002", Scope: "STRATEGY",
		Disposition: controlplane.DispositionConfigRejected, Reason: "STRATEGY_DOCUMENT_INVALID"}
	otherBusiness := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "3", SpaceScope: "bkcc__3"}
	global := identity
	global.GlobalBusiness = true
	broken := json.RawMessage(`{"id":1002}`)
	for name, tc := range map[string]struct {
		second  *controlplane.SourceStrategy
		kept    bool
		changed int
	}{
		"unreadable, same identity":      {&controlplane.SourceStrategy{SourceID: "1002", Identity: identity, SourceDisposition: &incomplete}, true, 0},
		"unreadable, another business":   {&controlplane.SourceStrategy{SourceID: "1002", Identity: otherBusiness, SourceDisposition: &incomplete}, false, 1},
		"unreadable, now global":         {&controlplane.SourceStrategy{SourceID: "1002", Identity: global, SourceDisposition: &incomplete}, false, 1},
		"unreadable, no identity stated": {&controlplane.SourceStrategy{SourceID: "1002", SourceDisposition: &incomplete}, true, 0},
		"rejected, same identity":        {&controlplane.SourceStrategy{SourceID: "1002", Document: documents[1], Identity: identity, SourceDisposition: &rejected}, true, 0},
		"rejected, another business":     {&controlplane.SourceStrategy{SourceID: "1002", Document: documents[1], Identity: otherBusiness, SourceDisposition: &rejected}, false, 1},
		"no longer listed":               {nil, true, 0},
		"does not compile, same":         {&controlplane.SourceStrategy{SourceID: "1002", Document: broken, Identity: identity}, true, 0},
		"does not compile, another one":  {&controlplane.SourceStrategy{SourceID: "1002", Document: broken, Identity: otherBusiness}, false, 1},
	} {
		strategies := []controlplane.SourceStrategy{{SourceID: "1001", Document: documents[0], Identity: identity}}
		if tc.second != nil {
			strategies = append(strategies, *tc.second)
		}
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
			Strategies: strategies, Planner: &recordingPlanner{facts: queryFacts(t)}, LastGood: lastGood,
		})
		if err != nil {
			t.Fatalf("%s: BuildCatalog() error = %v", name, err)
		}
		want := []string{"1001"}
		if tc.kept {
			want = []string{"1001", "1002"}
		}
		named := false
		for _, disposition := range catalog.Dispositions {
			named = named || (disposition.SourceID == "1002" && disposition.Reason == "LAST_GOOD_IDENTITY_CHANGED")
		}
		if got := catalogStrategyIDs(catalog); !reflect.DeepEqual(got, want) || catalog.LastGoodIdentityChanged != tc.changed ||
			named != (tc.changed > 0) || catalog.RetainedStaleRevisions != 0 {
			t.Errorf("%s: plans %v, identity changed %d (named %v), stale %d; want plans %v, identity changed %d",
				name, got, catalog.LastGoodIdentityChanged, named, catalog.RetainedStaleRevisions, want, tc.changed)
		}
	}
}
