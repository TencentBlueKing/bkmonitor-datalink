// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The production bundle publishes the view its Worker actually holds: after
// the fixture's one FULL Slot, the view is that one Query Group, sized by
// what the store holds under the digests its Segment names, beside an owned
// count of two. The crossing pinned is repository -> bundle's owned set ->
// recorder; a source left unbound publishes no series at all, and a source
// bound to the cache's residency would count both Query Groups' objects,
// which the activation read to compile them.
//
// The effective-time maintenance loop is not started. Once a second it reads
// every owned Query Group's Plans by content, which puts the sibling in the
// view as rightly as a Slot does; left running, the count was 2 whenever the
// fixture outlasted its first tick after the view opened, which under the
// race detector on a loaded machine it did in about one run in fifty.
func TestTheProductionBundlePublishesTheViewItsWorkerHolds(t *testing.T) {
	fixture := startCutoverFixtureOpened(t, nil, observability.Discard(observability.ComponentRuntime), nil,
		func(bundle *phaseTwoWorkerBundle) { bundle.dependencies.RunEffectiveTime = nil })
	ctx := context.Background()
	gathered := gatherPhaseTwoGauges(t, fixture)
	if owned := gathered["alarmd_worker_owned_query_groups{worker_role=complete}"]; owned != 2 {
		t.Fatalf("owned = %v, want the fixture's two", owned)
	}
	segment := fixture.initialSchedule.Segment
	prefix := productionPhaseTwoPrefix(fixture.cfg.Redis.StatePrefix, "catalog")
	wantObject := fixture.redisClient.StrLen(ctx, prefix+":qgobj:"+string(segment.ObjectDigest)).Val()
	var wantContexts int64
	counted := map[execution.OutputContextDigest]struct{}{}
	for _, ref := range segment.OutputContextRefs {
		if _, seen := counted[ref.Digest]; seen {
			continue
		}
		counted[ref.Digest] = struct{}{}
		wantContexts += fixture.redisClient.StrLen(ctx, prefix+":outctx:"+string(ref.Digest)).Val()
	}
	if wantObject == 0 || wantContexts == 0 {
		t.Fatalf("the store holds nothing under the Segment's digests: object=%d contexts=%d", wantObject, wantContexts)
	}
	if got := gathered["alarmd_local_view_query_groups"]; got != 1 {
		t.Fatalf("local_view_query_groups = %v, want 1: the one Query Group whose Slot ran (owned is 2)", got)
	}
	if got := gathered["alarmd_local_view_object_bytes{kind=query_group}"]; got != float64(wantObject) {
		t.Fatalf("local_view_object_bytes{query_group} = %v, want the stored object's %d bytes", got, wantObject)
	}
	if got := gathered["alarmd_local_view_object_bytes{kind=output_context}"]; got != float64(wantContexts) {
		t.Fatalf("local_view_object_bytes{output_context} = %v, want the stored contexts' %d bytes", got, wantContexts)
	}
}

// gatherPhaseTwoGauges reads every gauge the fixture's recorder publishes,
// keyed by name and, where labelled, by name{label=value,...}.
func gatherPhaseTwoGauges(t *testing.T, fixture *cutoverStallFixture) map[string]float64 {
	t.Helper()
	families, err := fixture.bundle.dependencies.Recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	gathered := map[string]float64{}
	for _, family := range families {
		if family.GetType() != dto.MetricType_GAUGE {
			continue
		}
		for _, series := range family.Metric {
			key := family.GetName()
			if len(series.Label) > 0 {
				key += "{"
				for index, pair := range series.Label {
					if index > 0 {
						key += ","
					}
					key += pair.GetName() + "=" + pair.GetValue()
				}
				key += "}"
			}
			gathered[key] = series.GetGauge().GetValue()
		}
	}
	return gathered
}

// Of the fixture's two owned Query Groups, only the one whose Slot ran has
// prepared a Schedule. Both are in worker_owned_query_groups; read_hold_groups
// counts the prepared one under the source its query plan names, and the
// other under none, other included, until it prepares.
func TestReadHoldGroupsCoverPreparedGroupsAndOwnedCountsEvery(t *testing.T) {
	fixture := startCutoverFixtureOpened(t, nil, observability.Discard(observability.ComponentRuntime), nil,
		func(bundle *phaseTwoWorkerBundle) { bundle.dependencies.RunEffectiveTime = nil })
	gathered := gatherPhaseTwoGauges(t, fixture)
	if owned := gathered["alarmd_worker_owned_query_groups{worker_role=complete}"]; owned != 2 {
		t.Fatalf("owned = %v, want the fixture's two", owned)
	}
	holds := fixture.bundle.dependencies.ReadHolds
	holds.mu.Lock()
	held, prepared := len(holds.groups), map[execution.QueryGroupIdentity]string{}
	for qg, group := range holds.groups {
		if group.semantics != "" {
			prepared[qg] = group.semantics
		}
	}
	holds.mu.Unlock()
	if held != 2 || len(prepared) != 1 {
		t.Fatalf("read holds bound %d groups with %d prepared (%v), want both owned groups bound and one prepared", held, len(prepared), prepared)
	}
	if prepared[fixture.initialSchedule.Segment.QueryGroup] == "" {
		t.Fatalf("prepared %v, want the group whose Slot ran", prepared)
	}
	counts := holds.groupsBySource(nil)
	if _, other := counts[controlplane.SourceSemanticsOther]; other {
		t.Fatalf("read_hold_groups counts %+v, want nothing under other", counts)
	}
	for source := range counts {
		if source != prepared[fixture.initialSchedule.Segment.QueryGroup] {
			t.Fatalf("read_hold_groups counts %+v, want only the prepared group's source", counts)
		}
	}
}
