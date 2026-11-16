// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// cloudHostFacts is the frozen facts of a query grouped by cloud and host,
// cloud first, as a host metric's group-by is.
func cloudHostFacts() *execution.QueryPlanFacts {
	return &execution.QueryPlanFacts{Provider: execution.ProviderUQ, QueryList: []execution.QueryClause{{
		TableID: "system.cpu_summary", Dimensions: []string{"cloud", "host"},
		Functions: []execution.QueryFunction{{Method: "mean", Dimensions: []string{"cloud", "host"}}},
	}}}
}

// A Query Group grouped by cloud and host whose cloud is "0" throughout, as
// a host metric's cloud id is, past half its share (largeSlicedFixture: 24
// hosts node-<c>, 40 KiB each, c spread over 0-9a-z in order). The Slot
// after the one that read it starts in two slices asked, by its bytes, on
// the cut that Slot's sample made.
//
// That Slot read it whole and let it go at the tenth host, over the slice
// limit of half the 768 KiB share: its sample is the first ten, ending in 0
// 1 3 4 6 7 9 a c d. On cloud it is one value; on host, by the series each
// last character carries (one each), the classes are [013], [46], [79a] and
// the rest - 3, 2 and 3 hosts, and 16, 640 KiB, in the rest: the sample saw
// only the start of a stream whose last characters are sorted slowest (the
// limit 7.3 names). So the Slot after reads the four classes and the
// exhausted slice (empty) and lets the rest go, a slice it started in; the
// rest's own sample, its first ten (c to p), refines its class into [cdf],
// [gi], [jlm] and the rest - 3, 2, 3 and 8 hosts, within the limit. On the
// first dimension by the fixed alphabet "0" would put every host in one
// slice.
func TestAFirstDimensionOfOneValueStartsInEvenSlices(t *testing.T) {
	fixture := largeSlicedFixture(t)
	for index := range fixture.source.series {
		fixture.source.series[index].cloud = "0"
	}
	fixture.source.facts = cloudHostFacts()
	request := windowedRequest()
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the Slot before: result=%+v err=%v", result, err)
	}
	first := len(fixture.source.calls)
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("result=%+v err=%v, want it completed", result, err)
	}
	if result.Usage.InputSlicesDiscarded != 1 || result.Usage.InputSliceStartDiscarded != 1 ||
		result.Usage.InputSliceCutOn != observability.InputSliceCutOnRootSplit {
		t.Fatalf("usage %+v, want the rest let go, a slice started in, on the root split's cut", result.Usage)
	}
	// By slice: a read ahead let go is read again, the same slice.
	landed, empty, largest := map[string]bool{}, map[string]bool{}, 0
	for _, call := range fixture.source.calls[first:] {
		for _, condition := range call.slice.Conditions {
			if condition.Dimension != "host" {
				t.Fatalf("a slice cut on %s, want host only", condition.Dimension)
			}
		}
		if !call.completed {
			continue
		}
		landed[call.slice.Digest()] = true
		largest = max(largest, len(call.delivered))
		if len(call.delivered) == 0 {
			empty[call.slice.Digest()] = true
		}
	}
	if len(landed) != 8 || len(empty) != 1 || largest != 8 {
		t.Fatalf("landed %d slices, %d empty, the largest %d hosts; want eight, the exhausted one empty, eight hosts the largest",
			len(landed), len(empty), largest)
	}
}

// cloudFacts is the frozen facts of a query grouped by cloud alone.
func cloudFacts() *execution.QueryPlanFacts {
	return &execution.QueryPlanFacts{Provider: execution.ProviderUQ, QueryList: []execution.QueryClause{{
		TableID: "system.cpu_summary", Dimensions: []string{"cloud"},
		Functions: []execution.QueryFunction{{Method: "mean", Dimensions: []string{"cloud"}}},
	}}}
}

// A Query Group read in slices on host, then grouped by cloud alone. The
// first Slot so grouped starts on the dimensions its memory holds, host,
// which its queries no longer slice on: the slices are let go at Begin,
// before any query is sent, and the Slot is read whole to sample - which the
// provider times out before any series reaches the sample. It completes
// degraded, keeps the size the read before saw, and the Slot after it is
// read in slices on its first dimension, cloud, the bytes asking for two.
// Read whole again, it would time out again, every round; read on host, each
// slice would answer a part of every cloud's aggregate.
func TestAQueryGroupWhoseCutDimensionWentAndTimedOutWholeIsSlicedNext(t *testing.T) {
	fixture := largeSlicedFixture(t)
	for index := range fixture.source.series {
		series := &fixture.source.series[index]
		series.cloud = "c" + series.host[len(series.host)-1:]
	}
	request := windowedRequest()
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the Slot before: result=%+v err=%v", result, err)
	}
	fixture.source.mu.Lock()
	fixture.source.facts, fixture.source.timeoutWhole = cloudFacts(), true
	fixture.source.mu.Unlock()
	// Grouped otherwise, its query is another revision (execution query_plan.go:372).
	request.Contract.QueryRevision += "-by-cloud"
	regrouped := len(fixture.source.calls)
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the first Slot grouped otherwise: result=%+v err=%v, want it completed", result, err)
	}
	for _, call := range fixture.source.calls[regrouped:] {
		if len(call.delivered) > 0 && !call.slice.IsZero() {
			t.Fatalf("the first Slot grouped otherwise read %d series in a slice %s, want none read in slices", len(call.delivered),
				call.slice.Digest())
		}
	}
	first := len(fixture.source.calls)
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("the Slot after: result=%+v err=%v, want it completed", result, err)
	}
	calls := fixture.source.calls[first:]
	if len(calls) < 2 || calls[0].slice.IsZero() || result.Usage.InputSliceCutOn != observability.InputSliceCutOnFirstDimension {
		t.Fatalf("the Slot after read %d times (first whole %t), cut on %q: want it in slices on its first dimension",
			len(calls), len(calls) > 0 && calls[0].slice.IsZero(), result.Usage.InputSliceCutOn)
	}
	for _, call := range calls {
		for _, condition := range call.slice.Conditions {
			if condition.Dimension != "cloud" {
				t.Fatalf("a slice cut on %s, want cloud", condition.Dimension)
			}
		}
	}
}

// A numbered fleet, host-00 to host-23, 30 KiB a series, past half its
// share (768 KiB, worker qgShareBytes over a 1 MiB pool): its values end in
// digits, as addresses and numbered names do. The fixed alphabet's classes
// are [0-8], [9a-h], [i-q] and the rest: [0-8] holds 22 of the 24 hosts,
// 660 KiB, past the slice limit of half the share (384 KiB), let go Slot
// after Slot. The Slot that reads it whole streams twelve hosts and lets it
// go at the thirteenth, and cuts its root by that sample's classes
// (inputslice.BestCut): last digits 0 to 2 twice, 3 to 9 once, split by the
// series they carry into [01], [23], [456] and the rest - six hosts each,
// 180 KiB. The Slot after starts in that cut (two slices asked, 720 KiB over
// half the share): five slices, six hosts each at most, none let go.
func TestANumberedFleetStartsInEvenSlices(t *testing.T) {
	fixture := newSlicedFixture(t, slicedHosts(24, 30<<10), sliceableFacts(), 200)
	fixture.source.queryMillis = 1_000
	request := windowedRequest()
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the Slot before: result=%+v err=%v", result, err)
	}
	first := len(fixture.source.calls)
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Usage.InputSlicesDiscarded != 0 || result.Usage.InputSliceStartDiscarded != 0 {
		t.Fatalf("result=%+v err=%v, want it completed with no slice let go", result, err)
	}
	read, largest := map[string]bool{}, 0
	for _, call := range fixture.source.calls[first:] {
		read[call.slice.Digest()] = true
		largest = max(largest, len(call.delivered))
	}
	if len(read) != 5 || largest != 6 {
		t.Fatalf("read %d slices, the largest %d hosts; want five, six hosts the largest", len(read), largest)
	}
}
