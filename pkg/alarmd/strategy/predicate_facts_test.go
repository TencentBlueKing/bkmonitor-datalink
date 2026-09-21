// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import "testing"

func TestPredicateFactsDetachedAndNormalizerMultiplier(t *testing.T) {
	predicate, normalizer := compileThresholdForTest(t, thresholdConfig("80"))
	before := predicate.Digest()
	facts := predicate.Facts()
	if facts.Kind != PredicateAny || len(facts.Children) != 1 || len(facts.Children[0].Children) != 1 {
		t.Fatalf("unexpected compiled tree %+v", facts)
	}
	leaf := facts.Children[0].Children[0]
	if leaf.Kind != PredicateCompare || leaf.NormalizedThreshold == "" || normalizer.SourceMultiplier() != 1 {
		t.Fatalf("facts %+v normalizer %+v", leaf, normalizer)
	}
	facts.Children[0].Children[0].NormalizedThreshold = "0"
	facts.Children[0].Children = nil
	if predicate.Digest() != before || predicate.Facts().Children[0].Children[0].NormalizedThreshold != leaf.NormalizedThreshold {
		t.Fatal("caller mutated frozen predicate")
	}
}
