// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import "testing"

func TestRemainingAlgorithmFactsSurviveNormalization(t *testing.T) {
	for family, detector := range map[AlgorithmFamily]AlgorithmDetectorKind{
		AlgorithmFamilySimpleYearRound:    AlgorithmDetectorKindSimpleYearRound,
		AlgorithmFamilyAdvancedRingRatio:  AlgorithmDetectorKindAdvancedRingRatio,
		AlgorithmFamilyAdvancedYearRound:  AlgorithmDetectorKindAdvancedYearRound,
		AlgorithmFamilyRingRatioAmplitude: AlgorithmDetectorKindRingRatioAmplitude,
		AlgorithmFamilyYearRoundAmplitude: AlgorithmDetectorKindYearRoundAmplitude,
		AlgorithmFamilyYearRoundRange:     AlgorithmDetectorKindYearRoundRange,
	} {
		t.Run(string(family), func(t *testing.T) {
			observation := Observation{Component: ComponentEvaluation, Stage: StageEvaluationCompleted, Result: ResultSuccess,
				AlgorithmEvaluations: []AlgorithmEvaluationFact{{SourceAlgorithmFamily: family, DetectorKind: detector, Result: AlgorithmEvaluationResultNormal}},
				AlgorithmInputs: []AlgorithmInputFact{{SourceAlgorithmFamily: family, DetectorKind: detector, InputName: AlgorithmInputNameHistory,
					DependencyPoint: AlgorithmDependencyPointHistorical, Result: AlgorithmInputResultAvailable}},
			}
			got := NormalizeObservation(observation)
			if len(got.AlgorithmEvaluations) != 1 || len(got.AlgorithmInputs) != 1 {
				t.Fatalf("supported algorithm observation dropped: %+v", got)
			}
			observation.AlgorithmInputs[0].DependencyPoint = "history_604800"
			observation.AlgorithmEvaluations[0].DetectorKind = AlgorithmDetectorKindThreshold
			got = NormalizeObservation(observation)
			if len(got.AlgorithmEvaluations) != 0 || len(got.AlgorithmInputs) != 0 {
				t.Fatalf("unbounded offset or wrong detector accepted: %+v", got)
			}
		})
	}
}
