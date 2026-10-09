// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"linkd/internal/onemodel"
)

// TargetReader 解析当前业务范围与完整实例目标；生命周期和预览复用同一只读契约。
type TargetReader interface {
	ResolveScope(context.Context, string, string) (onemodel.TargetScope, error)
	Resolve(context.Context, string, string, onemodel.TargetDescriptor) (onemodel.TargetResult, error)
}

// GroupMatch 记录一个配置条件组的独立匹配，合并准入可同时命中多个组。
type GroupMatch struct {
	Index int `json:"index"`
	MatchResult
}

// Verdict 是无副作用的策略判定，不增加计数、不占窗口、不建立关系。
// Evaluated=false 表示跳过，而不是普通未命中；仅包含稳定原因码和位置。
type Verdict struct {
	Evaluated bool         `json:"evaluated"`
	Matched   bool         `json:"matched"`
	Reason    string       `json:"reason,omitempty"`
	Groups    []GroupMatch `json:"groups"`
	GroupKey  string       `json:"group_key,omitempty"`
}

// EvaluateConditions 应用启用/时段、业务范围、完整目标与逐组条件，不计算分组字段。
// rely=true 只用于依赖屏蔽被屏蔽候选；目标描述约束主候选，子候选仍受同一业务范围约束。
func EvaluateConditions(ctx context.Context, release Release, compiled *Compiled, view *FactView, targets TargetReader, at time.Time, rely bool) (Verdict, error) {
	result := Verdict{Evaluated: true, Groups: []GroupMatch{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if compiled == nil || view == nil || release.TenantID != view.TenantID || compiled.Kind != release.Kind || release.Compiled.Digest != compiled.Summary.Digest {
		return result, fmt.Errorf("policy evaluation scope or release mismatch")
	}
	action, err := view.Field(ctx, "action")
	if err != nil {
		return result, err
	}
	if action.Data != "firing" {
		result.Reason = "terminal_bypass"
		return result, nil
	}
	if release.Deleted || !compiled.Active(at) {
		result.Reason = "inactive"
		return result, nil
	}
	if rely && (compiled.Shield == nil || compiled.Shield.ShieldType != "rely_shield" || compiled.Rely == nil) {
		return result, fmt.Errorf("rely evaluation requires dependency shield")
	}
	skip := func(reason string, err error) (Verdict, error) {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.Evaluated = false
		result.Matched = false
		result.Reason = reason
		return result, nil
	}
	if targets == nil {
		return skip("target_reader_unavailable", ErrUnavailable)
	}
	scope, err := targets.ResolveScope(ctx, release.TenantID, compiled.Common.SpaceCode)
	if err != nil {
		return skip("business_scope_unavailable", err)
	}
	if scope.TenantID != view.TenantID || scope.SpaceCode != compiled.Common.SpaceCode {
		return result, fmt.Errorf("resolved business scope tenant mismatch")
	}
	business, err := view.Field(ctx, "bk_biz_id")
	if err != nil {
		return skip("business_field_unavailable", err)
	}
	if !business.Present || business.Data == nil {
		result.Reason = "business_field_missing"
		return result, nil
	}
	text, err := queryScalar(FieldNumber, business.Data)
	if err != nil {
		return skip("business_field_invalid", err)
	}
	// canonicalNumber 是有理数文本；业务 ID 只允许正整数，不接受布尔、负值和分数。
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil || id < 1 {
		return skip("business_field_invalid", err)
	}
	if !slices.Contains(scope.BusinessIDs, id) {
		result.Reason = "outside_business_scope"
		return result, nil
	}
	if compiled.Common.Targets != nil && !rely {
		candidate, found, err := view.CanonicalInstance(ctx)
		if err != nil {
			return skip("instance_field_unavailable", err)
		}
		if !found {
			result.Reason = "instance_field_missing"
			return result, nil
		}
		if candidate.ModelID != compiled.Common.ModelID {
			result.Reason = "outside_target_model"
			return result, nil
		}
		resolved, err := targets.Resolve(ctx, release.TenantID, compiled.Common.SpaceCode, *compiled.Common.Targets)
		if err != nil {
			return skip("target_resolution_failed", err)
		}
		if resolved.Scope.TenantID != view.TenantID || resolved.Scope.SpaceCode != compiled.Common.SpaceCode {
			return result, fmt.Errorf("resolved target scope mismatch")
		}
		found = false
		for _, ref := range resolved.Instances {
			if ref.ModelID != compiled.Common.ModelID || ref.EntityUID != ref.ModelID+"|"+ref.InstanceID {
				return skip("target_identity_invalid", ErrUnavailable)
			}
			if ref == candidate {
				found = true
			}
		}
		if !found {
			result.Reason = "outside_target_instances"
			return result, nil
		}
	}
	expressions := compiled.Conditions
	if rely {
		expressions = []*Expression{compiled.Rely}
	}
	for i, expression := range expressions {
		var origin Reader
		if rely && view.origin != nil {
			origin = view.origin
		}
		match, err := expression.match(ctx, view, origin)
		result.Groups = append(result.Groups, GroupMatch{Index: i, MatchResult: match})
		if err != nil {
			if !errors.Is(err, ErrUnavailable) {
				return result, err
			}
			result.Evaluated = false
		}
		result.Matched = result.Matched || match.Matched
	}
	if !result.Evaluated {
		result.Matched = false
		result.Reason = "condition_unavailable"
		return result, nil
	}
	if !result.Matched {
		result.Reason = "conditions_not_matched"
		return result, nil
	}
	return result, nil
}

// EvaluateGrouping 独立求分组键，使组合抑制先完成防抖，再处理本次聚合的字段无效问题。
func EvaluateGrouping(ctx context.Context, compiled *Compiled, view *FactView) (string, string, error) {
	if compiled == nil || view == nil {
		return "", "group_field_unavailable", ErrInvalid
	}
	fields := []string{}
	if compiled.Merge != nil {
		fields = compiled.Merge.Fields
	}
	if compiled.Suppression != nil {
		for _, scheme := range compiled.Summary.Schemes {
			if scheme.Type == "aggregation" {
				fields = scheme.Fields
			}
		}
	}
	values := make([]any, 0, len(fields))
	for _, name := range fields {
		value, err := view.Field(ctx, name)
		if err != nil {
			return "", "group_field_unavailable", err
		}
		if !value.Present {
			return "", "invalid_group_value", ErrUnavailable
		}
		values = append(values, value.Data)
	}
	if compiled.Merge != nil || len(fields) > 0 {
		key, err := GroupKey(values)
		if err != nil {
			return "", "invalid_group_value", err
		}
		return key, "", nil
	}
	return "", "", nil
}

// Evaluate 为只读预览组合条件与分组求值；正式抑制按 scheme 顺序分别调用。
func Evaluate(ctx context.Context, release Release, compiled *Compiled, view *FactView, targets TargetReader, at time.Time, rely bool) (Verdict, error) {
	result, err := EvaluateConditions(ctx, release, compiled, view, targets, at, rely)
	if err != nil || !result.Evaluated || !result.Matched {
		return result, err
	}
	key, reason, err := EvaluateGrouping(ctx, compiled, view)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		result.Evaluated = false
		result.Matched = false
		result.Reason = reason
		return result, nil
	}
	result.GroupKey = key
	return result, nil
}
