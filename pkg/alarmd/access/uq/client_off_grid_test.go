// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// unalignedAttempt is the query of a Plan detected more often than it
// aggregates: one minute's window starting 35 s off the minute grid, read
// from where the request starts.
func unalignedAttempt(t *testing.T) execution.QueryAttempt {
	t.Helper()
	attempt := validAttempt(t)
	facts := attempt.Spec.PlanFacts
	facts.QueryRevision, facts.NotTimeAlign = "", true
	facts.QueryList = append([]execution.QueryClause(nil), facts.QueryList...)
	facts.QueryList[0].Offset, facts.QueryList[0].OffsetForward = "59999ms", "true"
	rebuilt, err := execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	window := execution.QueryWindow{Start: 1_700_123_015, End: 1_700_123_075}
	spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: rebuilt,
		LogicalWindow: window, ProviderRange: window, AcceptedRange: window, RequiredColumns: []string{"value"}})
	if err != nil {
		t.Fatal(err)
	}
	attempt.Spec = spec
	return attempt
}

func pointsBody(times ...int64) string {
	values := ""
	for index, at := range times {
		if index > 0 {
			values += ","
		}
		values += fmt.Sprintf("[%d,12.5]", at*1000)
	}
	return `{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],` +
		`"group_values":["127.0.0.1"],"values":[` + values + `]}],"is_partial":false}`
}

// The query service answers an unaligned request with the bucket that starts
// where the request starts and the one starting at its end: the first is the
// window, the second the next one and outside it.
func TestAnUnalignedQueryUsesTheBucketThatStartsWhereItsRequestDoes(t *testing.T) {
	attempt := unalignedAttempt(t)
	client := fixtureClient(t, http.StatusOK, pointsBody(1_700_123_015, 1_700_123_075), DefaultLimits())
	sink := &collectingSink{}
	completion, err := client.Execute(context.Background(), attempt, sink)
	if err != nil || completion.Completeness != execution.CompletenessFull || completion.DataState != execution.DataStateData ||
		len(sink.batches) != 1 || sink.batches[0].Dataset.Len() != 1 {
		t.Fatalf("completion %+v (%v) with %d batches, want the window's one bucket used", completion, err, len(sink.batches))
	}
}

// A storage that buckets on its own grid - the minute from the epoch - answers
// the same request with a bucket that starts 25 s into the window: part of
// this window and part of the next, at a time no detection is at. None of it
// is used, and the query is unavailable for a reason that names the step.
func TestAnUnalignedQueryAnsweredOnTheStoragesGridIsNotUsed(t *testing.T) {
	attempt := unalignedAttempt(t)
	client := fixtureClient(t, http.StatusOK, pointsBody(1_700_123_040), DefaultLimits())
	sink := &collectingSink{}
	completion, err := client.Execute(context.Background(), attempt, sink)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Completeness != execution.CompletenessUnavailable || len(sink.batches) != 0 || len(completion.RouteFacts.Attempts) != 1 ||
		completion.RouteFacts.Attempts[0].ReasonCode != execution.ReasonCode(contract.ReasonDetectIntervalStorageNotSliding) ||
		completion.RouteFacts.Attempts[0].Detail != "response=off_request_grid" {
		t.Fatalf("completion %+v with %d batches, want unavailable as %s and nothing consumed", completion, len(sink.batches),
			contract.ReasonDetectIntervalStorageNotSliding)
	}
	// An aligned query is aligned by the query service; the same point is
	// one of its buckets and is used as before.
	aligned := fixtureClient(t, http.StatusOK, pointsBody(1_700_123_040), DefaultLimits())
	alignedSink := &collectingSink{}
	completion, err = aligned.Execute(context.Background(), validAttempt(t), alignedSink)
	if err != nil || completion.Completeness != execution.CompletenessFull || len(alignedSink.batches) != 1 {
		t.Fatalf("aligned completion %+v (%v) with %d batches, want the point used", completion, err, len(alignedSink.batches))
	}
}
