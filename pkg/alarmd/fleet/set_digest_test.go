// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"fmt"
	"testing"
)

func identities(prefix string, n int) []string {
	list := make([]string, 0, n)
	for i := 0; i < n; i++ {
		list = append(list, fmt.Sprintf("%s-%d", prefix, i))
	}
	return list
}

// The replicas' digests, added, equal the expected objects' digest exactly
// when compareCoverage finds nothing: none held by several, none held
// without being expected, none expected without being held. Each way of
// disagreeing moves the sum or the count.
func TestAddedDigestsAgreeExactlyWhenCoverageDoes(t *testing.T) {
	a, b, c := identities("a", 300), identities("b", 250), identities("c", 5)
	expected := append(append(append([]string{}, a...), b...), c...)
	for name, tc := range map[string]struct {
		sets [][]string
	}{
		"each held once":               {[][]string{a, append(append([]string{}, b...), c...)}},
		"one held by two replicas":     {[][]string{append(append([]string{}, a...), c[0]), append(append([]string{}, b...), c...)}},
		"one expected and not held":    {[][]string{a, append(append([]string{}, b...), c[1:]...)}},
		"one held and not expected":    {[][]string{append(append([]string{}, a...), "stray"), append(append([]string{}, b...), c...)}},
		"one swapped for another":      {[][]string{append(append([]string{}, a...), "stray"), append(append([]string{}, b...), c[1:]...)}},
		"listed twice by its replica":  {[][]string{append(append([]string{}, a...), a[0]), append(append([]string{}, b...), c...)}},
		"everything on three replicas": {[][]string{a, b, c}},
	} {
		sum := SetDigest{}
		for _, set := range tc.sets {
			sum = sum.Add(DigestOf(set))
		}
		disagreement := compareCoverage(tc.sets, Expectation{Known: true, IDs: expected, QueryGroups: len(expected)}, true)
		agrees := disagreement == nil || (disagreement.HeldBySeveralTotal == 0 && disagreement.HeldNotExpectedTotal == 0 &&
			disagreement.ExpectedNotHeldTotal == 0)
		if (sum == DigestOf(expected)) != agrees {
			t.Errorf("%s: digests equal %v, coverage agrees %v (%+v)", name, sum == DigestOf(expected), agrees, disagreement)
		}
	}
}
