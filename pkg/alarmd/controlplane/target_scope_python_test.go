// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

// Legacy target compilation, from bk-monitor c0e828fa8c:
// alarm_backends/core/control/item.py:123-126 and
// bkmonitor/utils/range/target.py:36-158.

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func hostCondition(method string) legacyTargetCondition {
	return legacyTargetCondition{Field: "bk_target_ip", Method: method,
		Values: []json.RawMessage{json.RawMessage(`{"bk_target_ip":"192.0.2.7","bk_target_cloud_id":0}`)}}
}

// The method is lower-cased, not trimmed, and matched against "eq"; anything
// else is an exclusion (target.py:41,147-150) - an empty method, " eq" and a
// word Python has no meaning for included. None refuses the strategy.
func TestAConditionIsAnInclusionOnlyWhenItsMethodIsEq(t *testing.T) {
	for method, want := range map[string]contract.TargetScopeMethod{
		"eq": contract.TargetScopeInclude, "EQ": contract.TargetScopeInclude,
		"neq": contract.TargetScopeExclude, "": contract.TargetScopeExclude,
		" eq": contract.TargetScopeExclude, "include": contract.TargetScopeExclude,
	} {
		scope, err := compileTargetScope([][]legacyTargetCondition{{hostCondition(method)}}, nil)
		if err != nil {
			t.Fatalf("method %q: %v", method, err)
		}
		if got := scope.Groups[0].Conditions[0].Method; got != want {
			t.Errorf("method %q compiled as %s, want %s", method, got, want)
		}
	}
}

// Python builds no target condition when target[0] is empty, whatever the
// groups after it say (item.py:123-126): the item is not filtered at all.
func TestOnlyTheFirstGroupDecidesWhetherThereIsATarget(t *testing.T) {
	for name, target := range map[string][][]legacyTargetCondition{
		"no groups":                      {},
		"an empty first group":           {{}},
		"an empty first group, then one": {{}, {hostCondition("eq")}},
	} {
		if scope, err := compileTargetScope(target, nil); err != nil || scope != nil {
			t.Errorf("%s: scope %+v, error %v; want no target", name, scope, err)
		}
	}
	scope, err := compileTargetScope([][]legacyTargetCondition{{hostCondition("eq")}, {}}, nil)
	if err != nil || scope == nil || len(scope.Groups) != 1 {
		t.Errorf("a first group, then an empty one: scope %+v, error %v; want the first group's scope", scope, err)
	}
}
