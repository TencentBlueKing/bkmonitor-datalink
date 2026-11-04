// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/newopens"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// NewOpensReporter is an open-alert port that also counts each strategy's
// opens in the hour (newopens.Counter).
type NewOpensReporter interface {
	NewOpens(key newopens.Key) (newopens.Report, bool)
}

// observeNewOpens writes the Plan's strategy's hours past the report floor
// after a batch of its events was acknowledged, while one is and once more
// when none is: what the fleet adds up across replicas for its
// NEW_ALERTS_BURST line. Nothing when the port keeps no such count.
func (coordinator *SlotExecutionCoordinator) observeNewOpens(ctx context.Context, operation execution.Operation, plan execution.PlanIdentity) {
	reporter, ok := coordinator.ports.OpenAlerts.(NewOpensReporter)
	if !ok {
		return
	}
	report, say := reporter.NewOpens(newopens.Key{TenantID: plan.TenantID, StrategyID: plan.StrategyID})
	if !say {
		return
	}
	var fact observability.NewOpensFact
	opened := 0
	for _, hour := range report.Hours {
		fact.Hours = append(fact.Hours, observability.NewOpensHourFact{StartUnix: hour.Start.Unix(), Opened: hour.Opened,
			Dimension: hour.Dimension, Distinct: hour.Distinct, DistinctCapped: hour.DistinctCapped,
			DimensionsDropped: hour.DimensionsDropped})
		opened = max(opened, hour.Opened)
	}
	trace := observability.TraceFields{StrategyID: plan.StrategyID, BusinessID: plan.BusinessID}
	coordinator.emitObservation(observability.ContextWithTraceFields(ctx, trace), observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageNewOpens, Result: observability.ResultSuccess,
		ReasonCode: observability.ReasonNone, Operation: observability.Operation(operation),
		Counts: observability.Counts{Events: int64(opened)}, NewOpens: &fact, Trace: trace,
	})
}
