// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// sharedReadinessBoundary reports the one moment this execution's queries agree
// their data is readable.
//
// It is deliberately separate from sharedPendingReadiness, which decides
// whether to defer and therefore only answers about boundaries still in the
// future. This one answers about the boundary itself, past or future, and
// changes no decision.
func sharedReadinessBoundary(queries []PlannedQuery) (time.Time, string) {
	var shared time.Time
	found := false
	for _, query := range queries {
		if len(query.Requirements) == 0 {
			continue
		}
		readyAt := time.UnixMilli(query.ReadyAtUnixMilli)
		if !found {
			shared, found = readyAt, true
			continue
		}
		if !shared.Equal(readyAt) {
			return time.Time{}, observability.ReadinessBoundaryMixed
		}
	}
	if !found {
		return time.Time{}, observability.ReadinessBoundaryNone
	}
	return shared, observability.ReadinessBoundaryUnified
}

// observeSlotReadiness records how long the data had been ready when this
// execution arrived.
//
// Called once an execution is past the point that turns early arrivals away, so
// every recorded arrival is one that went on to do the work, and the time it
// carries is the moment the execution entered Access rather than the moment it
// finished waiting for anything. Both matter: a value taken after a wait would
// measure the wait's own length, and a value taken on an arrival that was then
// deferred would be counting the same Slot twice for one round of work.
//
// Recovery and replay are excluded. They run deliberately late against a
// different clock, and folding them in would move this distribution for a
// reason that has nothing to do with how well normal scheduling is timed.
func (source *Source) observeSlotReadiness(
	ctx context.Context,
	request execution.QueryExecutionRequest,
	prepared PreparedExecution,
	arrivedAt time.Time,
) {
	// Observability is a fail-open side channel, as at the Coordinator boundary.
	defer func() { _ = recover() }()
	if source.config.Observer == nil || request.Operation != execution.OperationNormal {
		return
	}
	boundary, kind := sharedReadinessBoundary(prepared.Queries)
	facts := observability.SlotReadinessFacts{Boundary: kind}
	if kind == observability.ReadinessBoundaryUnified && !arrivedAt.IsZero() && !boundary.IsZero() {
		facts.SlackSeconds = arrivedAt.Sub(boundary).Seconds()
		facts.Slack = true
	}
	source.config.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageSlotReadinessArrival,
		Result: observability.ResultSuccess, Operation: observability.Operation(request.Operation),
		Direction: observability.DirectionInternal, SlotReadiness: &facts,
		Trace: observability.TraceFields{
			QueryGroupKey:    string(request.Contract.Slot.QueryGroup),
			EvaluationTime:   int64(request.Contract.Slot.EvaluationTime),
			ScheduleRevision: string(request.Contract.ScheduleRevision),
			SnapshotRevision: string(request.Contract.SnapshotRevision),
			QueryRevision:    string(request.Contract.QueryRevision),
			DuePlanSetDigest: string(request.Contract.DuePlanSetDigest),
		},
	})
}
