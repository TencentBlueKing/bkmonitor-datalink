// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The projection and the counter that consumes it are each covered on their own
// side, and neither covers the line that joins them. That gap has a specific
// shape: delete the assignment in observeQueryCompleted and both suites stay
// green while the counter reads zero forever -- which is not "no signal", it is
// the signal saying the condition never happens. The observe call also swallows
// panics, so nothing would complain at runtime either.
//
// This is the same failure as a page section whose renderer was written and
// never called, which shipped once already.
func TestQueryCompletedObservationCarriesTheProjectedStatuses(t *testing.T) {
	var captured []observability.Observation
	coordinator := &SlotExecutionCoordinator{}
	coordinator.ports.Observer = observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			captured = append(captured, observation)
		})

	completion := execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{
		{RouteFacts: execution.ProviderRouteFacts{
			Status: &execution.ProviderStatusFact{Code: "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS", Allowed: true}}},
		{RouteFacts: execution.ProviderRouteFacts{
			Status: &execution.ProviderStatusFact{Code: "STORAGE_TIMEOUT"}}},
	}}
	coordinator.observeQueryCompleted(context.Background(), execution.OperationNormal,
		time.Now(), observability.ResultSuccess, "", completion, execution.EvaluationResult{})

	if len(captured) != 1 {
		t.Fatalf("observations = %d, want the one query_completed boundary", len(captured))
	}
	statuses := captured[0].QueryStatus
	if len(statuses) != 2 {
		t.Fatalf("QueryStatus = %+v, want both physical queries to reach the observation", statuses)
	}
	if statuses[0].Outcome != observability.QueryStatusOutcomeAllowed ||
		statuses[1].Outcome != observability.QueryStatusOutcomeUnavailable {
		t.Fatalf("outcomes did not survive the boundary: %+v", statuses)
	}
}

// A completion where nothing carried a code must not put an entry on the
// observation. An always-present entry would make the counter tick on every
// ordinary query, and a number that always moves cannot show that something
// started happening.
func TestQueryCompletedObservationCarriesNoStatusWhenNoneWasReported(t *testing.T) {
	var captured []observability.Observation
	coordinator := &SlotExecutionCoordinator{}
	coordinator.ports.Observer = observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			captured = append(captured, observation)
		})

	coordinator.observeQueryCompleted(context.Background(), execution.OperationNormal,
		time.Now(), observability.ResultSuccess, "",
		execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{{}, {}}},
		execution.EvaluationResult{})

	if len(captured) != 1 {
		t.Fatalf("observations = %d, want one", len(captured))
	}
	if statuses := captured[0].QueryStatus; len(statuses) != 0 {
		t.Fatalf("QueryStatus = %+v, want none for a completion that reported no code", statuses)
	}
}
