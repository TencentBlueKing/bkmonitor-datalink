// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"context"
	"fmt"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/jsonpath"
)

// CMDBAccess 在字段规则之后按最终业务 ID 补名称，并补旧 KAC 权限标签和动态分组。
// 只有受控处理器可以写权限字段，用户属性赋值仍不能修改权限结果。
type CMDBAccess struct{}

// Name 返回稳定注册名。
func (CMDBAccess) Name() string { return "cmdb-access" }

// Match 允许来源显式选择在规则完成后执行身份收尾。
func (CMDBAccess) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 独立补齐业务名称、权限和分组；某项依赖失败不撤销其他成功补丁。
func (CMDBAccess) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	state, err := scope.EvaluationState()
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	document := state.Document()
	read := func(name string) any {
		for _, root := range []string{"labels", "extra_data", "dimensions"} {
			obj, _ := document[root].(map[string]any)
			v := obj[name]
			if v != nil && fmt.Sprint(v) != "" && fmt.Sprint(v) != "[]" {
				return v
			}
		}
		return nil
	}
	patches := []domain.EnrichAssignment{}
	set := func(name string, value any) error {
		target, e := jsonpath.ParseTarget("$.extra_data." + name)
		if e != nil {
			return e
		}
		assignment, e := domain.NewEnrichAssignment(target, value)
		if e != nil {
			return e
		}
		patches = append(patches, assignment)
		return nil
	}
	// 1. 名称使用 KAC 相同的字段存在性优先级；空 labels 不能回退到另一业务 ID。
	namePatch, diagnostics, err := fillBusinessName(ctx, scope, document)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	if namePatch != nil {
		patches = append(patches, *namePatch)
	}
	// 2. 权限和分组保留原有身份收尾，不因名称依赖失败丢弃成功结果。
	model, inst := "", ""
	if v := read("model_id"); v != nil {
		model = fmt.Sprint(v)
	}
	if v := read("model_inst_id"); v != nil {
		inst = fmt.Sprint(v)
	}
	if read("cw_labels") == nil {
		labels := []string{}
		for _, name := range []string{"biz", "set"} {
			v := read("bk_" + name + "_id")
			if v != nil && fmt.Sprint(v) != "0" {
				labels = append(labels, name, name+"|"+legacyPermissionText(v))
			}
		}
		if model != "" && inst != "" {
			labels = append(labels, model, model+"|"+inst)
		}
		if err = set("cw_labels", labels); err != nil {
			return enrich.ProcessorResult{}, err
		}
	}
	status := domain.EnrichStatusSucceeded
	if len(diagnostics) != 0 {
		status = domain.EnrichStatusPartial
	}
	result, err := scope.AssignmentResult(status, patches)
	result.Diagnostics = diagnostics
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	if read("dynamic_group_id") == nil && model != "" && inst != "" {
		ids, e := scope.DynamicGroupIDs(ctx, model, inst)
		if e != nil {
			if ctx.Err() != nil {
				return enrich.ProcessorResult{}, ctx.Err()
			}
			result.Status = domain.EnrichStatusPartial
			if len(patches) == 0 {
				result.Status = domain.EnrichStatusFailed
			}
			result.Diagnostics = append(result.Diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: "dynamic_group"})
			return result, nil
		}
		if ids == nil {
			ids = []string{}
		}
		if err = set("dynamic_group_id", ids); err != nil {
			return enrich.ProcessorResult{}, err
		}
		result, err = scope.AssignmentResult(result.Status, patches)
		result.Diagnostics = diagnostics
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
	}
	return result, nil
}

// fillBusinessName 只补缺失的有效名称，不刷新非空来源值或显式规则值。
// labels 与 extra_data 按存在性选取，保持最终 KAC 投影的 ID/名称优先级。
func fillBusinessName(ctx context.Context, scope *enrich.Scope, document map[string]any) (*domain.EnrichAssignment, []enrich.Diagnostic, error) {
	read := func(field string) (any, string, bool) {
		for _, root := range []string{"labels", "extra_data"} {
			object, _ := document[root].(map[string]any)
			if value, found := object[field]; found {
				return value, root, true
			}
		}
		return nil, "extra_data", false
	}
	name, root, _ := read("bk_biz_name")
	if name != nil && name != "" {
		return nil, nil, nil
	}
	value, _, present := read("bk_biz_id")
	if !present {
		return nil, nil, nil
	}
	diagnostic := func(code enrich.DiagnosticCode) []enrich.Diagnostic {
		return []enrich.Diagnostic{{Code: code, Dependency: "onemodel_instance", Fields: []string{"bk_biz_name"}}}
	}
	id, valid := resourceCMDBID(value)
	if !valid {
		return nil, []enrich.Diagnostic{{Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"bk_biz_id"}}}, nil
	}
	business, found, err := scope.Instance(ctx, enrich.InstanceQuery{ModelCode: "cw-biz", InstanceID: id})
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if err != nil {
		return nil, diagnostic(enrich.DiagnosticCodeDependencyInvalid), nil
	}
	if !found {
		return nil, diagnostic(enrich.DiagnosticCodeMissingField), nil
	}
	if business.TenantID != scope.Facts().BKTenantID || business.ModelCode != "cw-biz" || business.InstanceID != id {
		return nil, diagnostic(enrich.DiagnosticCodeDependencyInvalid), nil
	}
	fields := mergeInstanceFields(business.Fields, business.Attributes)
	nameText, ok := fields["bk_biz_name"].(string)
	if !ok || nameText == "" {
		return nil, diagnostic(enrich.DiagnosticCodeMissingField), nil
	}
	target, err := jsonpath.ParseTarget("$." + root + ".bk_biz_name")
	if err != nil {
		return nil, nil, err
	}
	patch, err := domain.NewEnrichAssignment(target, nameText)
	return &patch, nil, err
}

func legacyPermissionText(v any) string {
	if values, ok := v.([]any); ok {
		parts := make([]string, len(values))
		for i, value := range values {
			parts[i] = fmt.Sprint(value)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return fmt.Sprint(v)
}
