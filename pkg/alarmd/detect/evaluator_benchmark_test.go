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
	records := make([]fixtureRecord, 100)
	for index := range records {
		records[index] = fixtureRecord{
			host: "host-" + strconv.Itoa(index%10), sourceTime: int64(1_000 + index*60), value: json.RawMessage(`60.1234565`),
		}
	}
	envelope := fixtureEnvelope(b, []contract.EvaluationPlanV2{fixturePlan("1001", []contract.LevelIRV2{
		fixtureLevel(1, 20, contract.LevelConnectorAND, fixtureThresholdAlgorithm("GTE", "50", "percent", "")),
		fixtureLevel(5, 1, contract.LevelConnectorOR,
			fixtureThresholdAlgorithm("GT", "100", "percent", ""),
			fixtureThresholdAlgorithm("LT", "70", "percent", "")),
	})}, records, contract.QueryCompletenessFull)
	input, executions, digest := fixtureExecutions(b, envelope)
	request := EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	}
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
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(records)), "ns/selected_record")
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(records)*2), "ns/level_fact")
}
