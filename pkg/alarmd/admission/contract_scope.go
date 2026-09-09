// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"

// TargetScopeFromContract converts the frozen wire scope into the shape the
// filter evaluates.
//
// The filter keeps its own shape - key sets instead of ordered slices - so
// matching costs a map lookup rather than a scan, and so a wire change cannot
// quietly alter what the predicate means. This is the single conversion point
// between the two.
func TargetScopeFromContract(source *contract.TargetScopeV2) *TargetScope {
	if source == nil {
		return nil
	}
	scope := &TargetScope{Groups: make([]TargetScopeGroup, 0, len(source.Groups))}
	for _, group := range source.Groups {
		converted := TargetScopeGroup{Conditions: make([]TargetScopeCondition, 0, len(group.Conditions))}
		for _, condition := range group.Conditions {
			keys := make(map[string]struct{}, len(condition.Keys))
			for _, key := range condition.Keys {
				keys[key] = struct{}{}
			}
			converted.Conditions = append(converted.Conditions, TargetScopeCondition{
				Field:  TargetScopeField(condition.Field),
				Method: TargetScopeMethod(condition.Method),
				Keys:   keys,
			})
		}
		scope.Groups = append(scope.Groups, converted)
	}
	return scope
}
