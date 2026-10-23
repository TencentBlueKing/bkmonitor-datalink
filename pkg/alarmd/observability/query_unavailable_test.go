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
		{Attribution: QueryUnavailableNoAttempts, HTTPClass: QueryHTTPClassNone},
		{Attribution: QueryUnavailableNoAttempts, HTTPClass: QueryHTTPClassNone},
		{Attribution: QueryUnavailableFromAttempt, HTTPClass: QueryHTTPClass4xx},
		{Attribution: "something_this_build_never_declared", HTTPClass: "3xx"},
	}
	got := NormalizeObservation(Observation{
		Component: ComponentAccess, Stage: StageQueryCompleted, Operation: OperationNormal, Result: ResultFailed,
		QueryUnavailable: input,
	}).QueryUnavailable
	want := []string{QueryUnavailableNoAttempts, QueryUnavailableNoAttempts, QueryUnavailableFromAttempt, QueryUnavailableOther}
	if len(got) != len(want) {
		t.Fatalf("normalized %d entries, want %d: %+v", len(got), len(want), got)
	}
	wantClass := []string{QueryHTTPClassNone, QueryHTTPClassNone, QueryHTTPClass4xx, QueryHTTPClassOther}
	for index, facts := range got {
		if facts.Attribution != want[index] || facts.HTTPClass != wantClass[index] {
			t.Fatalf("entry %d = %q/%q, want %q/%q", index, facts.Attribution, facts.HTTPClass, want[index], wantClass[index])
		}
	}
	for status, class := range map[int]string{0: QueryHTTPClassNone, 400: QueryHTTPClass4xx, 499: QueryHTTPClass4xx, 500: QueryHTTPClass5xx, 599: QueryHTTPClass5xx, 302: QueryHTTPClassOther, 600: QueryHTTPClassOther} {
		if got := QueryHTTPClassOf(status); got != class {
			t.Fatalf("QueryHTTPClassOf(%d) = %q, want %q", status, got, class)
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
