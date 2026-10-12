// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func contentKeySegment() execution.ScheduleSegmentFact {
	return execution.ScheduleSegmentFact{
		QueryGroup: "group", ObjectDigest: execution.ObjectDigest(strings.Repeat("a", 64)),
		OutputContextRefs: []execution.OutputContextRef{
			{Plan: execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "7"}, Digest: execution.OutputContextDigest(strings.Repeat("c", 64))},
			{Plan: execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "8"}, Digest: execution.OutputContextDigest(strings.Repeat("d", 64))},
		},
	}
}

// The content key of a Plan assembled by content names both of its sources
// -- the group's object and the Plan's own output context -- with the Plan's
// identity and piece, so a change of any one of them is another key. Two
// Plans of one group rendering by different contexts have different keys,
// and changing one Plan's context changes that Plan's key alone.
func TestACompileContentKeyNamesTheObjectThePlansOwnContextAndThePlan(t *testing.T) {
	segment := contentKeySegment()
	seven := FrozenPlan{Identity: execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "7"}}
	eight := FrozenPlan{Identity: execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "8"}}
	base := compileContentKey(true, segment, seven)
	if base == "" {
		t.Fatal("a Plan assembled by content has no content key")
	}
	if compileContentKey(true, segment, eight) == base {
		t.Fatal("two Plans of one group with different contexts share a content key")
	}

	distinct := map[string]string{"base": base}
	record := func(name, key string) {
		t.Helper()
		for other, seen := range distinct {
			if key == seen {
				t.Fatalf("%s has the content key of %s", name, other)
			}
		}
		distinct[name] = key
	}
	object := contentKeySegment()
	object.ObjectDigest = execution.ObjectDigest(strings.Repeat("b", 64))
	record("another object", compileContentKey(true, object, seven))

	context := contentKeySegment()
	context.OutputContextRefs = append([]execution.OutputContextRef(nil), context.OutputContextRefs...)
	context.OutputContextRefs[0].Digest = execution.OutputContextDigest(strings.Repeat("e", 64))
	record("another context of the Plan", compileContentKey(true, context, seven))
	if compileContentKey(true, context, eight) != compileContentKey(true, segment, eight) {
		t.Fatal("changing one Plan's context changed another Plan's key")
	}

	for name, identity := range map[string]execution.PlanIdentity{
		"another tenant":   {TenantID: "other", BusinessID: "2", StrategyID: "7"},
		"another business": {TenantID: "default", BusinessID: "3", StrategyID: "7"},
	} {
		moved := contentKeySegment()
		moved.OutputContextRefs = append(moved.OutputContextRefs, execution.OutputContextRef{Plan: identity, Digest: execution.OutputContextDigest(strings.Repeat("c", 64))})
		record(name, compileContentKey(true, moved, FrozenPlan{Identity: identity}))
	}
	for name, shard := range map[string]execution.ShardRef{
		"a piece":           {Dimension: "host", Index: 0, Count: 2, MatcherDigest: "m"},
		"another piece":     {Dimension: "host", Index: 1, Count: 2, MatcherDigest: "m"},
		"another split":     {Dimension: "host", Index: 0, Count: 3, MatcherDigest: "m"},
		"another dimension": {Dimension: "ip", Index: 0, Count: 2, MatcherDigest: "m"},
		"another matcher":   {Dimension: "host", Index: 0, Count: 2, MatcherDigest: "n"},
	} {
		piece := shard
		record(name, compileContentKey(true, segment, FrozenPlan{Identity: seven.Identity, Shard: &piece}))
	}

	if key := compileContentKey(false, segment, seven); key != "" {
		t.Fatalf("a group served from the Snapshot has content key %q, want none", key)
	}
	legacy := contentKeySegment()
	legacy.ObjectDigest = ""
	if key := compileContentKey(true, legacy, seven); key != "" {
		t.Fatalf("a Segment naming no object gives content key %q, want none", key)
	}
	unnamed := FrozenPlan{Identity: execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "9"}}
	if key := compileContentKey(true, segment, unnamed); key != "" {
		t.Fatalf("a Plan the Segment names no context for has content key %q, want none", key)
	}
}

// Which of the two objects each field of the Plan the compiler reads comes
// from when a group is assembled by content (AssembleQueryGroup).
//
// This is a prompt, not an invariant. The content key is sound only while
// every field comes from one of the two objects it names; a field added
// from anywhere else would be missed by it, and the compile would be served
// a stale Plan. When this fails because a field was added, say in the commit
// which object it comes from and whether the content key names it, and move
// it into the right list -- or, if it comes from neither, stop keying by
// content until the key names its source too.
func TestEveryCompiledPlanFieldComesFromAnObjectTheContentKeyNames(t *testing.T) {
	fromGroupObject := []string{
		"EffectiveTimeSnapshot", "InputProjection", "NoData", "OutputIdentity", "PlanID", "StrategyIR",
		"TargetPlan", "TargetScope", "TerminalReasonCode",
	}
	fromOutputContext := []string{
		"GlobalBusiness", "LegacyOutput", "SignalType", "SourceCompatibility", "StrategyRef", "SubjectFacts", "WireFormat",
	}
	want := append(append([]string(nil), fromGroupObject...), fromOutputContext...)
	sort.Strings(want)
	planType := reflect.TypeOf(contract.EvaluationPlanV2{})
	var fields []string
	for index := 0; index < planType.NumField(); index++ {
		fields = append(fields, planType.Field(index).Name)
	}
	sort.Strings(fields)
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("EvaluationPlanV2 fields = %v\nlisted by source = %v\nsay where each new field comes from and whether the content key names it", fields, want)
	}
}
