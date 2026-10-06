// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import "testing"

// The three situations behind one code. The code is what the callers picked
// before this existed -- the last attempt that named a reason, else the
// fallback -- so the only new fact is the second result, and each situation
// must land on its own value or the counter built on it says nothing.
func TestAttributeUnavailableTellsTheThreeSituationsApart(t *testing.T) {
	fallback := ReasonCode("QUERY_UNAVAILABLE")
	cases := []struct {
		name      string
		attempts  []RouteAttemptFact
		wantCode  ReasonCode
		wantWhere UnavailableAttribution
	}{
		{name: "no attempt was made", wantCode: fallback, wantWhere: UnavailableNoAttempts},
		{name: "attempts made, none classified",
			attempts: []RouteAttemptFact{{AttemptNo: 1, Result: RouteAttemptFailed}, {AttemptNo: 2, Result: RouteAttemptFailed}},
			wantCode: fallback, wantWhere: UnavailableNoAttemptReason},
		{name: "the last classified attempt names the code",
			attempts: []RouteAttemptFact{
				{AttemptNo: 1, Result: RouteAttemptFailed, ReasonCode: "QUERY_UNAVAILABLE"},
				{AttemptNo: 2, Result: RouteAttemptFailed, ReasonCode: "QUERY_TIMEOUT"},
				{AttemptNo: 3, Result: RouteAttemptFailed},
			},
			wantCode: "QUERY_TIMEOUT", wantWhere: UnavailableFromAttempt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, where := AttributeUnavailable(ProviderRouteFacts{Attempts: tc.attempts}, fallback)
			if code != tc.wantCode || where != tc.wantWhere {
				t.Fatalf("got (%s, %s), want (%s, %s)", code, where, tc.wantCode, tc.wantWhere)
			}
		})
	}
}

// The closed set is what a reader pre-creates series from; a value added to
// the type without being listed would be a series that never appears.
func TestUnavailableAttributionsListEveryValueTheFunctionCanReturn(t *testing.T) {
	seen := map[UnavailableAttribution]bool{}
	for _, attempts := range [][]RouteAttemptFact{
		nil,
		{{Result: RouteAttemptFailed}},
		{{Result: RouteAttemptFailed, ReasonCode: "X"}},
	} {
		_, where := AttributeUnavailable(ProviderRouteFacts{Attempts: attempts}, "F")
		seen[where] = true
	}
	if len(seen) != len(UnavailableAttributions) {
		t.Fatalf("the function returns %d distinct attributions, the list has %d", len(seen), len(UnavailableAttributions))
	}
	for _, listed := range UnavailableAttributions {
		if !seen[listed] {
			t.Fatalf("%s is listed but no input produces it", listed)
		}
	}
}
