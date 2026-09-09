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
	scope, err := compileTargetScope(target)
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
	scope, err := compileTargetScope(target)
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
		scope, err := compileTargetScope(decodeTarget(t, document))
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
	scope, err := compileTargetScope(target)
	if err == nil {
		t.Fatalf("an unresolvable target compiled to %+v", scope)
	}
	if !strings.Contains(err.Error(), "TARGET_SCOPE_UNRESOLVABLE") {
		t.Fatalf("error = %v", err)
	}
}

// Targets whose membership is not a property of the strategy, or that need a
// topology alarmd cannot resolve yet, must reject rather than be dropped: a
// silently ignored target is exactly the defect being fixed.
func TestTargetsThatCannotBeFrozenRejectThePlan(t *testing.T) {
	for _, document := range []string{
		`{"id":1,"target":[[{"field":"dynamic_group","method":"eq","value":[{"dynamic_group_id":"abc"}]}]]}`,
		`{"id":1,"target":[[{"field":"service_topo_node","method":"eq","value":[{"bk_obj_id":"module","bk_inst_id":3}]}]]}`,
		`{"id":1,"target":[[{"field":"something_new","method":"eq","value":[{"bk_obj_id":"module","bk_inst_id":3}]}]]}`,
	} {
		if _, err := compileTargetScope(decodeTarget(t, document)); err == nil {
			t.Fatalf("%s compiled instead of rejecting", document)
		} else if !strings.Contains(err.Error(), "TARGET_SCOPE_UNSUPPORTED") {
			t.Fatalf("%s produced %v", document, err)
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
	scope, err := compileTargetScope(target)
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
