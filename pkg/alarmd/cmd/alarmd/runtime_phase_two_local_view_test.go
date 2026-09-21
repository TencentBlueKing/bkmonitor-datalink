// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The production bundle publishes the view its Worker actually holds: after
// the fixture's one FULL Slot, the view is that one Query Group, sized by
// what the store holds under the digests its Segment names, beside an owned
// count of two. The crossing pinned is repository -> bundle's owned set ->
// recorder; a source left unbound publishes no series at all, and a source
// bound to the cache's residency would count both Query Groups' objects,
// which the activation read to compile them.
func TestTheProductionBundlePublishesTheViewItsWorkerHolds(t *testing.T) {
	fixture := startCutoverFixture(t, nil)
	ctx := context.Background()
	gathered := gatherPhaseTwoGauges(t, fixture)
	if owned := gathered["bkmonitor_alarmd_worker_owned_query_groups{worker_role=complete}"]; owned != 2 {
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
	if got := gathered["bkmonitor_alarmd_local_view_query_groups"]; got != 1 {
		t.Fatalf("local_view_query_groups = %v, want 1: the one Query Group whose Slot ran (owned is 2)", got)
	}
	if got := gathered["bkmonitor_alarmd_local_view_object_bytes{kind=query_group}"]; got != float64(wantObject) {
		t.Fatalf("local_view_object_bytes{query_group} = %v, want the stored object's %d bytes", got, wantObject)
	}
	if got := gathered["bkmonitor_alarmd_local_view_object_bytes{kind=output_context}"]; got != float64(wantContexts) {
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
