// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestQueryExecutionCompletionRequiresEveryFrozenPhysicalQuery(t *testing.T) {
	header := execution.InternalExecutionHeader{
		Contract: frozenContract(), DuePlans: validInternalExecution().DuePlans,
		Requirements:            validInternalExecution().Requirements,
		RequiredPhysicalQueries: []execution.PlannedPhysicalQueryRef{{Digest: "query-a", QueryRevision: "revision-a"}},
	}
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true}
	if err := completion.Validate(header, nil); err == nil {
		t.Fatal("completion missing a frozen physical query must fail")
	}

	delivery := execution.SeriesDelivery{PhysicalQuery: "query-a", QueryRevision: "revision-a", Series: 1, Records: 2, Digest: strings.Repeat("d", 64)}
	completion.PhysicalQueries = []execution.PhysicalQueryCompletion{{
		Ref: execution.ProviderResultRef("completion-a"), PhysicalQuery: "query-a", QueryRevision: "revision-a",
		Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: delivery,
	}}
	if err := completion.Validate(header, []execution.SeriesDelivery{delivery}); err != nil {
		t.Fatalf("valid completion: %v", err)
	}

	completion.PhysicalQueries[0].Delivery.Records++
	if err := completion.Validate(header, []execution.SeriesDelivery{delivery}); err == nil {
		t.Fatal("completion delivery counters must match delivered SeriesBatch facts")
	}
}

func TestSeriesDeliveryDigestAccumulatesEveryBatch(t *testing.T) {
	first := execution.SeriesDelivery{
		PhysicalQuery: "query-a", QueryRevision: "revision-a", Series: 1, Records: 2, Bytes: 20,
		Digest: strings.Repeat("a", 64),
	}
	last := execution.SeriesDelivery{
		PhysicalQuery: "query-a", QueryRevision: "revision-a", Series: 1, Records: 3, Bytes: 30,
		Digest: strings.Repeat("b", 64),
	}
	aggregate, err := execution.AccumulateSeriesDelivery(execution.SeriesDelivery{}, first)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err = execution.AccumulateSeriesDelivery(aggregate, last)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Series != 2 || aggregate.Records != 5 || aggregate.Bytes != 50 || aggregate.Digest == last.Digest {
		t.Fatalf("delivery aggregate=%+v", aggregate)
	}

	header := execution.InternalExecutionHeader{
		Contract: frozenContract(), DuePlans: validInternalExecution().DuePlans,
		Requirements:            validInternalExecution().Requirements,
		RequiredPhysicalQueries: []execution.PlannedPhysicalQueryRef{{Digest: "query-a", QueryRevision: "revision-a"}},
	}
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true, PhysicalQueries: []execution.PhysicalQueryCompletion{{
		Ref: "completion-a", PhysicalQuery: "query-a", QueryRevision: "revision-a",
		Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: aggregate,
	}}}
	if err := completion.Validate(header, []execution.SeriesDelivery{aggregate}); err != nil {
		t.Fatal(err)
	}
	completion.PhysicalQueries[0].Delivery.Digest = last.Digest
	if err := completion.Validate(header, []execution.SeriesDelivery{aggregate}); err == nil {
		t.Fatal("last batch digest must not prove a multi-batch delivery")
	}
}
