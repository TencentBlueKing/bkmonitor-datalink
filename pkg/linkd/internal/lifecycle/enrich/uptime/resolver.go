// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package uptime 集中解析拨测告警的任务、节点和对象模型，并投影到既有 Enrich 分组。
// 本包只读取 Scope 与 Alert 维度，所有产物均为新值，不修改 Alert 的任何原有字段。
package uptime

import (
	"context"
	"math"
	"net"
	"strconv"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

// Result 是 Uptime 对 resource 和 display 分组的完整场景贡献。
type Result struct {
	Resource            models.ResourceValues
	ResourceDiagnostics []enrich.Diagnostic
	Object              string
	Dimensions          []models.DimensionDisplay
	DisplayDiagnostics  []enrich.Diagnostic
}

// Enrich 一次解析 Uptime 依赖并生成 resource 与 display 贡献。
func Enrich(ctx context.Context, scope *enrich.Scope, modelCode string, dimensions domain.DimensionMap, fallbackBizID ...int64) (Result, error) {
	bizID := int64(0)
	if len(fallbackBizID) != 0 {
		bizID = fallbackBizID[0]
	}
	cached, err := scope.Scenario("uptime:"+modelCode, func() (any, error) {
		resolution, resolveErr := resolve(ctx, scope, modelCode, dimensions, bizID)
		if resolveErr != nil {
			return nil, resolveErr
		}
		resource := projectResource(resolution, bizID)
		display, projectErr := projectDisplay(ctx, resolution, dimensions, resource.Values)
		if projectErr != nil {
			return nil, projectErr
		}
		return Result{
			Resource: resource.Values, ResourceDiagnostics: resource.Diagnostics,
			Object: display.Object, Dimensions: display.Dimensions, DisplayDiagnostics: display.Diagnostics,
		}, nil
	})
	if err != nil {
		return Result{}, err
	}
	return cached.(Result), nil
}

// Resolution 是 Resolver 与两个 Projector 之间的内部协议。
// 字段保持私有，调用方只能将结果交给 Projector，避免依赖解析过程。
type Resolution struct {
	tenantID   string
	modelCode  string
	model      enrich.Model
	modelFound bool
	modelErr   error

	businessID    int64
	business      enrich.Instance
	businessFound bool
	businessErr   error
	cloudHost     enrich.Instance
	cloudFound    bool
	cloudErr      error
	cloudID       int64
	cloudAddress  string

	taskInputState taskInputState
	taskIdentity   string
	taskRaw        domain.Scalar
	task           models.UptimeTask
	taskFound      bool
	taskErr        error

	nodeProvided bool
	nodeIdentity string
	nodeRaw      domain.Scalar
	node         models.UptimeNode
	nodeFound    bool
	nodeErr      error
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
	modelCode string,
	dimensions domain.DimensionMap,
	fallbackBizID int64,
) (Resolution, error) {
	alert := scope.Alert()
	resolution := Resolution{
		tenantID: alert.BKTenantID, modelCode: modelCode, taskRaw: domain.NewStringScalar("0"),
	}

	if task, exists := dimensions[rules.FieldTaskID]; exists {
		resolution.taskRaw = task
	}
	resolution.taskIdentity, resolution.taskInputState = taskIdentity(resolution.taskRaw)
	if resolution.taskInputState == taskInputValid {
		resolution.task, resolution.taskFound, resolution.taskErr = scope.UptimeTask(ctx, resolution.taskIdentity)
		if err := ctx.Err(); err != nil {
			return Resolution{}, err
		}
	}

	businessID := fallbackBizID
	if resolution.taskErr == nil && resolution.taskFound && validTask(resolution) && resolution.task.BKBizID > 0 {
		businessID = resolution.task.BKBizID
	}
	resolution.businessID = businessID
	if businessID > 0 {
		resolution.business, resolution.businessFound, resolution.businessErr = scope.Instance(ctx, enrich.InstanceQuery{
			ModelCode: "cw-biz", InstanceID: strconv.FormatInt(businessID, 10),
		})
		if err := ctx.Err(); err != nil {
			return Resolution{}, err
		}
	}

	if node, exists := dimensions[rules.FieldNodeID]; exists && rules.ScalarProvided(node) {
		resolution.nodeProvided = true
		resolution.nodeRaw = node
		resolution.nodeIdentity = rules.ScalarIdentity(node)
		resolution.node, resolution.nodeFound, resolution.nodeErr = scope.UptimeNode(ctx, resolution.nodeIdentity)
		if err := ctx.Err(); err != nil {
			return Resolution{}, err
		}
	}

	if ip, ok := targetHostAddress(dimensions); ok {
		if cloudID, valid := targetCloudIdentity(dimensions); valid {
			resolution.cloudID, resolution.cloudAddress = cloudID, ip
			resolution.cloudHost, resolution.cloudFound, resolution.cloudErr = scope.Instance(ctx, enrich.InstanceQuery{
				ModelCode: rules.HostModelCode,
				AttributeFilters: []enrich.InstanceAttributeFilter{
					{Field: rules.FieldBKHostInnerIP, Type: enrich.InstanceAttributeKeyword, Value: ip},
					{Field: rules.FieldBKCloudID, Type: enrich.InstanceAttributeLong, Value: cloudID},
				},
			})
			if err := ctx.Err(); err != nil {
				return Resolution{}, err
			}
		}
	}

	resolution.model, resolution.modelFound, resolution.modelErr = scope.ModelByCode(ctx, modelCode)
	if err := ctx.Err(); err != nil {
		return Resolution{}, err
	}
	return resolution, nil
}

func targetHostAddress(dimensions domain.DimensionMap) (string, bool) {
	for _, field := range []string{rules.FieldBKTargetIP, rules.FieldTargetHost} {
		if value, exists := dimensions[field]; exists {
			if ip, valid := value.StringValue(); valid && net.ParseIP(ip) != nil {
				return ip, true
			}
		}
	}
	return "", false
}

func targetCloudIdentity(dimensions domain.DimensionMap) (int64, bool) {
	value, exists := dimensions[rules.FieldBKTargetCloudID]
	if !exists {
		return 0, false
	}
	if number, valid := value.NumberValue(); valid && number >= 0 && number < float64(math.MaxInt64) && math.Trunc(number) == number {
		return int64(number), true
	}
	if text, valid := value.StringValue(); valid {
		cloudID, err := strconv.ParseInt(text, 10, 64)
		return cloudID, err == nil && cloudID >= 0 && strconv.FormatInt(cloudID, 10) == text
	}
	return 0, false
}

func taskIdentity(value domain.Scalar) (string, taskInputState) {
	switch value.Kind() {
	case domain.ScalarKindNumber:
		number, _ := value.NumberValue()
		if number == 0 {
			return "", taskInputMissing
		}
		if number < 0 || math.Trunc(number) != number {
			return "", taskInputInvalid
		}
		return strconv.FormatInt(int64(number), 10), taskInputValid
	case domain.ScalarKindString:
		text, _ := value.StringValue()
		if text == "" {
			return "", taskInputMissing
		}
		number, err := strconv.ParseInt(text, 10, 64)
		if err != nil || number < 0 {
			return "", taskInputInvalid
		}
		if number == 0 {
			return "", taskInputMissing
		}
		return strconv.FormatInt(number, 10), taskInputValid
	default:
		return "", taskInputInvalid
	}
}
