// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"reflect"
	"testing"
)

// Gate facts are a closed vocabulary that becomes a metric label: a cause
// outside it is dropped, a zero count is dropped, and the facts belong to a
// completed evaluation and nowhere else.
func TestRecoveryGateFactsAreNormalizedToTheClosedVocabulary(t *testing.T) {
	facts := []RecoveryGateFact{
		{Cause: RecoveryGateLevelUnavailable, Records: 2},
		{Cause: RecoveryGateLevelRecovering, Records: 0},
		{Cause: RecoveryGateLevelWithoutRecovery, Records: 3},
		{Cause: RecoveryGateCause("qg-secret"), Records: 9},
	}
	normalized := NormalizeObservation(Observation{
		Component: ComponentEvaluation, Stage: StageEvaluationCompleted, RecoveryGates: facts,
	})
	want := []RecoveryGateFact{
		{Cause: RecoveryGateLevelUnavailable, Records: 2},
		{Cause: RecoveryGateLevelWithoutRecovery, Records: 3},
	}
	if !reflect.DeepEqual(normalized.RecoveryGates, want) {
		t.Fatalf("normalized gate facts = %+v, want %+v", normalized.RecoveryGates, want)
	}

	elsewhere := NormalizeObservation(Observation{
		Component: ComponentScheduler, Stage: StageQueryCooldown, RecoveryGates: facts,
	})
	if elsewhere.RecoveryGates != nil {
		t.Fatalf("gate facts survived on a %s/%s observation: %+v", elsewhere.Component, elsewhere.Stage, elsewhere.RecoveryGates)
	}
}
