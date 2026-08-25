// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

var detectionBatchSink DetectionBatch

func BenchmarkEvaluatorThreshold(b *testing.B) {
	cases := []struct {
		name        string
		planCount   int
		recordCount int
		hostCount   int
		reverse     bool
		sparse      bool
		exactBudget bool
	}{
		{name: "single_plan", planCount: 1, recordCount: 100, hostCount: 10},
		{name: "multi_plan_overlap", planCount: 4, recordCount: 100, hostCount: 25},
		{name: "multi_plan_sparse", planCount: 4, recordCount: 100, hostCount: 25, sparse: true},
		{name: "hot_series_ordered", planCount: 1, recordCount: 100, hostCount: 1},
		{name: "hot_series_reversed", planCount: 1, recordCount: 100, hostCount: 1, reverse: true},
		{name: "exact_result_budget", planCount: 1, recordCount: 100, hostCount: 1, exactBudget: true},
	}
	for _, benchmark := range cases {
		b.Run(benchmark.name, func(b *testing.B) {
			request, selectedRecords := benchmarkRequest(b, benchmark.planCount, benchmark.recordCount, benchmark.hostCount,
				benchmark.reverse, benchmark.sparse, benchmark.exactBudget)
			evaluator := newTestEvaluator(b)
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				batch, err := evaluator.Evaluate(context.Background(), request)
				if err != nil {
					b.Fatal(err)
				}
				detectionBatchSink = batch
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*selectedRecords), "ns/selected_record")
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*selectedRecords*2), "ns/level_fact")
		})
	}
}

func benchmarkRequest(
	b testing.TB,
	planCount int,
	recordCount int,
	hostCount int,
	reverse bool,
	sparse bool,
	exactBudget bool,
) (EvaluateRequest, int) {
	b.Helper()
	plans := make([]contract.EvaluationPlanV2, planCount)
	for index := range plans {
		plans[index] = fixturePlan(strconv.Itoa(1001+index), []contract.LevelIRV2{
			fixtureLevel(1, 20, contract.LevelConnectorAND, fixtureThresholdAlgorithm("GTE", "50", "percent", "")),
			fixtureLevel(5, 1, contract.LevelConnectorOR,
				fixtureThresholdAlgorithm("GT", "100", "percent", ""),
				fixtureThresholdAlgorithm("LT", "70", "percent", "")),
		})
	}
	records := make([]fixtureRecord, recordCount)
	for index := range records {
		sourceIndex := index
		if reverse {
			sourceIndex = recordCount - index
		}
		records[index] = fixtureRecord{
			host: "host-" + strconv.Itoa(index%hostCount), sourceTime: int64(1_000 + sourceIndex*60), value: json.RawMessage(`60.1234565`),
		}
	}
	envelope := fixtureEnvelope(b, plans, records, contract.QueryCompletenessFull)
	selectedRecords := planCount * recordCount
	if sparse {
		selectedRecords = recordCount
		chunk := recordCount / planCount
		selectors := make([]contract.PlanSelectorV2, planCount)
		for index := range selectors {
			start := uint32(index * chunk)
			end := uint32((index + 1) * chunk)
			if index == planCount-1 {
				end = uint32(recordCount)
			}
			ranges := []contract.SelectorRangeV2{{Start: start, End: end}}
			selectors[index] = contract.PlanSelectorV2{
				PlanOrdinal: uint32(index), Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &ranges},
			}
		}
		envelope.Selectors = selectors
	}
	input, executions, digest := fixtureExecutions(b, envelope)
	limits := generousLimits()
	if exactBudget {
		resultBytes, ok := estimatePlanResultBytes(uint64(hostCount), uint64(recordCount), 2, 1)
		if !ok {
			b.Fatal("exact result budget overflowed")
		}
		limits.MaxResultBytes = resultBytes
	}
	return EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: limits,
	}, selectedRecords
}
