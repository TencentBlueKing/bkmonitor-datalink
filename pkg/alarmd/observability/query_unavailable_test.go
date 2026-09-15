// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import "testing"

// One entry per unavailable physical query survives normalization, an
// attribution outside the set lands in other rather than vanishing, and the
// facts exist only on the query_completed observation of the access component
// -- anywhere else they would be a label with no writer.
func TestNormalizeQueryUnavailableKeepsEveryEntryAndBoundsTheSet(t *testing.T) {
	input := []QueryUnavailableFacts{
		{Attribution: QueryUnavailableNoAttempts},
		{Attribution: QueryUnavailableNoAttempts},
		{Attribution: QueryUnavailableFromAttempt},
		{Attribution: "something_this_build_never_declared"},
	}
	got := NormalizeObservation(Observation{
		Component: ComponentAccess, Stage: StageQueryCompleted, Operation: OperationNormal, Result: ResultFailed,
		QueryUnavailable: input,
	}).QueryUnavailable
	want := []string{QueryUnavailableNoAttempts, QueryUnavailableNoAttempts, QueryUnavailableFromAttempt, QueryUnavailableOther}
	if len(got) != len(want) {
		t.Fatalf("normalized %d entries, want %d: %+v", len(got), len(want), got)
	}
	for index, facts := range got {
		if facts.Attribution != want[index] {
			t.Fatalf("entry %d = %q, want %q", index, facts.Attribution, want[index])
		}
	}
	elsewhere := NormalizeObservation(Observation{
		Component: ComponentScheduler, Stage: StageSlotCompleted, Operation: OperationNormal, Result: ResultFailed,
		QueryUnavailable: input,
	}).QueryUnavailable
	if elsewhere != nil {
		t.Fatalf("facts survived on a stage that does not produce them: %+v", elsewhere)
	}
	if empty := NormalizeObservation(Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Operation: OperationNormal, Result: ResultSuccess}).QueryUnavailable; empty != nil {
		t.Fatalf("an observation without unavailable queries grew facts: %+v", empty)
	}
}
