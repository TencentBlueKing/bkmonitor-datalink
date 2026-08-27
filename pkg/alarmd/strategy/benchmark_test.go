// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"encoding/json"
	"testing"
)

func BenchmarkCompiledPlanReadOnlyViews(b *testing.B) {
	compiled := mustCompilePlan(b, newTestCompiler(b), validPlan())
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		readOnlyLevelsSink = compiled.Levels()
		readOnlyDetectorsSink = readOnlyLevelsSink[0].Detectors()
		readOnlyPredicateSink = readOnlyDetectorsSink[0].Predicate()
	}
}

func BenchmarkPredicateEvaluate(b *testing.B) {
	predicate, normalizer := compileThresholdForTest(b, thresholdConfig("50"))
	value := normalizer.Normalize(json.RawMessage(`60`)).Value()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := predicate.Evaluate(value); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNumericNormalizerNormalize(b *testing.B) {
	_, normalizer := compileThresholdForTest(b, thresholdConfig("50"))
	raw := json.RawMessage(`60.25`)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if result := normalizer.Normalize(raw); !result.Available() {
			b.Fatalf("Normalize() reason = %q", result.ReasonCode())
		}
	}
}
