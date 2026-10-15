// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package basetarget

import (
	"net/netip"
	"strconv"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/rules"
)

// 无数据只描述检测结果；主机资源仍可使用原生 ID、明确的 CMDB Subject，
// 或带云区域的地址定位。这里只构造查询，不向 Event 维度注入资源身份。
func nativeHostQuery(dimensions domain.DimensionMap, event domain.Event) (enrich.InstanceQuery, []enrich.Diagnostic, inputState) {
	for _, field := range []string{rules.FieldBKInstID, rules.FieldBKTargetHostID, rules.FieldBKHostID} {
		value, exists := dimensions[field]
		if !exists || !rules.ScalarProvided(value) {
			continue
		}
		id, valid := positiveIntegerIdentity(value)
		if !valid {
			return enrich.InstanceQuery{}, invalidField(field), inputInvalid
		}
		if hostSubjectConflict(event, id) {
			return enrich.InstanceQuery{}, []enrich.Diagnostic{{Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"subject_id", "dimensions." + field}}}, inputInvalid
		}
		return enrich.InstanceQuery{ModelCode: rules.HostModelCode, InstanceID: id}, nil, inputValid
	}
	if event.SubjectSystem == "cmdb" && event.SubjectType == "host" && event.SubjectID != "" {
		id, valid := positiveIntegerIdentity(domain.NewStringScalar(event.SubjectID))
		if !valid {
			return enrich.InstanceQuery{}, []enrich.Diagnostic{{Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"subject_id"}}}, inputInvalid
		}
		return enrich.InstanceQuery{ModelCode: rules.HostModelCode, InstanceID: id}, nil, inputValid
	}
	ip, hasIP := dimensionAlias(dimensions, rules.FieldBKTargetIP, rules.FieldIP)
	cloud, hasCloud := dimensionAlias(dimensions, rules.FieldBKTargetCloudID, rules.FieldBKCloudID)
	if !hasIP || !hasCloud {
		return enrich.InstanceQuery{}, missingFields(rules.FieldBKHostID, rules.FieldBKTargetHostID, rules.FieldBKTargetIP, rules.FieldBKTargetCloudID), inputMissing
	}
	address, valid := ip.StringValue()
	if _, err := netip.ParseAddr(address); !valid || err != nil {
		return enrich.InstanceQuery{}, invalidField(rules.FieldBKTargetIP), inputInvalid
	}
	cloudID, err := strconv.ParseInt(rules.ScalarIdentity(cloud), 10, 64)
	if err != nil || cloudID < 0 || strconv.FormatInt(cloudID, 10) != rules.ScalarIdentity(cloud) {
		return enrich.InstanceQuery{}, invalidField(rules.FieldBKTargetCloudID), inputInvalid
	}
	return enrich.InstanceQuery{ModelCode: rules.HostModelCode, AttributeFilters: []enrich.InstanceAttributeFilter{
		{Field: rules.FieldBKHostInnerIP, Type: enrich.InstanceAttributeKeyword, Value: address},
		{Field: rules.FieldBKCloudID, Type: enrich.InstanceAttributeLong, Value: cloudID},
	}}, nil, inputValid
}

func dimensionAlias(dimensions domain.DimensionMap, primary, alias string) (domain.Scalar, bool) {
	value, exists := dimensions[primary]
	if exists {
		return value, true
	}
	value, exists = dimensions[alias]
	return value, exists
}

func hostSubjectConflict(event domain.Event, instanceID string) bool {
	return instanceID != "" && event.SubjectSystem == "cmdb" && event.SubjectType == "host" &&
		event.SubjectID != "" && event.SubjectID != instanceID
}
