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
	"testing"
	"time"

	"encoding/json"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Freezing a Slot reports how many no-data Plans the object store handed back
// and how many survived compilation.
//
// These are the two hops between the published bytes and the round that judges
// them, and the reason they are counted rather than argued is that the call
// graph has said "this hop carries the section" three times while production
// read zero at the end of it. Both are reported on every Slot, so a zero here
// is a measurement rather than an absence.
//
// It runs against a Segment the activation reconciler cut, which is the only
// kind that takes the content path. An earlier version of this test built the
// Segment by hand; a hand-built one carries no object digest, so it took the
// Snapshot fallback and proved nothing about the road production drives on.
func TestFreezingASlotReportsTheAssembledAndFrozenNoDataPlans(t *testing.T) {
	fixture := newNoDataHopFixture(t, "no-data-hops", noDataSourceCatalog(t))
	fixture.freeze(t)

	if got := fixture.hops[observability.NoDataHopAssembled]; got != 1 {
		t.Fatalf("assembled = %d, want the one no-data Plan the object store handed back; hops=%+v",
			got, fixture.hops)
	}
	if got := fixture.hops[observability.NoDataHopFrozen]; got != 1 {
		t.Fatalf("frozen = %d, want the one that survived compilation; hops=%+v. A difference from "+
			"assembled is the section being lost in the compile, which is one of the hops this exists "+
			"to separate", got, fixture.hops)
	}
}

// A Slot whose Plans detect nothing reports zero at both hops, rather than
// reporting nothing.
//
// Without this the two hops would answer only when the answer is good news,
// which is the failure mode every other no-data signal already had.
func TestFreezingASlotWithoutNoDataPlansReportsZeroAtBothHops(t *testing.T) {
	fixture := newNoDataHopFixture(t, "no-data-hops-empty", validCatalog(t, 80))
	reported := map[string]int{}
	fixture.repository.ConfigureObserver(observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			if facts := observation.NoDataCensus; facts != nil {
				reported[facts.Hop]++
			}
		}))
	schedule, err := fixture.runtime.ReadFrozenSchedule(context.Background(), fixture.group, 60)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runtime.FreezeSlotContract(context.Background(), execution.FreezeSlotContractRequest{
		QueryGroup: schedule.Segment.QueryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: 60, DuePlans: schedule.DuePlanRefs(60),
	}); err != nil {
		t.Fatal(err)
	}

	for _, hop := range []string{
		observability.NoDataHopAssembledBytes, observability.NoDataHopAssembled, observability.NoDataHopFrozen,
	} {
		if reported[hop] != 1 {
			t.Fatalf("hop %q was reported %d times on a Slot with no such Plan, want exactly once: a hop "+
				"that only speaks when it has something to say cannot answer why the next one is empty",
				hop, reported[hop])
		}
	}
}

// noDataSourceCatalog builds a Catalog from a strategy whose item asks for
// no-data detection, so the section arrives the way production's does.
func noDataSourceCatalog(t *testing.T) controlplane.Catalog {
	t.Helper()
	document := realThresholdDocuments(t)[0]
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	items, _ := value["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("fixture has %d items, want one to attach no-data to", len(items))
	}
	item, _ := items[0].(map[string]any)
	item["no_data_config"] = map[string]any{
		"is_enabled": true, "continuous": 1, "level": 2,
		"agg_dimension": []any{"bk_target_ip", "bk_target_cloud_id"},
	}
	edited, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "1001", Document: edited,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		Planner: &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 {
		t.Fatalf("catalog=%#v, want one Plan", catalog.QueryGroups)
	}
	if catalog.QueryGroups[0].Plans[0].Plan.NoData == nil {
		t.Fatal("the source document asked for no-data and the compiled Plan has no section; this test " +
			"would then measure nothing")
	}
	return catalog
}

// Publishing reports how many no-data Plans the bytes carry, and reports none
// as none.
//
// The count is computed while the payloads are built, which is not the same as
// it reaching a reader: deleting the one line that reports it leaves every
// other assertion about the count standing. This drives the publication the
// leader performs and reads what came out of it.
func TestPublishingReportsTheNoDataPlansTheBytesCarry(t *testing.T) {
	for name, test := range map[string]struct {
		prefix  string
		catalog func(*testing.T) controlplane.Catalog
		want    int
	}{
		"a publication carrying one": {prefix: "one", catalog: noDataSourceCatalog, want: 1},
		"a publication carrying none": {
			prefix:  "none",
			catalog: func(t *testing.T) controlplane.Catalog { return validCatalog(t, 80) }, want: 0,
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := newControlplaneRedis(t)
			ctx := context.Background()
			repository, err := controlplane.NewRedisCatalogRepository(
				client, "alarmd:control:no-data-publish-"+test.prefix, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			reported := 0
			published := -1
			repository.ConfigureObserver(observability.ObserverFunc(
				func(_ context.Context, observation observability.Observation) {
					facts := observation.NoDataCensus
					if facts == nil || facts.Hop != observability.NoDataHopPublished {
						return
					}
					reported++
					published = facts.Plans
				}))

			if _, _, err := repository.PublishCatalog(ctx, test.catalog(t)); err != nil {
				t.Fatal(err)
			}

			if reported != 1 {
				t.Fatalf("the published hop was reported %d times, want once per publication. A count "+
					"computed and never reported reads exactly like a hop that carries none", reported)
			}
			if published != test.want {
				t.Fatalf("published = %d, want %d", published, test.want)
			}
		})
	}
}
