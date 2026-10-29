// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// withNamedOutputs has the fixture's query answer as it does, its first
// physical query carrying a named-output table of records records.
func withNamedOutputs(f fixture, records int) *execution.NamedOutputRecords {
	table := &execution.NamedOutputRecords{Refs: []string{"A", "C"}, Records: map[string][]execution.NamedOutputValue{}}
	for record := 0; record < records; record++ {
		table.Records[fmt.Sprintf("%064x", record)] = []execution.NamedOutputValue{
			{Value: json.RawMessage("1"), State: execution.NamedOutputSuccess}, {Value: json.RawMessage("11"), State: execution.NamedOutputSuccess}}
	}
	f.ports.executeOverride = func(ctx context.Context, request execution.QueryExecutionRequest, consumer execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
		override := f.ports.executeOverride
		f.ports.executeOverride = nil
		defer func() { f.ports.executeOverride = override }()
		completion, err := f.ports.Execute(ctx, request, consumer)
		if err == nil && len(completion.PhysicalQueries) > 0 {
			completion.PhysicalQueries[0].NamedOutputs = table
		}
		return completion, err
	}
	return table
}

// A Slot whose query aligned named outputs is charged for them, beside its
// series, when its completion arrives; and one whose table takes it past
// the share one Query Group's Slot may hold is refused for the share, by
// its own total, the way the scheduler stops re-reading.
func TestASlotIsChargedForItsNamedOutputsAndRefusedPastItsShare(t *testing.T) {
	budget := worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10}
	plain, err := newFixtureWithBudget(t, budget).coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !plain.Completed {
		t.Fatalf("Execute() = %+v, %v", plain, err)
	}
	f := newFixtureWithBudget(t, budget)
	table := withNamedOutputs(f, 10)
	named, err := f.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !named.Completed || named.Usage.RetainedBytes != plain.Usage.RetainedBytes+table.RetainedBytes() {
		t.Fatalf("retained %d, want %d and the table's %d: %+v, %v", named.Usage.RetainedBytes, plain.Usage.RetainedBytes,
			table.RetainedBytes(), named, err)
	}
	f = newFixtureWithBudget(t, budget)
	table = withNamedOutputs(f, 1<<20/4*3/200)
	_, err = f.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	refusal, deterministic := execution.ShareRefusalOf(err)
	if err == nil || !deterministic || refusal.RequestedBytes != table.RetainedBytes() || refusal.LimitBytes != 1<<20/4*3 {
		t.Fatalf("refusal %+v deterministic %v error %v", refusal, deterministic, err)
	}
	if reserved := worker.RetainedReserved(f.coordinator); reserved != 0 {
		t.Fatalf("%d bytes still reserved after a refused Slot", reserved)
	}
}

// The table a Slot's primary query aligned reaches the evaluation of each of
// its series, for the snapshots their events carry; a Slot whose queries
// aligned none evaluates with none.
func TestTheNamedOutputTableReachesTheSeriesEvaluation(t *testing.T) {
	budget := worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10}
	plain := newFixtureWithBudget(t, budget)
	if _, err := plain.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal)); err != nil {
		t.Fatal(err)
	}
	if plain.ports.lastEvaluation.NamedOutputs != nil {
		t.Fatal("a Slot without named outputs evaluated with a table")
	}
	f := newFixtureWithBudget(t, budget)
	table := withNamedOutputs(f, 2)
	if _, err := f.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal)); err != nil {
		t.Fatal(err)
	}
	if f.ports.lastEvaluation.NamedOutputs != table {
		t.Fatalf("the series was evaluated with %p, want the primary query's table %p", f.ports.lastEvaluation.NamedOutputs, table)
	}
}
