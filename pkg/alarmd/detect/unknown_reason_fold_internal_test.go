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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The Level's UNKNOWN reason is the fold of its unavailable inputs, not the
// first one in algorithm order.
//
// This is the third of the three derivations the fold exists to make agree,
// and it is the one nothing else would catch: the result contract requires a
// Level's UNKNOWN outcome to carry its gap marker's reason
// (finalGapGuardsOutcome), the marker is folded over the same inputs, and a
// Level that took the first algorithm's reason instead would disagree with it
// whenever two of its inputs failed differently -- refusing the Plan's whole
// round for as long as that lasted.
func TestTheLevelsUnknownReasonIsTheFoldNotTheFirstAlgorithm(t *testing.T) {
	for _, test := range []struct {
		name      string
		current   string
		candidate string
		want      bool
	}{
		{name: "nothing yet takes the first", current: "", candidate: contract.ReasonQueryTimeout, want: true},
		{name: "a stronger reason replaces a weaker one",
			current: contract.ReasonQueryTimeout, candidate: contract.ReasonQueryUnavailable, want: true},
		{name: "a weaker one does not replace a stronger one",
			current: contract.ReasonQueryUnavailable, candidate: contract.ReasonQueryTimeout, want: false},
		{name: "the same reason twice changes nothing",
			current: contract.ReasonQueryTimeout, candidate: contract.ReasonQueryTimeout, want: false},
		{name: "a reason nobody ranked replaces a ranked one",
			current: contract.ReasonQueryUnavailable, candidate: "SOMETHING_NOBODY_RANKED", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := strongerUnknownReason(test.current, test.candidate); got != test.want {
				t.Fatalf("strongerUnknownReason(%q, %q) = %t, want %t", test.current, test.candidate, got, test.want)
			}
		})
	}

	// And the whole point: walking the same two inputs in either order leaves
	// the same reason, which is what lets the Level agree with a marker folded
	// from the same set.
	forward, backward := "", ""
	for _, reason := range []string{contract.ReasonQueryTimeout, contract.ReasonQueryUnavailable} {
		if strongerUnknownReason(forward, reason) {
			forward = reason
		}
	}
	for _, reason := range []string{contract.ReasonQueryUnavailable, contract.ReasonQueryTimeout} {
		if strongerUnknownReason(backward, reason) {
			backward = reason
		}
	}
	if forward != backward || forward != contract.ReasonQueryUnavailable {
		t.Fatalf("algorithm order decided the Level's reason: %q one way, %q the other", forward, backward)
	}
}
