// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func decodeTarget(t *testing.T, document string) [][]legacyTargetCondition {
	t.Helper()
	var item legacyItem
	decoder := json.NewDecoder(strings.NewReader(document))
	decoder.UseNumber()
	if err := decoder.Decode(&item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	return item.Target
}

// The production shape: a set of sets, minus some modules under them.
func TestATopologyTargetCompilesToItsNodeKeys(t *testing.T) {
	target := decodeTarget(t, `{"id":1,"target":[[
		{"field":"host_topo_node","method":"eq","value":[{"bk_obj_id":"set","bk_inst_id":81},{"bk_obj_id":"set","bk_inst_id":87}]},
		{"field":"host_topo_node","method":"neq","value":[{"bk_obj_id":"module","bk_inst_id":7298}]}
	]]}`)
	scope, err := compileTargetScope(target, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if scope == nil || len(scope.Groups) != 1 || len(scope.Groups[0].Conditions) != 2 {
		t.Fatalf("scope = %+v", scope)
	}
	include := scope.Groups[0].Conditions[0]
	if include.Field != contract.TargetScopeTopoNode || include.Method != contract.TargetScopeInclude {
		t.Fatalf("include condition = %+v", include)
	}
	if strings.Join(include.Keys, ",") != "set|81,set|87" {
		t.Fatalf("include keys = %v", include.Keys)
	}
	exclude := scope.Groups[0].Conditions[1]
	if exclude.Method != contract.TargetScopeExclude || strings.Join(exclude.Keys, ",") != "module|7298" {
		t.Fatalf("exclude condition = %+v", exclude)
	}
	if err := scope.Validate(); err != nil {
		t.Fatalf("compiled scope failed validation: %v", err)
	}
}

// A host target names a machine two ways at once, and Python matches on either.
func TestAHostTargetKeepsBothIdentities(t *testing.T) {
	target := decodeTarget(t, `{"id":1,"target":[[
		{"field":"bk_target_ip","method":"eq","value":[
			{"bk_host_id":4210,"bk_target_ip":"192.0.2.10","bk_target_cloud_id":0},
			{"bk_target_ip":"192.0.2.11"}
		]}
	]]}`)
	scope, err := compileTargetScope(target, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got := strings.Join(scope.Groups[0].Conditions[0].Keys, ",")
	if got != "192.0.2.10|0,192.0.2.11|0,4210" {
		t.Fatalf("host keys = %s", got)
	}
}

func TestNoTargetCompilesToNoScope(t *testing.T) {
	for _, document := range []string{`{"id":1}`, `{"id":1,"target":[]}`, `{"id":1,"target":[[]]}`} {
		scope, err := compileTargetScope(decodeTarget(t, document), nil)
		if err != nil || scope != nil {
			t.Fatalf("%s produced scope %+v, err %v", document, scope, err)
		}
	}
}

// A target that reduces to nothing would make Python match no record at all.
// Publishing a Plan without a scope would do the opposite, so the compiler
// refuses and the strategy stays on Python.
func TestAStatedTargetThatReducesToNothingRejectsThePlan(t *testing.T) {
	target := decodeTarget(t, `{"id":1,"target":[[
		{"field":"host_topo_node","method":"eq","value":[{"bk_obj_id":"","bk_inst_id":0}]}
	]]}`)
	scope, err := compileTargetScope(target, nil)
	if err == nil {
		t.Fatalf("an unresolvable target compiled to %+v", scope)
	}
	if !strings.Contains(err.Error(), "TARGET_SCOPE_UNRESOLVABLE") {
		t.Fatalf("error = %v", err)
	}
}

// Targets whose membership is not a property of the strategy must reject
// rather than be dropped: a silently ignored target is exactly the defect
// being fixed. A dynamic group is one: the strategy cache writer expands it
// before writing, and one reaching here is a target the writer did not
// expand, which the reason says.
func TestTargetsThatCannotBeFrozenRejectThePlan(t *testing.T) {
	for _, document := range []string{
		`{"id":1,"target":[[{"field":"dynamic_group","method":"eq","value":[{"dynamic_group_id":"abc"}]}]]}`,
		`{"id":1,"target":[[{"field":"cw_dynamic_group","method":"eq","value":[{"dynamic_group_id":"abc"}]}]]}`,
		`{"id":1,"target":[[{"field":"something_new","method":"eq","value":[{"bk_obj_id":"module","bk_inst_id":3}]}]]}`,
	} {
		if _, err := compileTargetScope(decodeTarget(t, document), nil); err == nil {
			t.Fatalf("%s compiled instead of rejecting", document)
		} else if !strings.Contains(err.Error(), "TARGET_SCOPE_UNSUPPORTED") {
			t.Fatalf("%s produced %v", document, err)
		} else if strings.Contains(document, "dynamic_group") && !strings.Contains(err.Error(), "strategy cache writer") {
			t.Fatalf("%s does not tell the reader who expands it: %v", document, err)
		}
	}
}

// A service topology target names nodes exactly as a host topology target
// does; the difference is which fuller places the record under them. It
// compiles to the same field with the same keys, so a strategy pointed at a
// module's service instances is no longer refused.
func TestAServiceTopologyTargetCompilesLikeAHostOne(t *testing.T) {
	target := decodeTarget(t, `{"id":1,"target":[[
		{"field":"service_topo_node","method":"eq","value":[{"bk_obj_id":"module","bk_inst_id":3},{"bk_obj_id":"set","bk_inst_id":9}]}
	]]}`)
	scope, err := compileTargetScope(target, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	condition := scope.Groups[0].Conditions[0]
	if condition.Field != contract.TargetScopeTopoNode || strings.Join(condition.Keys, ",") != "module|3,set|9" {
		t.Fatalf("condition = %+v", condition)
	}
	if len(condition.IdentityFields) != 0 {
		t.Fatalf("a topology condition carries identity pairs: %+v", condition)
	}
}

// The fork's object-model target: one key per value, "model|instance", with
// the dimension pairs the record is identified by frozen beside the keys.
// An instance id of 0 is a value, not an absence - Python only refuses None.
func TestAnObjectModelTargetCompilesToItsKeysAndIdentityPairs(t *testing.T) {
	target := decodeTarget(t, `{"id":1,"target":[[
		{"field":"cw_object_model_inst","method":"eq","value":[
			{"cw_object_model_id":"switch","cw_object_model_inst_id":12},
			{"cw_object_model_id":"switch","cw_object_model_inst_id":"7"},
			{"cw_object_model_id":"router","cw_object_model_inst_id":0},
			{"cw_object_model_id":"switch","cw_object_model_inst_id":12.0}
		]},
		{"field":"cw_object_model_inst","method":"neq","value":[{"cw_object_model_id":"switch","cw_object_model_inst_id":7}]}
	]]}`)
	scope, err := compileTargetScope(target, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(scope.Groups) != 1 || len(scope.Groups[0].Conditions) != 2 {
		t.Fatalf("scope = %+v", scope)
	}
	include := scope.Groups[0].Conditions[0]
	if include.Field != contract.TargetScopeObjectModelInst || include.Method != contract.TargetScopeInclude {
		t.Fatalf("include = %+v", include)
	}
	if strings.Join(include.Keys, ",") != "router|0,switch|12,switch|7" {
		t.Fatalf("include keys = %v", include.Keys)
	}
	if len(include.IdentityFields) != 1 || include.IdentityFields[0] != [2]string{"cw_object_model_id", "cw_object_model_inst_id"} {
		t.Fatalf("identity pairs = %v, want the platform default alone", include.IdentityFields)
	}
	exclude := scope.Groups[0].Conditions[1]
	if exclude.Method != contract.TargetScopeExclude || strings.Join(exclude.Keys, ",") != "switch|7" {
		t.Fatalf("exclude = %+v", exclude)
	}
	if err := scope.Validate(); err != nil {
		t.Fatalf("compiled scope failed validation: %v", err)
	}
}

// Python skips a cw_object_model_inst value it cannot read a key from. This
// side refuses the strategy under its own reason instead: a skipped value is
// a target that silently names fewer objects than it was written to, and no
// other value shape has been seen to exist. Each refusal says what was
// wrong, so the sample that proves a shape exists is the refusal itself.
func TestAnObjectModelValueOfUnknownShapeRefusesTheStrategyByName(t *testing.T) {
	for name, document := range map[string]string{
		"missing instance": `{"id":1,"target":[[{"field":"cw_object_model_inst","method":"eq","value":[{"cw_object_model_id":"switch"}]}]]}`,
		"missing model":    `{"id":1,"target":[[{"field":"cw_object_model_inst","method":"eq","value":[{"cw_object_model_inst_id":3}]}]]}`,
		"empty model":      `{"id":1,"target":[[{"field":"cw_object_model_inst","method":"eq","value":[{"cw_object_model_id":"","cw_object_model_inst_id":3}]}]]}`,
		"null instance":    `{"id":1,"target":[[{"field":"cw_object_model_inst","method":"eq","value":[{"cw_object_model_id":"switch","cw_object_model_inst_id":null}]}]]}`,
		"extra key":        `{"id":1,"target":[[{"field":"cw_object_model_inst","method":"eq","value":[{"cw_object_model_id":"switch","cw_object_model_inst_id":3,"name":"core"}]}]]}`,
		"nested instance":  `{"id":1,"target":[[{"field":"cw_object_model_inst","method":"eq","value":[{"cw_object_model_id":"switch","cw_object_model_inst_id":{"id":3}}]}]]}`,
		"not an object":    `{"id":1,"target":[[{"field":"cw_object_model_inst","method":"eq","value":["switch|3"]}]]}`,
		// One bad value among good ones still refuses: the good ones would
		// otherwise be published as the whole target.
		"one bad among good": `{"id":1,"target":[[{"field":"cw_object_model_inst","method":"eq","value":[{"cw_object_model_id":"switch","cw_object_model_inst_id":3},{"cw_object_model_id":"switch"}]}]]}`,
	} {
		_, err := compileTargetScope(decodeTarget(t, document), nil)
		if err == nil {
			t.Fatalf("%s compiled", name)
		}
		if !strings.Contains(err.Error(), "TARGET_SCOPE_VALUE_SHAPE") {
			t.Fatalf("%s produced %v, want the value shape named", name, err)
		}
		if reason := targetScopeDispositionReason(err); reason != "UNSUPPORTED_TARGET_VALUE_SHAPE" {
			t.Fatalf("%s is published as %q", name, reason)
		}
		if shouldRetainLastGood([]ObjectDisposition{{Disposition: DispositionUnsupported, Reason: targetScopeDispositionReason(err)}}) {
			t.Fatalf("%s would retain the previous plan", name)
		}
	}
}

// The identity pairs are Python's iter_object_model_field_pairs over the
// query configurations: every object_model_inst target_identity with both
// names, first occurrence wins, and the platform default last unless already
// named. Nothing else in a query configuration contributes.
func TestIdentityPairsAreTranscribedFromTheQueryConfigurations(t *testing.T) {
	target := decodeTarget(t, `{"id":1,"target":[[{"field":"cw_object_model_inst","method":"eq","value":[{"cw_object_model_id":"switch","cw_object_model_inst_id":1}]}]]}`)
	for name, testCase := range map[string]struct {
		configs []string
		want    string
	}{
		"no configurations": {nil, "cw_object_model_id|cw_object_model_inst_id"},
		"no target identity": {
			[]string{`{"metric_field":"usage"}`}, "cw_object_model_id|cw_object_model_inst_id",
		},
		"one renamed identity": {
			[]string{`{"target_identity":{"type":"object_model_inst","object_model_field":"model","object_model_inst_field":"inst"}}`},
			"model|inst,cw_object_model_id|cw_object_model_inst_id",
		},
		"the same pair twice keeps one": {
			[]string{
				`{"target_identity":{"type":"object_model_inst","object_model_field":"model","object_model_inst_field":"inst"}}`,
				`{"target_identity":{"type":"object_model_inst","object_model_field":"model","object_model_inst_field":"inst"}}`,
			},
			"model|inst,cw_object_model_id|cw_object_model_inst_id",
		},
		"two different pairs keep their order": {
			[]string{
				`{"target_identity":{"type":"object_model_inst","object_model_field":"m2","object_model_inst_field":"i2"}}`,
				`{"target_identity":{"type":"object_model_inst","object_model_field":"m1","object_model_inst_field":"i1"}}`,
			},
			"m2|i2,m1|i1,cw_object_model_id|cw_object_model_inst_id",
		},
		"the default named explicitly is not repeated": {
			[]string{`{"target_identity":{"type":"object_model_inst","object_model_field":"cw_object_model_id","object_model_inst_field":"cw_object_model_inst_id"}}`},
			"cw_object_model_id|cw_object_model_inst_id",
		},
		"another identity type is ignored": {
			[]string{`{"target_identity":{"type":"host","object_model_field":"model","object_model_inst_field":"inst"}}`},
			"cw_object_model_id|cw_object_model_inst_id",
		},
		"a pair missing a name is ignored": {
			[]string{`{"target_identity":{"type":"object_model_inst","object_model_field":"model"}}`},
			"cw_object_model_id|cw_object_model_inst_id",
		},
		"an unreadable configuration is ignored": {
			[]string{`not json`}, "cw_object_model_id|cw_object_model_inst_id",
		},
	} {
		configs := make([]json.RawMessage, 0, len(testCase.configs))
		for _, config := range testCase.configs {
			configs = append(configs, json.RawMessage(config))
		}
		scope, err := compileTargetScope(target, configs)
		if err != nil {
			t.Fatalf("%s: compile: %v", name, err)
		}
		pairs := make([]string, 0)
		for _, pair := range scope.Groups[0].Conditions[0].IdentityFields {
			pairs = append(pairs, pair[0]+"|"+pair[1])
		}
		if got := strings.Join(pairs, ","); got != testCase.want {
			t.Fatalf("%s: pairs = %s, want %s", name, got, testCase.want)
		}
	}
}

// Python drops a condition that yields no keys instead of letting it reject
// everything; a group left with a usable condition still applies.
func TestAConditionWithNoUsableValuesIsDroppedNotEnforced(t *testing.T) {
	target := decodeTarget(t, `{"id":1,"target":[[
		{"field":"bk_target_ip","method":"eq","value":[]},
		{"field":"host_topo_node","method":"eq","value":[{"bk_obj_id":"set","bk_inst_id":5}]}
	]]}`)
	scope, err := compileTargetScope(target, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(scope.Groups) != 1 || len(scope.Groups[0].Conditions) != 1 {
		t.Fatalf("scope = %+v", scope)
	}
	if scope.Groups[0].Conditions[0].Field != contract.TargetScopeTopoNode {
		t.Fatalf("surviving condition = %+v", scope.Groups[0].Conditions[0])
	}
}

// The catalog decoder must actually read the field. It ignored it silently
// until 2026-09-09, and nothing failed - which is how the defect lasted.
func TestTheCatalogDecoderReadsTheItemTarget(t *testing.T) {
	var strategy legacyStrategy
	decoder := json.NewDecoder(strings.NewReader(`{"id":7,"bk_biz_id":2,"items":[{"id":9,"target":[[{"field":"host_topo_node","method":"eq","value":[{"bk_obj_id":"set","bk_inst_id":1}]}]]}]}`))
	decoder.UseNumber()
	if err := decoder.Decode(&strategy); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(strategy.Items) != 1 || len(strategy.Items[0].Target) != 1 {
		t.Fatalf("decoded items = %+v", strategy.Items)
	}
}

// A rejected target has to be reported as an unsupported capability, not as a
// rejected configuration. The catalog retains the last good Plan for a rejected
// configuration, and that Plan predates the target filter - so the wrong
// disposition would leave the strategy alerting outside its target with nothing
// to show for it.
func TestARejectedTargetIsReportedAsUnsupported(t *testing.T) {
	for _, err := range []error{
		errorWithText("TARGET_SCOPE_UNSUPPORTED: service_topo_node target is not resolved yet"),
		errorWithText("TARGET_SCOPE_UNRESOLVABLE: strategy states a monitoring target that reduces to no condition"),
	} {
		reason := targetScopeDispositionReason(err)
		if !strings.HasPrefix(reason, "UNSUPPORTED_TARGET_SCOPE") {
			t.Fatalf("%v produced reason %q", err, reason)
		}
		if shouldRetainLastGood([]ObjectDisposition{{Disposition: DispositionUnsupported, Reason: reason}}) {
			t.Fatalf("a plan rejected for %q would retain its previous unfiltered plan", reason)
		}
	}
}

type textError string

func (e textError) Error() string { return string(e) }

func errorWithText(text string) error { return textError(text) }
