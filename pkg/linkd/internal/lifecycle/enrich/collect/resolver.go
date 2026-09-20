// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package collect 集中解析采集任务告警的配置和对象实例，并投影到既有 Enrich 分组。
// 本包只读取 Scope 与 Alert 维度，所有产物均为新值，不修改 Alert 的任何原有字段。
package collect

import (
	"context"
	"math"
	"strconv"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

// Result 是 Collect 对 resource 和 display 分组的完整场景贡献。
type Result struct {
	Resource            models.ResourceValues
	ResourceDiagnostics []enrich.Diagnostic
	Object              string
}

// Enrich 一次解析 Collect 依赖并生成 resource 与 display 贡献。
func Enrich(ctx context.Context, scope *enrich.Scope, dimensions domain.DimensionMap, fallbackBizID int64, subjectName string) (Result, error) {
	cached, err := scope.Scenario("collect", func() (any, error) {
		resolution, resolveErr := resolve(ctx, scope, dimensions)
		if resolveErr != nil {
			return nil, resolveErr
		}
		projection := projectResource(resolution, fallbackBizID)
		return Result{
			Resource: projection.Values, ResourceDiagnostics: projection.Diagnostics,
			Object: projectDisplay(resolution, subjectName),
		}, nil
	})
	if err != nil {
		return Result{}, err
	}
	return cached.(Result), nil
}

// Resolution 是 Resolver 与两个 Projector 之间的包内协议。
// 字段保持私有，调用方只需将结果交给对应 Projector。
type Resolution struct {
	tenantID string

	taskInputState taskInputState
	taskIdentity   string
	config         models.CollectConfig
	configFound    bool
	configErr      error

	instance      enrich.Instance
	instanceFound bool
	instanceErr   error
	model         enrich.Model
	modelFound    bool
	modelErr      error

	cloudHostLookup bool
	cloudHost       enrich.Instance
	cloudHostFound  bool
	cloudHostErr    error

	topologyRequired  bool
	relatedHost       enrich.Instance
	relatedHostFound  bool
	relatedHostErr    error
	hostTopology      models.ResourceTopology
	hostTopologyFound bool
	hostTopologyErr   error
}

type taskInputState uint8

const (
	taskInputMissing taskInputState = iota
	taskInputInvalid
	taskInputValid
)

func resolve(
	ctx context.Context,
	scope *enrich.Scope,
	dimensions domain.DimensionMap,
) (Resolution, error) {
	resolution := Resolution{tenantID: scope.Alert().BKTenantID}
	task, exists := dimensions[rules.FieldBKCollectConfigID]
	resolution.taskIdentity, resolution.taskInputState = collectTaskIdentity(task, exists)
	if resolution.taskInputState != taskInputValid {
		return resolution, nil
	}

	resolution.config, resolution.configFound, resolution.configErr = scope.CollectConfig(ctx, resolution.taskIdentity)
	if err := ctx.Err(); err != nil {
		return Resolution{}, err
	}
	if resolution.configErr != nil || !resolution.configFound || !validConfig(resolution) {
		return resolution, nil
	}

	resolution.model, resolution.modelFound, resolution.modelErr = scope.ModelByCode(ctx, resolution.config.BKObjectCode)
	if err := ctx.Err(); err != nil {
		return Resolution{}, err
	}
	query := enrich.InstanceQuery{
		ModelCode:  resolution.config.BKObjectCode,
		InstanceID: strconv.FormatInt(resolution.config.BKInstID, 10),
	}
	// 服务实例的业务身份属于统一实例来源属性，使用 long 类型槽参与精确匹配。
	if rules.IsServiceInstanceModel(resolution.config.BKObjectCode) && resolution.config.FinalBizID != nil && *resolution.config.FinalBizID > 0 {
		query.AttributeFilters = append(query.AttributeFilters, enrich.InstanceAttributeFilter{
			Field: rules.FieldCWBIZID, Type: enrich.InstanceAttributeLong, Value: *resolution.config.FinalBizID,
		})
	}
	resolution.instance, resolution.instanceFound, resolution.instanceErr = scope.Instance(ctx, query)
	if err := ctx.Err(); err != nil {
		return Resolution{}, err
	}
	// KAC 对主机始终补业务拓扑；非主机仅在实例自身缺少业务时沿 host_related_field 补偿。
	// IsRemoteCollect 表示执行目标可能是关联主机，资源身份仍固定使用 CollectConfig.BKInstID。
	resolution.topologyRequired = resolution.config.BKObjectCode == rules.HostModelCode ||
		resolution.config.IsRemoteCollect || !instanceHasBusiness(resolution.instance)
	if !resolution.topologyRequired {
		query, ok := collectCloudHostQuery(dimensions)
		if ok && !instanceHasCloudName(resolution.instance) {
			resolution.cloudHostLookup = true
			resolution.cloudHost, resolution.cloudHostFound, resolution.cloudHostErr = scope.Instance(ctx, query)
			if err := ctx.Err(); err != nil {
				return Resolution{}, err
			}
		}
		return resolution, nil
	}
	if resolution.config.BKObjectCode == rules.HostModelCode {
		resolution.relatedHost = resolution.instance
		resolution.relatedHostFound = resolution.instanceFound && resolution.instanceErr == nil
		resolution.relatedHostErr = resolution.instanceErr
		resolution.hostTopology, resolution.hostTopologyFound, resolution.hostTopologyErr = scope.HostTopology(
			ctx, strconv.FormatInt(resolution.config.BKInstID, 10),
		)
		if err := ctx.Err(); err != nil {
			return Resolution{}, err
		}
		return resolution, nil
	}
	if resolution.modelErr == nil && resolution.modelFound {
		if field, ok := rules.FirstStringField(resolution.model.Fields, rules.FieldHostRelatedField); ok && field != "" {
			resolution.relatedHost, resolution.relatedHostFound, resolution.relatedHostErr = scope.RelatedHost(
				ctx, resolution.config.BKObjectCode, strconv.FormatInt(resolution.config.BKInstID, 10), field,
			)
			if resolution.relatedHostFound && resolution.relatedHostErr == nil {
				hostID := rules.FirstStringFieldValue(resolution.relatedHost.Fields, rules.FieldBKHostID, rules.FieldBKInstID, rules.FieldModelInstID)
				if hostID != "" {
					resolution.hostTopology, resolution.hostTopologyFound, resolution.hostTopologyErr = scope.HostTopology(ctx, hostID)
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Resolution{}, err
	}
	return resolution, nil
}

func collectCloudHostQuery(dimensions domain.DimensionMap) (enrich.InstanceQuery, bool) {
	cloudID, cloudExists := dimensions[rules.FieldBKTargetCloudID]
	if !cloudExists {
		cloudID, cloudExists = dimensions[rules.FieldBKCloudID]
	}
	cloudIdentity, validCloud := collectCloudID(cloudID)
	if !cloudExists || !validCloud {
		return enrich.InstanceQuery{}, false
	}
	for _, field := range []string{rules.FieldBKHostID, rules.FieldBKTargetHostID} {
		value, exists := dimensions[field]
		if !exists {
			continue
		}
		identity := rules.ScalarIdentity(value)
		parsed, err := strconv.ParseInt(identity, 10, 64)
		if err == nil && parsed > 0 && strconv.FormatInt(parsed, 10) == identity {
			return enrich.InstanceQuery{ModelCode: rules.HostModelCode, InstanceID: identity, AttributeFilters: []enrich.InstanceAttributeFilter{
				{Field: rules.FieldBKCloudID, Type: enrich.InstanceAttributeLong, Value: cloudIdentity},
			}}, true
		}
	}
	ip, ipExists := dimensions[rules.FieldBKTargetIP]
	if !ipExists {
		ip, ipExists = dimensions[rules.FieldIP]
	}
	ipText, validIP := ip.StringValue()
	if !ipExists || !validIP || ipText == "" {
		return enrich.InstanceQuery{}, false
	}
	return enrich.InstanceQuery{ModelCode: rules.HostModelCode, AttributeFilters: []enrich.InstanceAttributeFilter{
		{Field: rules.FieldBKHostInnerIP, Type: enrich.InstanceAttributeKeyword, Value: ipText},
		{Field: rules.FieldBKCloudID, Type: enrich.InstanceAttributeLong, Value: cloudIdentity},
	}}, true
}

func collectCloudID(value domain.Scalar) (int64, bool) {
	if number, ok := value.NumberValue(); ok {
		if number >= 0 && number < float64(math.MaxInt64) && math.Trunc(number) == number {
			return int64(number), true
		}
		return 0, false
	}
	if text, ok := value.StringValue(); ok {
		parsed, err := strconv.ParseInt(text, 10, 64)
		return parsed, err == nil && parsed >= 0 && strconv.FormatInt(parsed, 10) == text
	}
	return 0, false
}

func collectTaskIdentity(value domain.Scalar, exists bool) (string, taskInputState) {
	if !exists || !rules.ScalarProvided(value) {
		return "", taskInputMissing
	}
	switch value.Kind() {
	case domain.ScalarKindString, domain.ScalarKindNumber:
		return rules.ScalarIdentity(value), taskInputValid
	default:
		return "", taskInputInvalid
	}
}

func instanceHasCloudName(instance enrich.Instance) bool {
	fields := make(map[string]any, len(instance.Fields)+len(instance.Attributes))
	for key, value := range instance.Attributes {
		fields[key] = value
	}
	for key, value := range instance.Fields {
		fields[key] = value
	}
	name, found := rules.FirstStringField(fields, rules.FieldBKCloudName)
	return found && name != ""
}

func instanceHasBusiness(instance enrich.Instance) bool {
	return rules.FirstStringFieldValue(instance.Fields, rules.FieldBKBizID, rules.FieldCWBIZID) != ""
}

func validConfig(resolution Resolution) bool {
	return resolution.config.BKTenantID == resolution.tenantID &&
		resolution.config.BKCollectTaskID == resolution.taskIdentity &&
		resolution.config.BKObjectCode != "" && resolution.config.BKInstID > 0
}
