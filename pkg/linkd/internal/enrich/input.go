// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"math"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/enrich/models"
)

const (
	labelStrategyID      = "strategy_id"
	labelStrategyVersion = "strategy_version"
	labelBizID           = "bk_biz_id"
)

// RequiredIDs 是内置监控丰富入口校验后的稳定查询身份。
type RequiredIDs struct {
	StrategyID      int64
	StrategyVersion int64
	BizID           int64
}

// ValidateStrategyIdentity 只校验策略查询所需的租户、策略 ID 和执行修订。
// bk_biz_id 不参与历史策略定位；来源空间和资源业务由各自消费者另行校验。
func ValidateStrategyIdentity(alert domain.Event) (models.StrategyRevisionQuery, []Diagnostic) {
	identity := models.StrategyRevisionQuery{TenantID: alert.BKTenantID}
	diagnostics := validateIDFields(alert, []requiredIDField{
		{name: labelStrategyID, dest: &identity.ID},
		{name: labelStrategyVersion, dest: &identity.Version},
	})
	if identity.TenantID == "" {
		diagnostics = append(diagnostics, Diagnostic{Code: DiagnosticCodeMissingField, Fields: []string{"bk_tenant_id"}})
	} else if strings.TrimSpace(identity.TenantID) != identity.TenantID {
		diagnostics = append(diagnostics, Diagnostic{Code: DiagnosticCodeInvalidField, Fields: []string{"bk_tenant_id"}})
	}
	return identity, diagnostics
}

// ValidateRequiredIDs 为需要来源业务的丰富步骤校验三个正整数标签。
// 策略查询和策略关联应使用 ValidateStrategyIdentity，不以业务身份作为前提。
func ValidateRequiredIDs(alert domain.Event) (RequiredIDs, []Diagnostic) {
	ids := RequiredIDs{}
	diagnostics := validateIDFields(alert, []requiredIDField{
		{name: labelStrategyID, dest: &ids.StrategyID},
		{name: labelStrategyVersion, dest: &ids.StrategyVersion},
		{name: labelBizID, dest: &ids.BizID},
	})
	return ids, diagnostics
}

// ValidateBusinessID 仅供真实查询以 CMDB 业务作为定位条件的分支使用。
func ValidateBusinessID(event domain.Event) (int64, []Diagnostic) {
	var id int64
	diagnostics := validateIDFields(event, []requiredIDField{{name: labelBizID, dest: &id}})
	return id, diagnostics
}

// OptionalBusinessID 返回可用的 CMDB 业务；负数空间、缺失或非法值不作为资源业务兜底。
// 不依赖业务的查询不得因此失败；真正需要业务的分支应调用 ValidateBusinessID。
func OptionalBusinessID(event domain.Event) int64 {
	id, _ := ValidateBusinessID(event)
	return id
}

type requiredIDField struct {
	name string
	dest *int64
}

func validateIDFields(alert domain.Event, fields []requiredIDField) []Diagnostic {
	missing := make([]string, 0, len(fields))
	invalid := make([]string, 0, len(fields))
	for _, field := range fields {
		value, exists := alert.Labels[field.name]
		if !exists {
			missing = append(missing, "labels."+field.name)
			continue
		}
		number, ok := value.NumberValue()
		if !ok || number <= 0 || math.Trunc(number) != number || number > math.MaxInt64 {
			invalid = append(invalid, "labels."+field.name)
			continue
		}
		*field.dest = int64(number)
	}
	diagnostics := make([]Diagnostic, 0, 2)
	if len(missing) != 0 {
		diagnostics = append(diagnostics, Diagnostic{Code: DiagnosticCodeMissingField, Fields: missing})
	}
	if len(invalid) != 0 {
		diagnostics = append(diagnostics, Diagnostic{Code: DiagnosticCodeInvalidField, Fields: invalid})
	}
	return diagnostics
}
