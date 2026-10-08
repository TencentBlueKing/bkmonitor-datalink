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
	"encoding/json"
	"fmt"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/enrich"
)

// CMDBAccess 在字段规则之后按有效身份补旧 KAC 权限标签和动态分组；保留非空现有值。
// 只有受控处理器可以写这两个字段，用户属性赋值仍不能修改权限结果。
type CMDBAccess struct{}

// Name 返回稳定注册名。
func (CMDBAccess) Name() string { return "cmdb-access" }

// Match 允许来源显式选择在规则完成后执行身份收尾。
func (CMDBAccess) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 只读取租户分组投影，失败不丢弃已经生成的权限标签。
func (CMDBAccess) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	document, err := domain.EventDocument(scope.EffectiveEvent(), scope.Evaluation())
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
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
	patches := []domain.EnrichPatch{}
	set := func(name string, value any) error {
		raw, e := json.Marshal(value)
		if e != nil {
			return e
		}
		patches = append(patches, domain.EnrichPatch{Op: "set", Path: "$.extra_data." + name, Value: raw})
		return nil
	}
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
	result := enrich.ProcessorResult{Status: domain.EnrichStatusSucceeded, Patches: patches}
	if read("dynamic_group_id") == nil && model != "" && inst != "" {
		ids, e := scope.DynamicGroupIDs(ctx, model, inst)
		if e != nil {
			if ctx.Err() != nil {
				return enrich.ProcessorResult{}, ctx.Err()
			}
			result.Status = domain.EnrichStatusPartial
			if len(result.Patches) == 0 {
				result.Status = domain.EnrichStatusFailed
			}
			result.Diagnostics = []enrich.Diagnostic{{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: "dynamic_group"}}
			return result, nil
		}
		if ids == nil {
			ids = []string{}
		}
		if err = set("dynamic_group_id", ids); err != nil {
			return enrich.ProcessorResult{}, err
		}
		result.Patches = patches
	}
	return result, nil
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
