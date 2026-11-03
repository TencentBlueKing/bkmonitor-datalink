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
	"context"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// An object keeps the ranges its latest round's primary queries asked of the
// provider and accept, one per physical query: the request's facts, so a
// round whose query failed still sent them and replaces them. At most four,
// in digest order, with the count of all; an observation that names none
// leaves them.
func TestAnObjectKeepsTheRangesItsLatestRoundAsked(t *testing.T) {
	d := newDefectTracker(t, "qg-events")
	query := func(slot int64, result observability.Result, ranges ...observability.QueryRangeFacts) {
		d.tick()
		d.tracker.Observe(context.Background(), observability.Observation{Component: observability.ComponentAccess,
			Stage: observability.StageQueryCompleted, Result: result, QueryRanges: ranges,
			Trace: observability.TraceFields{QueryGroupKey: d.group, StrategyID: "410", BusinessID: "2", EvaluationTime: slot}})
	}
	read := func() (QueryRanges, bool) {
		t.Helper()
		return d.tracker.QueryRanges(d.group)
	}
	if _, known := read(); known {
		t.Fatal("an object that asked nothing yet has ranges")
	}
	short := observability.QueryRangeFacts{Digest: "query-b", AskedSeconds: 120, AcceptedSeconds: 60}
	long := observability.QueryRangeFacts{Digest: "query-a", AskedSeconds: 600, AcceptedSeconds: 60}
	query(60, observability.ResultSuccess, short, long)
	got, known := read()
	want := QueryRanges{Slot: 60, Total: 2, Ranges: []QueryRangeFact{
		{Digest: "query-a", AskedSeconds: 600, AcceptedSeconds: 60}, {Digest: "query-b", AskedSeconds: 120, AcceptedSeconds: 60}}}
	if !known || !reflect.DeepEqual(got, want) {
		t.Fatalf("after a round with two primaries: %+v, want %+v", got, want)
	}
	query(120, observability.ResultFailed, observability.QueryRangeFacts{Digest: "query-a", AskedSeconds: 660, AcceptedSeconds: 60})
	if got, _ := read(); got.Slot != 120 || got.Total != 1 || got.Ranges[0].AskedSeconds != 660 {
		t.Fatalf("after a failed round that still asked: %+v, want its range at its Slot", got)
	}
	query(180, observability.ResultSuccess)
	if got, _ := read(); got.Slot != 120 {
		t.Fatalf("an observation naming no range replaced them: %+v", got)
	}
	var many []observability.QueryRangeFacts
	for _, digest := range []string{"query-f", "query-e", "query-d", "query-c", "query-b", "query-a"} {
		many = append(many, observability.QueryRangeFacts{Digest: digest, AskedSeconds: 60, AcceptedSeconds: 60})
	}
	query(240, observability.ResultSuccess, many...)
	got, _ = read()
	digests := []string{}
	for _, item := range got.Ranges {
		digests = append(digests, item.Digest)
	}
	if got.Total != 6 || !reflect.DeepEqual(digests, []string{"query-a", "query-b", "query-c", "query-d"}) {
		t.Fatalf("six primaries kept %v of %d, want the first four in digest order of six", digests, got.Total)
	}
}
