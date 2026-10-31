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
	"strconv"
	"strings"

	"linkd/internal/enrich"
	"linkd/internal/enrich/basetarget"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

// 主机上下文独立于告警对象，MySQL 等对象的 canonical 身份、业务和展示
// 均不因所在主机被找到而改变。Scope 缓存复用主机 Resource/Display 的地址查询。
func resourceHostContext(ctx context.Context, scope *enrich.Scope, values *models.ResourceValues) ([]enrich.Diagnostic, error) {
	event := scope.Facts()
	dimensions := event.Dimensions
	var query enrich.InstanceQuery
	canonicalHost := false
	if values.ModelID == rules.HostModelCode {
		_, hasModel := dimensions[rules.FieldModelID]
		_, hasInstance := dimensions[rules.FieldModelInstID]
		if hasModel || hasInstance {
			if values.ModelInstID == "" {
				// 主资源已保留身份或依赖错误；不能再按地址绑定采集主机。
				return nil, nil
			}
			// 主机告警的主机信息属于被采实例，远程执行主机的地址和
			// Subject 不能覆盖该身份；查询复用主资源已确认的实例。
			canonicalHost = true
			query = enrich.InstanceQuery{ModelCode: rules.HostModelCode, InstanceID: values.ModelInstID}
		}
	}
	if !canonicalHost {
		_, targetIP := dimensions[rules.FieldBKTargetIP]
		_, ip := dimensions[rules.FieldIP]
		_, targetCloud := dimensions[rules.FieldBKTargetCloudID]
		_, cloud := dimensions[rules.FieldBKCloudID]
		if !targetIP && !ip || !targetCloud && !cloud {
			return nil, nil
		}
		var diagnostics []enrich.Diagnostic
		query, diagnostics = basetarget.HostAddressQuery(dimensions)
		if len(diagnostics) != 0 {
			return diagnostics, nil
		}
	}
	host, found, err := scope.Instance(ctx, query)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	invalid := func() ([]enrich.Diagnostic, error) {
		return []enrich.Diagnostic{{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyOneModel, Fields: []string{"bk_host_id", "bk_host_name"}}}, nil
	}
	if err != nil || !found || host.TenantID != event.BKTenantID || host.ModelCode != rules.HostModelCode || canonicalHost && host.InstanceID != query.InstanceID {
		return invalid()
	}
	id, parseErr := strconv.ParseInt(host.InstanceID, 10, 64)
	if parseErr != nil || id <= 0 || strconv.FormatInt(id, 10) != host.InstanceID {
		return invalid()
	}
	fields := mergeInstanceFields(host.Fields, host.Attributes)
	if raw, exists := fields[rules.FieldBKHostID]; exists && rules.FirstStringFieldValue(map[string]any{rules.FieldBKHostID: raw}, rules.FieldBKHostID) != host.InstanceID {
		return invalid()
	}
	// 地址补充不能覆盖已确认的主机主键。显式 model_id/model_inst_id
	// 存在时，原生字段可属于远程执行主机，不再要求它们与被采实例一致。
	conflicts := []string{}
	if values.ModelID == rules.HostModelCode && values.ModelInstID != "" && values.ModelInstID != host.InstanceID {
		conflicts = append(conflicts, "model_inst_id")
	}
	if !canonicalHost {
		for _, field := range []string{rules.FieldBKHostID, rules.FieldBKTargetHostID} {
			if value, exists := dimensions[field]; exists && rules.ScalarIdentity(value) != host.InstanceID {
				conflicts = append(conflicts, "dimensions."+field)
			}
		}
	}
	if len(conflicts) != 0 {
		return []enrich.Diagnostic{{Code: enrich.DiagnosticCodeInvalidField, Fields: conflicts}}, nil
	}
	values.BKHostID = id
	name, ok := fields["bk_host_name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return []enrich.Diagnostic{{Code: enrich.DiagnosticCodeMissingField, Dependency: rules.DependencyOneModel, Fields: []string{"bk_host_name"}}}, nil
	}
	values.BKHostName = name
	return nil, nil
}
